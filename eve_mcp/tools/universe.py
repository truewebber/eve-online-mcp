"""Universe lookups: name search, systems, routes, item types, live danger data."""
from __future__ import annotations

import asyncio
from typing import Annotated, Any, Literal

from mcp.server.mcpserver import MCPServer
from pydantic import Field

from ..context import AppContext
from ..schema import CharacterArg, limit_arg
from ._common import handled, isk, page

_ResultsPerCategoryLimit = limit_arg("results per category", 50)
_SystemsLimit = limit_arg("systems", 100)

_SEARCH_CATEGORIES = (
    "agent",
    "alliance",
    "character",
    "constellation",
    "corporation",
    "faction",
    "inventory_type",
    "region",
    "solar_system",
    "station",
    "structure",
)

_ROUTE_PREFERENCES = {"shorter": "Shorter", "safer": "Safer", "less_secure": "LessSecure"}

#: Minimum candidates to rank before slicing to `limit`. A pool that scales only
#: with `limit` re-loses the exact match: at limit=1 a four-id pool never even
#: sees 'Tritanium'.
_RANK_POOL = 50


def register(mcp: MCPServer, ctx: AppContext) -> None:
    @mcp.tool()
    @handled
    async def eve_universe_search(
        query: Annotated[
            str,
            Field(
                description=(
                    "At least 3 characters. Prefix match by default, so 'Trit' finds "
                    "'Tritanium'."
                ),
                min_length=3,
            ),
        ],
        categories: Annotated[
            str,
            Field(
                description=(
                    "Comma-separated subset of: agent, alliance, character, "
                    "constellation, corporation, faction, inventory_type, region, "
                    "solar_system, station, structure. Narrow this — searching every "
                    "category returns a lot of irrelevant matches."
                )
            ),
        ] = "inventory_type,solar_system,station,region",
        strict: Annotated[
            bool,
            Field(description="Exact-match instead of prefix match."),
        ] = False,
        character: CharacterArg = "",
        limit: _ResultsPerCategoryLimit = 10,
    ) -> dict[str, Any]:
        """Resolve a partial or misspelled name to the exact EVE name and its id.

        Call this first whenever you are not certain of a name. eve_market_price,
        eve_universe_route, eve_universe_item and eve_ui_set_waypoint all require
        exact in-game names and will refuse anything else — this is how you find
        the right spelling.

        Note this searches EVE's global index and needs an authorized character's
        token, because the `structure` category only returns structures that
        character can actually see.

        Returns: one section per requested category, each with total and
        results[] of {id, name}.
        """
        wanted = [c.strip() for c in categories.split(",") if c.strip()]
        invalid = [c for c in wanted if c not in _SEARCH_CATEGORIES]
        if invalid:
            return {
                "error": (
                    f"Unknown categories {invalid}. Valid values: "
                    f"{', '.join(_SEARCH_CATEGORIES)}"
                )
            }

        token = ctx.resolve_character(character)
        ctx.require_scope(token, "esi-search.search_structures.v1", "the search index")

        raw, used = await _search_with_fallback(ctx, token.character_id, wanted, query, strict)

        out: dict[str, Any] = {"query": query, "strict": strict}
        if used != query:
            out["matched_on_prefix"] = used
            out["note"] = (
                f"Nothing matched {query!r} exactly. ESI matches on prefix, not "
                f"fuzzily, so the search was retried with the shorter prefix "
                f"{used!r}. Check that the result below is really what was meant."
            )

        # Resolve more than `limit` so ranking has something to choose from: ESI
        # returns prefix matches in its own order, so the exact base name ('Tritanium')
        # can sit behind longer derivatives ('Tritanium Prospecting Array 1 Blueprint').
        pool = min(max(4 * limit, _RANK_POOL), 200)
        all_ids = {i for ids in raw.values() for i in (ids or [])[:pool]}
        names = await ctx.resolver.names(all_ids, character_id=token.character_id)
        for category, ids in raw.items():
            ids = ids or []
            ranked = sorted(
                ({"id": i, "name": names.get(i, f"#{i}")} for i in ids[:pool]),
                key=lambda r: (len(r["name"]), r["name"]),
            )
            out[category] = {"total": len(ids), "results": ranked[:limit]}
        if not any(raw.values()):
            out["note"] = (
                f"No matches for {query!r} even after shortening the prefix. ESI "
                "searches by prefix, so a typo in the first few characters cannot "
                "be recovered. Try a different part of the name, or widen `categories`."
            )
        return out

    @mcp.tool()
    @handled
    async def eve_universe_item(
        item: Annotated[
            str,
            Field(
                description=(
                    "Exact item type name, e.g. 'Rifter'. Use eve_universe_search "
                    "if unsure of the spelling."
                ),
                min_length=1,
            ),
        ],
    ) -> dict[str, Any]:
        """Item type reference: group, volume, mass, capacity and description.

        The two volumes differ and the difference matters for hauling: ships and
        assembled containers occupy `volume_m3`, but packaged (repackaged, empty)
        they occupy the much smaller `packaged_volume_m3`. Cargo capacity maths
        should use the packaged figure unless the item is assembled.

        For what it actually costs, use eve_market_price instead — the price here
        is CCP's global average, not a live quote.

        Returns: item, group, volume_m3, packaged_volume_m3, mass_kg, capacity_m3,
        description, ccp_average_price.
        """
        match = (await ctx.resolver.resolve_names([item], only=("inventory_types",)))[
            item.strip().lower()
        ]
        if match.chosen is None:
            return {
                "error": (
                    f"No item type is named exactly {item!r}. Call eve_universe_search "
                    "with this text to find the real name."
                )
            }
        type_id = match.chosen.id
        info = await ctx.resolver.type_info(type_id)
        # Group ids live in their own id space, so the bulk name endpoint would
        # resolve them to whatever inventory type happens to share the number.
        group_name = await ctx.resolver.group_name(info.get("group_id", 0))
        price = await ctx.resolver.reference_price(type_id)
        out: dict[str, Any] = {
            "item": info.get("name"),
            "type_id": type_id,
            "group": group_name,
            "volume_m3": info.get("volume"),
            "packaged_volume_m3": info.get("packaged_volume"),
            "mass_kg": info.get("mass"),
            "capacity_m3": info.get("capacity"),
            "published": info.get("published"),
            "ccp_average_price": isk(price),
            "description": (info.get("description") or "")[:500],
        }
        if match.ambiguous:
            out["ambiguity_note"] = (
                f"{len(match.alternatives) + 1} item types are named {item!r}; showing "
                f"#{type_id}. Others: "
                + ", ".join(f"#{m.id}" for m in match.alternatives)
                + ". Call eve_universe_search with categories='inventory_type' to pick."
            )
        return out

    @mcp.tool()
    @handled
    async def eve_universe_system(
        system: Annotated[
            str,
            Field(description="Exact solar system name, e.g. 'Jita'.", min_length=1),
        ],
    ) -> dict[str, Any]:
        """Security status, region, and the last hour of kills and jumps for one system.

        Security class is what decides how dangerous a system is: `highsec`
        (0.5 and above) has CONCORD response to unprovoked aggression, `lowsec`
        (0.1-0.4) has none, `nullsec` (0.0 and below) has none and usually
        belongs to a player alliance.

        The kill counts are a live one-hour window, so this doubles as a
        "is it hot right now" check before flying somewhere.

        Returns: system, region, security_status, security_class, kills and
        jumps in the last hour.
        """
        match = (await ctx.resolver.resolve_names([system], only=("systems",)))[
            system.strip().lower()
        ]
        if match.chosen is None:
            return {
                "error": (
                    f"No solar system is named exactly {system!r}. Call "
                    "eve_universe_search with categories='solar_system'."
                )
            }
        system_id, name = match.chosen.id, match.chosen.name

        info_result, kills_result, jumps_result = await asyncio.gather(
            ctx.esi.get(f"/universe/systems/{system_id}"),
            ctx.esi.get("/universe/system_kills"),
            ctx.esi.get("/universe/system_jumps"),
        )
        info = info_result.data or {}
        kills = next((k for k in (kills_result.data or []) if k.get("system_id") == system_id), {})
        jumps = next((j for j in (jumps_result.data or []) if j.get("system_id") == system_id), {})

        region_name = None
        if info.get("constellation_id"):
            constellation = await ctx.esi.get(
                f"/universe/constellations/{info['constellation_id']}"
            )
            region_id = (constellation.data or {}).get("region_id")
            if region_id:
                region_name = await ctx.resolver.name(region_id)

        security = info.get("security_status", 0.0)
        return {
            "system": name,
            "system_id": system_id,
            "region": region_name,
            "security_status": round(security, 2),
            "security_class": _sec_band(security),
            "stations": len(info.get("stations", [])),
            "stargates": len(info.get("stargates", [])),
            "ship_kills_last_hour": kills.get("ship_kills", 0),
            "pod_kills_last_hour": kills.get("pod_kills", 0),
            "npc_kills_last_hour": kills.get("npc_kills", 0),
            "jumps_last_hour": jumps.get("ship_jumps", 0),
            "data_age": kills_result.stale_note,
        }

    @mcp.tool()
    @handled
    async def eve_universe_route(
        origin: Annotated[
            str, Field(description="Exact origin system name.", min_length=1)
        ],
        destination: Annotated[
            str, Field(description="Exact destination system name.", min_length=1)
        ],
        preference: Annotated[
            Literal["shorter", "safer", "less_secure"],
            Field(
                description=(
                    "'shorter' is fewest jumps regardless of danger. 'safer' prefers "
                    "high-security space and is what a hauler wants. 'less_secure' "
                    "deliberately avoids high-sec, for characters with poor standings."
                )
            ),
        ] = "shorter",
        avoid: Annotated[
            str,
            Field(
                description=(
                    "Comma-separated exact system names to route around, e.g. "
                    "'Uedama,Niarja' — the classic hauling ambush points."
                )
            ),
        ] = "",
        show_hops: Annotated[
            bool,
            Field(
                description=(
                    "Include the full system-by-system list. Off returns just the "
                    "summary counts, which is what most questions need and costs a "
                    "fraction of the tokens on a long route."
                )
            ),
        ] = False,
    ) -> dict[str, Any]:
        """Gate-to-gate route between two systems, with the danger profile of each hop.

        `safe` in the result means the whole route stays in high-security space.
        Note that "high-sec" is not the same as "safe from players": suicide
        ganking happens in high-sec, most notoriously at Uedama and Niarja on
        the Jita-Amarr run. If the user is hauling something valuable, mention
        the `avoid` parameter.

        Returns: jumps, lowsec_systems, nullsec_systems, safe, and route[] when
        show_hops is true.
        """
        pref = _ROUTE_PREFERENCES.get(preference.strip().lower())
        if pref is None:
            return {"error": f"preference must be one of {sorted(_ROUTE_PREFERENCES)}"}

        wanted = [origin, destination] + [a.strip() for a in avoid.split(",") if a.strip()]
        lookup = await ctx.resolver.ids_from_names(wanted)
        found = {s["name"].lower(): s["id"] for s in (lookup.get("systems") or [])}
        origin_id = found.get(origin.strip().lower())
        destination_id = found.get(destination.strip().lower())
        if not origin_id or not destination_id:
            missing = [n for n in (origin, destination) if n.strip().lower() not in found]
            return {
                "error": (
                    f"Unknown system name(s): {missing}. Names must be exact — call "
                    "eve_universe_search with categories='solar_system'."
                )
            }

        body: dict[str, Any] = {"preference": pref}
        avoid_ids = [
            found[a.strip().lower()]
            for a in avoid.split(",")
            if a.strip() and a.strip().lower() in found
        ]
        if avoid_ids:
            body["avoid_systems"] = avoid_ids

        route = await ctx.esi.post(f"/route/{origin_id}/{destination_id}", json_body=body)
        hops = (route or {}).get("route", [])
        if not hops:
            return {
                "error": (
                    "No gate route exists between those systems. They may be in "
                    "wormhole space, or every path is excluded by `avoid`."
                )
            }

        details = await asyncio.gather(
            *(ctx.esi.get(f"/universe/systems/{sid}") for sid in hops),
            return_exceptions=True,
        )
        steps = []
        lowsec = nullsec = 0
        for sid, detail in zip(hops, details):
            data = detail.data if hasattr(detail, "data") else {}
            security = (data or {}).get("security_status", 0.0)
            band = _sec_band(security)
            if band == "lowsec":
                lowsec += 1
            elif band == "nullsec":
                nullsec += 1
            steps.append(
                {
                    "system": (data or {}).get("name", str(sid)),
                    "security": round(security, 1),
                    "class": band,
                }
            )

        out = {
            "origin": origin,
            "destination": destination,
            "preference": pref,
            "jumps": len(hops) - 1,
            "lowsec_systems": lowsec,
            "nullsec_systems": nullsec,
            "safe": lowsec == 0 and nullsec == 0,
            "avoided": [n.strip() for n in avoid.split(",") if n.strip()] or None,
        }
        if show_hops:
            out["route"] = steps
        elif not out["safe"]:
            out["dangerous_hops"] = [s["system"] for s in steps if s["class"] != "highsec"][:20]
        return {k: v for k, v in out.items() if v is not None}

    @mcp.tool()
    @handled
    async def eve_universe_hotspots(
        limit: _SystemsLimit = 10,
    ) -> dict[str, Any]:
        """Systems with the most ship and pod kills in the last hour, by name.

        Shows where the fighting is right now. High `npc_kills` with low ship
        kills just means busy ratting, not danger to players — it is the ship
        and pod kills that indicate PvP.

        Returns: window, systems[] sorted by player kills.
        """
        result = await ctx.esi.get("/universe/system_kills")
        rows = sorted(
            (k for k in (result.data or []) if isinstance(k, dict)),
            key=lambda k: -(k.get("ship_kills", 0) + k.get("pod_kills", 0)),
        )[:limit]
        names = await ctx.resolver.names({r["system_id"] for r in rows})
        visible, meta = page(
            [
                {
                    "system": names.get(r["system_id"], f"#{r['system_id']}"),
                    "ship_kills": r.get("ship_kills", 0),
                    "pod_kills": r.get("pod_kills", 0),
                    "npc_kills": r.get("npc_kills", 0),
                }
                for r in rows
            ],
            limit,
        )
        return {"window": "last hour", "data_age": result.stale_note, **meta, "systems": visible}


def _sec_band(security: float) -> str:
    if security >= 0.45:
        return "highsec"
    if security > 0.0:
        return "lowsec"
    return "nullsec"


#: Shortest prefix worth retrying — below this ESI returns noise.
_MIN_PREFIX = 3


async def _search_with_fallback(
    ctx: AppContext,
    character_id: int,
    categories: list[str],
    query: str,
    strict: bool,
) -> tuple[dict[str, Any], str]:
    """Search, shortening the prefix until something matches.

    ESI matches on prefix, not fuzzily, so a typo anywhere in the word returns
    nothing at all — 'Tritanum' finds no Tritanium. Trimming the tail one
    character at a time recovers any typo that is not in the first few letters,
    which covers most of them.
    """
    attempt = query.strip()
    while True:
        result = await ctx.esi.get(
            f"/characters/{character_id}/search",
            character_id=character_id,
            params={"categories": categories, "search": attempt, "strict": strict},
        )
        data = result.data or {}
        if any(data.values()) or strict or len(attempt) <= _MIN_PREFIX:
            return data, attempt
        attempt = attempt[:-1]
