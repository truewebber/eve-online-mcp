"""Industry jobs, planetary interaction and the mining ledger."""
from __future__ import annotations

import asyncio
from collections import defaultdict
from datetime import datetime, timezone
from typing import Annotated, Any

from mcp.server.mcpserver import MCPServer
from pydantic import Field

from ..context import AppContext
from ..schema import CharacterArg, DetailArg, limit_arg
from ._common import handled, isk, page, project, unit_price
from .character import _human_delta, _parse

_JobsLimit = limit_arg("jobs", 200)
_OreTypesLimit = limit_arg("ore types", 100)

_ACTIVITIES = {
    1: "Manufacturing",
    3: "Researching Time Efficiency",
    4: "Researching Material Efficiency",
    5: "Copying",
    7: "Reverse Engineering",
    8: "Invention",
    9: "Reactions",
    11: "Reactions",
}


def register(mcp: MCPServer, ctx: AppContext) -> None:
    @mcp.tool()
    @handled
    async def eve_industry_jobs(
        character: CharacterArg = "",
        include_completed: Annotated[
            bool,
            Field(
                description=(
                    "Also return jobs that already delivered. Off by default because "
                    "the live question is almost always 'what is running and what can "
                    "I collect'."
                )
            ),
        ] = False,
        limit: _JobsLimit = 20,
        response_format: DetailArg = "concise",
    ) -> dict[str, Any]:
        """Manufacturing, research, invention and reaction jobs with time remaining.

        Jobs whose end time has passed show `ready: true` — they are finished
        but still need collecting in game, and the materials stay locked until
        someone does. Surfacing that count is usually the useful part.

        Returns: active_jobs, ready_to_deliver, jobs[] sorted by end time.
        'detailed' adds the ISK install cost and the raw end_date.
        """
        token = ctx.resolve_character(character)
        cid = token.character_id
        ctx.require_scope(token, "esi-industry.read_character_jobs.v1", "industry jobs")
        concise = response_format == "concise"

        result = await ctx.esi.get(
            f"/characters/{cid}/industry/jobs",
            character_id=cid,
            params={"include_completed": include_completed},
        )
        jobs = [j for j in (result.data or []) if isinstance(j, dict)]
        if not jobs:
            return {
                "character": token.character_name,
                "jobs": [],
                "note": "No industry jobs. Pass include_completed=true to see finished ones.",
            }

        type_ids = {j["blueprint_type_id"] for j in jobs} | {
            j["product_type_id"] for j in jobs if j.get("product_type_id")
        }
        names = await ctx.resolver.names(type_ids)
        places = await ctx.resolver.names(
            {j.get("station_id") or j.get("output_location_id") for j in jobs if j},
            character_id=cid,
        )
        now = datetime.now(timezone.utc)

        rows = []
        for job in jobs:
            end = _parse(job.get("end_date"))
            ready = end is not None and end <= now
            rows.append(
                {
                    "activity": _ACTIVITIES.get(
                        job.get("activity_id"), f"#{job.get('activity_id')}"
                    ),
                    "product": names.get(job.get("product_type_id"))
                    or names.get(job["blueprint_type_id"]),
                    "runs": job.get("runs"),
                    "ends_in": "ready to deliver"
                    if ready
                    else (_human_delta(end - now) if end else "unknown"),
                    "location": places.get(job.get("station_id") or job.get("output_location_id")),
                    "ready": ready,
                    "status": job.get("status"),
                    "blueprint": names.get(job["blueprint_type_id"]),
                    "install_cost": isk(job.get("cost")),
                    "end_date": job.get("end_date"),
                }
            )
        rows.sort(key=lambda r: r.get("end_date") or "")
        visible, meta = page(rows, limit)
        return {
            "character": token.character_name,
            "active_jobs": sum(1 for r in rows if not r["ready"]),
            "ready_to_deliver": sum(1 for r in rows if r["ready"]),
            "data_age": result.stale_note,
            **meta,
            "jobs": project(
                visible, ("activity", "product", "runs", "ends_in", "location"), concise
            ),
        }

    @mcp.tool()
    @handled
    async def eve_industry_planets(
        character: CharacterArg = "",
        detail: Annotated[
            bool,
            Field(
                description=(
                    "Fetch each colony's layout to report extractor expiry and stored "
                    "output. Costs one extra ESI call per colony, so leave it off for "
                    "a quick list and turn it on for 'what needs restarting'."
                )
            ),
        ] = False,
    ) -> dict[str, Any]:
        """Planetary interaction colonies: where they are and whether they have stalled.

        PI extractors run for a fixed period and then stop producing until
        restarted by hand, which is the single most common way PI income
        silently dies. Pass `detail=true` to get `extractor_expires_in` per
        colony — anything reading "expired" is currently earning nothing.

        Returns: colony_count, colonies[]. With detail: extractor_expiry and
        stored output per colony.
        """
        token = ctx.resolve_character(character)
        cid = token.character_id
        ctx.require_scope(token, "esi-planets.manage_planets.v1", "planetary colonies")

        result = await ctx.esi.get(f"/characters/{cid}/planets", character_id=cid)
        colonies = [c for c in (result.data or []) if isinstance(c, dict)]
        if not colonies:
            return {"character": token.character_name, "colonies": [], "note": "No PI colonies."}

        names = await ctx.resolver.names(
            {c["planet_id"] for c in colonies} | {c["solar_system_id"] for c in colonies}
        )
        rows = [
            {
                "planet": names.get(c["planet_id"]),
                "system": names.get(c["solar_system_id"]),
                "type": c.get("planet_type"),
                "upgrade_level": c.get("upgrade_level"),
                "pins": c.get("num_pins"),
                "planet_id": c["planet_id"],
            }
            for c in colonies
        ]

        if detail:
            details = await asyncio.gather(
                *(
                    ctx.esi.get(f"/characters/{cid}/planets/{c['planet_id']}", character_id=cid)
                    for c in colonies
                ),
                return_exceptions=True,
            )
            now = datetime.now(timezone.utc)
            for row, layout in zip(rows, details):
                if not hasattr(layout, "data"):
                    continue
                pins = layout.data.get("pins", [])
                expiries = [
                    e for e in (_parse(p.get("expiry_time")) for p in pins if p.get("expiry_time"))
                    if e
                ]
                if expiries:
                    soonest = min(expiries)
                    row["extractor_expires_in"] = (
                        "EXPIRED — producing nothing"
                        if soonest <= now
                        else _human_delta(soonest - now)
                    )
                stored: dict[int, int] = defaultdict(int)
                for pin in pins:
                    for content in pin.get("contents", []) or []:
                        stored[content["type_id"]] += content.get("amount", 0)
                if stored:
                    product_names = await ctx.resolver.names(set(stored))
                    row["stored"] = {
                        product_names.get(t, str(t)): q
                        for t, q in sorted(stored.items(), key=lambda kv: -kv[1])[:8]
                    }

        return {
            "character": token.character_name,
            "colony_count": len(rows),
            "data_age": result.stale_note,
            "colonies": rows,
        }

    @mcp.tool()
    @handled
    async def eve_industry_mining(
        character: CharacterArg = "",
        limit: _OreTypesLimit = 15,
    ) -> dict[str, Any]:
        """Mining ledger for the last ~30 days, aggregated by ore type and valued.

        Values use CCP's global average price, so treat the total as an order of
        magnitude rather than what a buyer would actually pay. For real numbers
        on a specific ore, follow up with eve_market_price.

        Returns: total_estimated_value, top_systems[], ores[] sorted by volume.
        """
        token = ctx.resolve_character(character)
        cid = token.character_id
        ctx.require_scope(token, "esi-industry.read_character_mining.v1", "the mining ledger")

        result = await ctx.esi.get_all_pages(f"/characters/{cid}/mining", character_id=cid)
        entries = [e for e in (result.data or []) if isinstance(e, dict)]
        if not entries:
            return {"character": token.character_name, "ores": [], "note": "Nothing mined recently."}

        totals: dict[int, int] = defaultdict(int)
        by_system: dict[int, int] = defaultdict(int)
        for entry in entries:
            totals[entry["type_id"]] += entry.get("quantity", 0)
            by_system[entry["solar_system_id"]] += entry.get("quantity", 0)

        names = await ctx.resolver.names(set(totals) | set(by_system))
        prices = await ctx.resolver.reference_prices()

        rows = []
        grand_total = 0.0
        for type_id, qty in sorted(totals.items(), key=lambda kv: -kv[1]):
            unit = unit_price(prices, type_id)
            value = unit * qty
            grand_total += value
            rows.append(
                {
                    "ore": names.get(type_id, f"#{type_id}"),
                    "units": qty,
                    "estimated_value": isk(value),
                }
            )
        visible, meta = page(rows, limit)
        return {
            "character": token.character_name,
            "period": "last ~30 days",
            "total_estimated_value": isk(grand_total),
            "top_systems": [
                {"system": names.get(sid, f"#{sid}"), "units": q}
                for sid, q in sorted(by_system.items(), key=lambda kv: -kv[1])[:5]
            ],
            "data_age": result.stale_note,
            **meta,
            "ores": visible,
        }
