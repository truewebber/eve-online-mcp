"""Wallet history: ISK movements and market trades."""
from __future__ import annotations

from collections import defaultdict
from typing import Annotated, Any, Literal

from mcp.server.mcpserver import MCPServer
from pydantic import Field

from ..context import AppContext
from ..schema import CharacterArg, DetailArg, limit_arg
from ._common import handled, isk, page, project

#: How many entries `limit` may display. Unrelated to how deep the fetch goes.
_IndividualEntriesLimit = limit_arg("individual entries", 200)
#: ESI returns up to 2500 transactions per response, walked backwards by
#: `from_id`. Four passes covers ~10k trades — the full retention window for
#: everyone but a full-time station trader.
_TRANSACTION_BATCH = 2500
_TRANSACTION_PAGES = 4
#: Pages of wallet journal read before giving up; mirrors the call below.
_JOURNAL_PAGES = 10


def register(mcp: MCPServer, ctx: AppContext) -> None:
    @mcp.tool()
    @handled
    async def eve_wallet_history(
        character: CharacterArg = "",
        kind: Annotated[
            Literal["journal", "transactions", "both"],
            Field(
                description=(
                    "'journal' is every ISK movement with a reason code (bounties, "
                    "fees, contracts, transfers) — use it for 'where did my money "
                    "go'. 'transactions' is market trades only, with item names and "
                    "stations — use it for 'what did I buy and sell'. 'both' returns "
                    "each in its own section."
                )
            ),
        ] = "journal",
        ref_type: Annotated[
            str,
            Field(
                description=(
                    "Journal only: keep just one reason code, e.g. 'bounty_prizes', "
                    "'player_trading', 'market_escrow', 'brokers_fee'. The unfiltered "
                    "call lists which codes actually occur, so run it once first."
                )
            ),
        ] = "",
        limit: _IndividualEntriesLimit = 15,
        response_format: DetailArg = "concise",
    ) -> dict[str, Any]:
        """Where the ISK went: journal entries and market trades, with totals by category.

        The current balance is not here — eve_character_overview already carries it.

        ESI keeps roughly the last 30 days, so this cannot answer questions about
        older activity no matter what limit you pass. The `by_category` summary is
        computed over the whole window, not just the returned rows, so it stays
        accurate even when the entry list is truncated.

        Note on transactions: `gross_margin` is simply everything sold minus
        everything bought in the window. It is not per-item profit and will
        mislead if the character was stockpiling or liquidating.

        Returns: period, totals, by_category[], and journal[] / transactions[]
        depending on `kind`.
        """
        token = ctx.resolve_character(character)
        cid = token.character_id
        ctx.require_scope(token, "esi-wallet.read_character_wallet.v1", "wallet history")
        concise = response_format == "concise"

        out: dict[str, Any] = {
            "character": token.character_name,
            "period": "last ~30 days (ESI retention limit)",
        }

        if kind in ("journal", "both"):
            out["journal_section"] = await _journal(
                ctx, cid, ref_type, limit, concise
            )
        if kind in ("transactions", "both"):
            out["transactions_section"] = await _transactions(ctx, cid, limit, concise)

        # Flatten when only one section was asked for, so the shape stays simple.
        # pop() must happen before the dict display, or `**out` still carries the
        # nested copy and the whole payload is returned twice.
        if kind == "journal":
            section = out.pop("journal_section")
            return {**out, **section}
        if kind == "transactions":
            section = out.pop("transactions_section")
            return {**out, **section}
        return out


async def _journal(
    ctx: AppContext, cid: int, ref_type: str, limit: int, concise: bool
) -> dict[str, Any]:
    result = await ctx.esi.get_all_pages(
        f"/characters/{cid}/wallet/journal", character_id=cid, max_pages=_JOURNAL_PAGES
    )
    entries = [e for e in (result.data or []) if isinstance(e, dict)]
    available_codes = sorted({e.get("ref_type", "unknown") for e in entries})
    if ref_type:
        entries = [e for e in entries if e.get("ref_type") == ref_type]
        if not entries:
            return {
                "journal": [],
                "error": (
                    f"No journal entries with ref_type {ref_type!r} in the window. "
                    f"Codes actually present: {available_codes}"
                ),
            }

    totals: dict[str, dict[str, float]] = defaultdict(lambda: {"in": 0.0, "out": 0.0, "n": 0})
    for entry in entries:
        amount = float(entry.get("amount") or 0.0)
        bucket = totals[entry.get("ref_type", "unknown")]
        bucket["in" if amount >= 0 else "out"] += amount
        bucket["n"] += 1

    by_category = sorted(
        (
            {
                "ref_type": name,
                "entries": int(b["n"]),
                "income": isk(b["in"]),
                "spending": isk(b["out"]),
                "net_isk": round(b["in"] + b["out"], 2),
            }
            for name, b in totals.items()
        ),
        key=lambda r: -abs(r["net_isk"]),
    )

    rows = [
        {
            "date": e.get("date"),
            "ref_type": e.get("ref_type"),
            "amount": isk(e.get("amount")),
            "description": e.get("description"),
            "amount_isk": e.get("amount"),
            "balance_after": isk(e.get("balance")),
            "reason": e.get("reason"),
        }
        for e in sorted(entries, key=lambda e: e.get("date", ""), reverse=True)
    ]
    visible, meta = page(rows, limit, "Raise `limit`, or narrow with `ref_type`.")
    gross_in = sum(b["in"] for b in totals.values())
    gross_out = sum(b["out"] for b in totals.values())
    out = {
        "total_income": isk(gross_in),
        "total_spending": isk(gross_out),
        "net": isk(gross_in + gross_out),
        "by_category": by_category[:15],
        "data_age": result.stale_note,
        **meta,
        "journal": project(visible, ("date", "ref_type", "amount", "description"), concise),
    }
    if result.truncated:
        out["totals_caveat"] = (
            f"Hit the {_JOURNAL_PAGES}-page read cap: the totals and by_category above "
            f"cover the newest {len(entries):,} entries, not the full window."
        )
    return out


async def _transactions(ctx: AppContext, cid: int, limit: int, concise: bool) -> dict[str, Any]:
    result = await ctx.esi.get_cursor_pages(
        f"/characters/{cid}/wallet/transactions",
        character_id=cid,
        cursor_param="from_id",
        cursor_key="transaction_id",
        batch_size=_TRANSACTION_BATCH,
        max_pages=_TRANSACTION_PAGES,
    )
    entries = [t for t in (result.data or []) if isinstance(t, dict)]
    if not entries:
        return {"transactions": [], "note": "No market trades in the retained window."}

    type_names = await ctx.resolver.names({t["type_id"] for t in entries})
    place_names = await ctx.resolver.names({t["location_id"] for t in entries}, character_id=cid)

    bought = sum(t["unit_price"] * t["quantity"] for t in entries if t.get("is_buy"))
    sold = sum(t["unit_price"] * t["quantity"] for t in entries if not t.get("is_buy"))
    rows = [
        {
            "date": t.get("date"),
            "side": "buy" if t.get("is_buy") else "sell",
            "item": type_names.get(t["type_id"]),
            "quantity": t.get("quantity"),
            "total": isk(t["unit_price"] * t["quantity"]),
            "unit_price": isk(t.get("unit_price")),
            "location": place_names.get(t["location_id"], f"#{t['location_id']}"),
        }
        for t in sorted(entries, key=lambda t: t.get("date", ""), reverse=True)
    ]
    visible, meta = page(rows, limit)
    out = {
        "total_bought": isk(bought),
        "total_sold": isk(sold),
        "gross_margin": isk(sold - bought),
        "covers": f"{rows[-1]['date']} to {rows[0]['date']} ({len(entries):,} trades)",
        "margin_caveat": "Sold minus bought over the trades in `covers`, not per-item profit.",
        "data_age": result.stale_note,
        **meta,
        "transactions": project(
            visible, ("date", "side", "item", "quantity", "total"), concise
        ),
    }
    if result.truncated:
        out["totals_caveat"] = (
            f"Only the newest {len(entries):,} trades were read, so the totals cover "
            "`covers` and not the full retention window."
        )
    return out
