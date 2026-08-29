"""Asset inventory: where things are, what they are worth, and where that ship went."""
from __future__ import annotations

from collections import defaultdict
from typing import Annotated, Any

from mcp.server.mcpserver import MCPServer
from pydantic import Field

from ..context import AppContext
from ..schema import CharacterArg, DetailArg, limit_arg
from ._common import handled, isk, line_value, page, project, root_locations, unit_price

_LocationsLimit = limit_arg("locations", 200)
_MatchingStacksLimit = limit_arg("matching stacks", 200)
_BlueprintsLimit = limit_arg("blueprints", 300)

_ItemsPerLocation = Annotated[
    int,
    Field(
        description=(
            "Maximum items to list inside each location, in 'detailed' mode. "
            "Leave at the default for routine questions — every row costs "
            "context. Raise it to build a full inventory: a location's list is "
            "complete once its length equals that location's `distinct_types`."
        ),
        ge=1,
        le=200,
    ),
]


def register(mcp: MCPServer, ctx: AppContext) -> None:
    @mcp.tool()
    @handled
    async def eve_assets_list(
        character: CharacterArg = "",
        location: Annotated[
            str,
            Field(
                description=(
                    "Case-insensitive substring of a station or structure name, e.g. "
                    "'Jita' or 'Amarr VIII'. Empty means every location."
                )
            ),
        ] = "",
        min_value: Annotated[
            float,
            Field(
                description=(
                    "Hide locations holding less than this many ISK. Useful for "
                    "skipping the long tail of near-empty hangars."
                ),
                ge=0,
            ),
        ] = 0,
        limit: _LocationsLimit = 10,
        items: _ItemsPerLocation = 5,
        response_format: DetailArg = "concise",
    ) -> dict[str, Any]:
        """Assets grouped by the station or structure they sit in, with an ISK estimate.

        Items nested inside containers and ship holds are rolled up into the
        station that ultimately holds them, so every row is somewhere the
        character could actually undock from.

        Valuation uses CCP's global average price per type, not a hub quote. It
        is fine for "roughly how much is parked here" and can be well off for
        rare or illiquid items — use eve_market_price for anything the user
        might actually sell.

        ESI caches assets for a full hour, so recent hauling will not show. The
        `data_age` field says how stale the snapshot is.

        Returns: total_estimated_value, total_locations, locations[] sorted by
        value, plus truncation metadata. In 'detailed' mode each location also
        carries top_items and raw location_id; `items` sets how many of those
        are listed, and a list shorter than `distinct_types` means the rest
        were cut.
        """
        token = ctx.resolve_character(character)
        cid = token.character_id
        ctx.require_scope(token, "esi-assets.read_assets.v1", "assets")
        concise = response_format == "concise"

        result = await ctx.esi.get_all_pages(f"/characters/{cid}/assets", character_id=cid)
        assets = [i for i in (result.data or []) if isinstance(i, dict)]
        if not assets:
            return {
                "character": token.character_name,
                "locations": [],
                "note": "This character holds no personal assets (corp hangars are separate).",
            }

        roots = root_locations(assets)
        prices = await ctx.resolver.reference_prices()
        type_names = await ctx.resolver.names({i["type_id"] for i in assets})
        place_names = await ctx.resolver.names(set(roots.values()), character_id=cid)

        buckets: dict[int, dict[str, Any]] = defaultdict(
            lambda: {"value": 0.0, "units": 0, "types": defaultdict(int)}
        )
        for item in assets:
            root = roots.get(item["item_id"])
            if root is None:
                continue
            qty = item.get("quantity", 1)
            bucket = buckets[root]
            bucket["value"] += unit_price(prices, item["type_id"]) * qty
            bucket["units"] += qty
            bucket["types"][item["type_id"]] += qty

        needle = location.strip().lower()
        rows = []
        for place_id, bucket in buckets.items():
            place = place_names.get(place_id, f"Unknown #{place_id}")
            if needle and needle not in place.lower():
                continue
            if bucket["value"] < min_value:
                continue
            top = sorted(bucket["types"].items(), key=lambda kv: -line_value(prices, kv))[:items]
            rows.append(
                {
                    "location": place,
                    "value": isk(bucket["value"]),
                    "value_isk": round(bucket["value"], 2),
                    "distinct_types": len(bucket["types"]),
                    "units": bucket["units"],
                    "location_id": place_id,
                    "top_items": [
                        f"{type_names.get(t, t)} x{q} (~{isk(unit_price(prices, t) * q)})"
                        for t, q in top
                    ],
                }
            )
        rows.sort(key=lambda r: -r["value_isk"])
        visible, meta = page(rows, limit, "Raise `limit`, or filter with `location` / `min_value`.")

        total = sum(b["value"] for b in buckets.values())
        return {
            "character": token.character_name,
            "total_estimated_value": isk(total),
            "total_locations": len(buckets),
            "matching_locations": len(rows),
            "valuation_basis": "CCP global average price per type, not a hub quote",
            "data_age": result.stale_note,
            **meta,
            "locations": project(
                visible, ("location", "value", "distinct_types", "units"), concise
            ),
        }

    @mcp.tool()
    @handled
    async def eve_assets_find(
        name: Annotated[
            str,
            Field(
                description=(
                    "Case-insensitive substring of the item type name, e.g. 'Drake' "
                    "or 'Tritanium'. Partial names are fine — this is a substring "
                    "match over the character's own items, not the global index."
                ),
                min_length=1,
            ),
        ],
        character: CharacterArg = "",
        limit: _MatchingStacksLimit = 20,
        response_format: DetailArg = "concise",
    ) -> dict[str, Any]:
        """Locate a specific item across every hangar, container and ship hold.

        Answers "where did I leave my Orca" or "do I still have any Tritanium".
        Unlike eve_assets_list this searches inside containers and reports which
        container an item sits in, so it finds things that a location summary
        hides.

        Searches personal assets only. Corporation hangars are eve_corp_assets_find
        (needs EVE_CORP_SCOPES and the Director role).

        Returns: total_units, total_stacks, matches[]. In 'detailed' mode each
        match also carries the containing item, slot flag, packaged state and
        raw item_id.
        """
        token = ctx.resolve_character(character)
        cid = token.character_id
        ctx.require_scope(token, "esi-assets.read_assets.v1", "assets")
        concise = response_format == "concise"

        result = await ctx.esi.get_all_pages(f"/characters/{cid}/assets", character_id=cid)
        items = [i for i in (result.data or []) if isinstance(i, dict)]
        type_names = await ctx.resolver.names({i["type_id"] for i in items})

        needle = name.strip().lower()
        matches = [i for i in items if needle in type_names.get(i["type_id"], "").lower()]
        if not matches:
            return {
                "character": token.character_name,
                "query": name,
                "matches": [],
                "note": (
                    "Nothing matching in personal assets. Check the spelling with "
                    "eve_universe_search, or the item may be in a corp hangar "
                    "(eve_corp_assets_find)."
                ),
            }

        roots = root_locations(items)
        by_id = {i["item_id"]: i for i in items}
        place_names = await ctx.resolver.names(
            {roots[i["item_id"]] for i in matches if i["item_id"] in roots}, character_id=cid
        )
        prices = await ctx.resolver.reference_prices()

        rows = []
        for item in matches:
            root = roots.get(item["item_id"])
            container = by_id.get(item.get("location_id"))
            qty = item.get("quantity", 1)
            rows.append(
                {
                    "item": type_names.get(item["type_id"]),
                    "quantity": qty,
                    "location": place_names.get(root, f"Unknown #{root}"),
                    "estimated_value": isk(unit_price(prices, item["type_id"]) * qty),
                    "inside": type_names.get(container["type_id"]) if container else None,
                    "slot": item.get("location_flag"),
                    "packaged": not item.get("is_singleton", False),
                    "item_id": item["item_id"],
                }
            )
        rows.sort(key=lambda r: -r["quantity"])
        visible, meta = page(rows, limit)
        return {
            "character": token.character_name,
            "query": name,
            "total_units": sum(r["quantity"] for r in rows),
            "total_stacks": len(rows),
            "data_age": result.stale_note,
            **meta,
            "matches": project(
                visible, ("item", "quantity", "location", "estimated_value"), concise
            ),
        }

    @mcp.tool()
    @handled
    async def eve_assets_blueprints(
        character: CharacterArg = "",
        limit: _BlueprintsLimit = 25,
        response_format: DetailArg = "concise",
    ) -> dict[str, Any]:
        """Blueprints with material/time efficiency and remaining runs.

        Two kinds exist and the difference matters for industry planning:
        originals (BPO) can be used forever and report `runs_left` absent,
        while copies (BPC) are consumed and report a finite `runs_left`.

        Material efficiency (0-10) cuts input materials; time efficiency (0-20)
        cuts job duration. Higher is better and researched originals are worth
        far more than unresearched ones.

        Returns: originals, copies, blueprints[] sorted originals-first then by
        material efficiency.
        """
        token = ctx.resolve_character(character)
        cid = token.character_id
        ctx.require_scope(token, "esi-characters.read_blueprints.v1", "blueprints")
        concise = response_format == "concise"

        result = await ctx.esi.get_all_pages(f"/characters/{cid}/blueprints", character_id=cid)
        blueprints = [b for b in (result.data or []) if isinstance(b, dict)]
        if not blueprints:
            return {"character": token.character_name, "blueprints": [], "note": "None owned."}

        type_names = await ctx.resolver.names({b["type_id"] for b in blueprints})
        place_names = await ctx.resolver.names(
            {b["location_id"] for b in blueprints}, character_id=cid
        )

        rows = [
            {
                "blueprint": type_names.get(b["type_id"]),
                "kind": "copy" if b.get("runs", -1) != -1 else "original",
                "material_efficiency": b.get("material_efficiency"),
                "time_efficiency": b.get("time_efficiency"),
                "runs_left": None if b.get("runs", -1) == -1 else b["runs"],
                "location": place_names.get(b["location_id"], f"#{b['location_id']}"),
                "quantity": b.get("quantity"),
            }
            for b in blueprints
        ]
        rows.sort(key=lambda r: (r["kind"] == "copy", -(r["material_efficiency"] or 0)))
        visible, meta = page(rows, limit)
        return {
            "character": token.character_name,
            "originals": sum(1 for r in rows if r["kind"] == "original"),
            "copies": sum(1 for r in rows if r["kind"] == "copy"),
            "data_age": result.stale_note,
            **meta,
            "blueprints": project(
                visible,
                ("blueprint", "kind", "material_efficiency", "time_efficiency", "runs_left"),
                concise,
            ),
        }

