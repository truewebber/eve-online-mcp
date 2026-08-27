"""Market: live prices, own orders, contracts."""
from __future__ import annotations

from datetime import datetime, timedelta, timezone
from typing import Annotated, Any

from mcp.server.mcpserver import MCPServer
from pydantic import Field

from ..config import JITA_4_4_STATION_ID, THE_FORGE_REGION_ID
from ..context import AppContext
from ..schema import CharacterArg, DetailArg, limit_arg
from ._common import handled, isk, page, project
from .character import _human_delta, _parse

_OrdersLimit = limit_arg("orders", 300)
_ContractsLimit = limit_arg("contracts", 200)


def register(mcp: MCPServer, ctx: AppContext) -> None:
    @mcp.tool()
    @handled
    async def eve_market_price(
        item: Annotated[
            str,
            Field(
                description=(
                    "Exact item type name, e.g. 'Tritanium' or 'Rifter'. Must match "
                    "the in-game name exactly — run eve_universe_search first if you "
                    "are unsure of the spelling."
                ),
                min_length=1,
            ),
        ],
        region: Annotated[
            str,
            Field(
                description=(
                    "Exact region name to price in, e.g. 'Domain' or 'Sinq Laison'. "
                    "Empty means The Forge, whose hub Jita 4-4 sets EVE's reference "
                    "prices and carries by far the deepest order book."
                )
            ),
        ] = "",
        whole_region: Annotated[
            bool,
            Field(
                description=(
                    "Price across every station in the region instead of just the "
                    "main hub. Wider coverage, but the best quote may be somewhere "
                    "inconvenient to fly to."
                )
            ),
        ] = False,
        history_days: Annotated[
            int,
            Field(
                description=(
                    "Also summarise this many days of daily price history and return "
                    "the trend. 0 skips it and keeps the call small. 14 is a good "
                    "default when the user asks whether something is moving."
                ),
                ge=0,
                le=365,
            ),
        ] = 0,
    ) -> dict[str, Any]:
        """Live best buy and sell price for an item, from real orders on the market.

        Use this — not the average price in asset or mining results — whenever
        the answer involves actually buying or selling something. `best_sell` is
        what you would pay to buy right now; `best_buy` is what you would get
        selling instantly into a buy order. The gap between them is the spread,
        and it is where trading profit lives.

        Volumes matter as much as price: a tempting quote backed by 3 units is
        not a real opportunity. `sell_volume_available` and `buy_volume_wanted`
        report the depth behind each side.

        Returns: best_sell, best_buy, spread_pct, volumes, ccp_average_price,
        packaged_volume_m3 (for hauling math), and history when history_days > 0.
        """
        match = (await ctx.resolver.resolve_names([item], only=("inventory_types",)))[
            item.strip().lower()
        ]
        if match.chosen is None:
            return {
                "error": (
                    f"No item type is named exactly {item!r}. EVE names are exact — "
                    "call eve_universe_search with this text to find the real spelling."
                )
            }
        type_id, resolved_name = match.chosen.id, match.chosen.name

        region_id = THE_FORGE_REGION_ID
        region_name = "The Forge"
        if region:
            region_match = (await ctx.resolver.resolve_names([region], only=("regions",)))[
                region.strip().lower()
            ]
            if region_match.chosen is None:
                return {
                    "error": (
                        f"No region is named exactly {region!r}. Call eve_universe_search "
                        "with categories='region' to find it."
                    )
                }
            region_id, region_name = region_match.chosen.id, region_match.chosen.name

        station = None
        if not whole_region and region_id == THE_FORGE_REGION_ID:
            station = JITA_4_4_STATION_ID

        quotes = await ctx.resolver.hub_quotes(type_id, region_id=region_id, station_id=station)
        average = await ctx.resolver.reference_price(type_id)
        info = await ctx.resolver.type_info(type_id)

        spread = None
        if quotes["best_sell"] and quotes["best_buy"]:
            spread = round(
                100 * (quotes["best_sell"] - quotes["best_buy"]) / quotes["best_sell"], 2
            )

        out: dict[str, Any] = {
            "item": resolved_name,
            "priced_at": "Jita IV-4" if station else f"all of {region_name}",
            "best_sell": isk(quotes["best_sell"]),
            "best_sell_isk": quotes["best_sell"],
            "best_buy": isk(quotes["best_buy"]),
            "best_buy_isk": quotes["best_buy"],
            "spread_pct": spread,
            "sell_volume_available": quotes["sell_volume"],
            "buy_volume_wanted": quotes["buy_volume"],
            "ccp_average_price": isk(average),
            "packaged_volume_m3": info.get("packaged_volume") or info.get("volume"),
            "data_age": quotes["data_age"],
        }
        if quotes["best_sell"] is None and quotes["best_buy"] is None:
            out["note"] = (
                "No orders at all here. Try whole_region=true, or a different region — "
                "not everything is traded outside the main hubs."
            )

        if match.ambiguous:
            out["ambiguity_note"] = (
                f"{len(match.alternatives) + 1} item types are named {item!r}; priced "
                f"#{type_id}. Others: "
                + ", ".join(f"#{m.id}" for m in match.alternatives)
                + ". Call eve_universe_search with categories='inventory_type' to pick."
            )

        if history_days:
            out["history"] = await _history(ctx, type_id, region_id, history_days)
        return {k: v for k, v in out.items() if v is not None}

    @mcp.tool()
    @handled
    async def eve_market_orders(
        character: CharacterArg = "",
        limit: _OrdersLimit = 25,
        response_format: DetailArg = "concise",
    ) -> dict[str, Any]:
        """The character's own open buy and sell orders, with fill progress and expiry.

        Two numbers are worth watching: `buy_escrow_locked` is ISK tied up in
        buy orders and unavailable to spend, and `expires_in` warns which orders
        are about to lapse. Orders that lapse are simply cancelled, and relisting
        costs another broker fee.

        Returns: open_orders, sell_side_value, buy_escrow_locked, orders[].
        'detailed' adds order range and per-order escrow.
        """
        token = ctx.resolve_character(character)
        cid = token.character_id
        ctx.require_scope(token, "esi-markets.read_character_orders.v1", "market orders")
        concise = response_format == "concise"

        result = await ctx.esi.get(f"/characters/{cid}/orders", character_id=cid)
        orders = [o for o in (result.data or []) if isinstance(o, dict)]
        if not orders:
            return {
                "character": token.character_name,
                "orders": [],
                "note": "No open market orders.",
            }

        names = await ctx.resolver.names({o["type_id"] for o in orders})
        places = await ctx.resolver.names({o["location_id"] for o in orders}, character_id=cid)
        now = datetime.now(timezone.utc)

        rows = []
        sell_value = buy_escrow = 0.0
        for order in orders:
            is_buy = order.get("is_buy_order", False)
            remaining = order.get("volume_remain", 0)
            if is_buy:
                buy_escrow += order.get("escrow", 0.0) or 0.0
            else:
                sell_value += order.get("price", 0.0) * remaining
            issued = _parse(order.get("issued"))
            expires = issued + timedelta(days=order.get("duration", 0)) if issued else None
            rows.append(
                {
                    "side": "buy" if is_buy else "sell",
                    "item": names.get(order["type_id"]),
                    "price": isk(order.get("price")),
                    "remaining": remaining,
                    "filled_pct": round(100 * (1 - remaining / order["volume_total"]), 1)
                    if order.get("volume_total")
                    else None,
                    "location": places.get(order["location_id"], f"#{order['location_id']}"),
                    "expires_in": _human_delta(expires - now) if expires else "unknown",
                    "range": order.get("range") if is_buy else None,
                    "escrow": isk(order.get("escrow")) if is_buy else None,
                }
            )
        rows.sort(key=lambda r: (r["side"], r["item"] or ""))
        visible, meta = page(rows, limit)
        return {
            "character": token.character_name,
            "open_orders": len(rows),
            "sell_side_value": isk(sell_value),
            "buy_escrow_locked": isk(buy_escrow),
            "data_age": result.stale_note,
            **meta,
            "orders": project(
                visible,
                ("side", "item", "price", "remaining", "filled_pct", "location", "expires_in"),
                concise,
            ),
        }

    @mcp.tool()
    @handled
    async def eve_market_contracts(
        character: CharacterArg = "",
        outstanding_only: Annotated[
            bool,
            Field(
                description=(
                    "Only contracts still awaiting action. Off returns finished and "
                    "expired ones too, which is usually just noise."
                )
            ),
        ] = True,
        limit: _ContractsLimit = 15,
        response_format: DetailArg = "concise",
    ) -> dict[str, Any]:
        """Contracts the character issued or was assigned, newest first.

        Courier contracts are the ones with a collateral and a reward: you post
        collateral, deliver the goods, and get it back plus the reward. A courier
        contract nearing `expires` with undelivered cargo means the collateral is
        about to be lost, which is worth flagging loudly.

        Returns: total, outstanding, contracts[]. 'detailed' adds volume,
        issue date and raw contract_id (needed by eve_ui_open_window).
        """
        token = ctx.resolve_character(character)
        cid = token.character_id
        ctx.require_scope(token, "esi-contracts.read_character_contracts.v1", "contracts")
        concise = response_format == "concise"

        result = await ctx.esi.get_all_pages(f"/characters/{cid}/contracts", character_id=cid)
        contracts = [c for c in (result.data or []) if isinstance(c, dict)]
        if outstanding_only:
            contracts = [c for c in contracts if c.get("status") == "outstanding"]
        if not contracts:
            return {
                "character": token.character_name,
                "contracts": [],
                "note": (
                    "No outstanding contracts. Pass outstanding_only=false to include "
                    "finished and expired ones."
                    if outstanding_only
                    else "This character has no contracts at all, in any state."
                ),
            }

        party_ids, place_ids = set(), set()
        for contract in contracts:
            party_ids.update(
                i for i in (contract.get("issuer_id"), contract.get("assignee_id")) if i
            )
            place_ids.update(
                i
                for i in (contract.get("start_location_id"), contract.get("end_location_id"))
                if i
            )
        names = await ctx.resolver.names(party_ids | place_ids, character_id=cid)

        rows = [
            {
                "type": c.get("type"),
                "status": c.get("status"),
                "title": c.get("title"),
                "issuer": names.get(c.get("issuer_id")),
                "price": isk(c.get("price")) if c.get("price") else None,
                "reward": isk(c.get("reward")) if c.get("reward") else None,
                "collateral": isk(c.get("collateral")) if c.get("collateral") else None,
                "from": names.get(c.get("start_location_id")),
                "to": names.get(c.get("end_location_id")),
                "expires": c.get("date_expired"),
                "volume_m3": c.get("volume"),
                "issued": c.get("date_issued"),
                "contract_id": c.get("contract_id"),
            }
            for c in sorted(contracts, key=lambda c: c.get("date_issued", ""), reverse=True)
        ]
        visible, meta = page(rows, limit)
        return {
            "character": token.character_name,
            "total": len(rows),
            "outstanding": sum(1 for r in rows if r["status"] == "outstanding"),
            "data_age": result.stale_note,
            **meta,
            "contracts": project(
                visible,
                ("type", "status", "title", "price", "reward", "collateral", "from", "to", "expires"),
                concise,
            ),
        }


async def _history(ctx: AppContext, type_id: int, region_id: int, days: int) -> dict[str, Any]:
    result = await ctx.esi.get(f"/markets/{region_id}/history", params={"type_id": type_id})
    history = [h for h in (result.data or []) if isinstance(h, dict)]
    recent = history[-days:]
    if not recent:
        return {"note": "No trade history for this item in this region."}
    avg = sum(h["average"] for h in recent) / len(recent)
    volume = sum(h["volume"] for h in recent)
    first = recent[0]["average"]
    return {
        "days": len(recent),
        "average_price": isk(avg),
        "daily_volume": int(volume / len(recent)),
        "period_low": isk(min(h["lowest"] for h in recent)),
        "period_high": isk(max(h["highest"] for h in recent)),
        "trend_pct": round(100 * (recent[-1]["average"] - first) / first, 2) if first else None,
    }
