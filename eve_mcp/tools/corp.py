"""Corporation hangars, wallets, industry and the rest of the corp read surface.

Registered only when EVE_CORP_SCOPES is on, so the tool list itself is the
boundary — same idea as write capabilities. Every call still checks the
matching ESI scope and the in-game role, because a 403 burns CCP's error
budget.
"""
from __future__ import annotations

import asyncio
import logging
from collections import defaultdict
from datetime import datetime, timedelta, timezone
from typing import Annotated, Any, Literal

from mcp.server.mcpserver import MCPServer
from pydantic import Field

from ..config import CORP_READ_SCOPES
from ..context import AppContext, Corporation
from ..esi import EsiError
from ..schema import CharacterArg, DetailArg, DivisionArg, limit_arg
from ._common import handled, isk, line_value, page, project, root_locations, unit_price
from .character import _human_delta, _parse
from .industry import _ACTIVITIES

log = logging.getLogger("eve_mcp.tools.corp")

_LocationsLimit = limit_arg("locations", 200)
_MatchingStacksLimit = limit_arg("matching stacks", 200)
_BlueprintsLimit = limit_arg("blueprints", 300)
_IndividualEntriesLimit = limit_arg("individual entries", 200)
_JobsLimit = limit_arg("jobs", 200)
_OreTypesLimit = limit_arg("ore types", 100)
_OrdersLimit = limit_arg("orders", 300)
_ContractsLimit = limit_arg("contracts", 200)
_KillmailsLimit = limit_arg("killmails", 50)
_StructuresLimit = limit_arg("structures", 200)
_MembersLimit = limit_arg("members", 500)

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

#: Corp hangars dwarf personal ones. 80 pages is 80k stacks; past that we stop
#: rather than walk the error limit down.
_ASSET_PAGES = 80
_JOURNAL_PAGES = 10
_TRANSACTION_BATCH = 2500
_TRANSACTION_PAGES = 4
_MINING_OBSERVER_PAGES = 10
_MINING_OBSERVER_CAP = 25

_HANGAR_FLAGS = {f"CorpSAG{i}": i for i in range(1, 8)}

_SCOPES = {
    "assets": "esi-assets.read_corporation_assets.v1",
    "blueprints": "esi-corporations.read_blueprints.v1",
    "wallets": "esi-wallet.read_corporation_wallets.v1",
    "jobs": "esi-industry.read_corporation_jobs.v1",
    "mining": "esi-industry.read_corporation_mining.v1",
    "orders": "esi-markets.read_corporation_orders.v1",
    "contracts": "esi-contracts.read_corporation_contracts.v1",
    "killmails": "esi-killmails.read_corporation_killmails.v1",
    "structures": "esi-corporations.read_structures.v1",
    "members": "esi-corporations.read_corporation_membership.v1",
    "divisions": "esi-corporations.read_divisions.v1",
}

#: Roles ESI actually checks. Director implies the rest; Hangar_Take_3 etc.
#: are noise in an overview and a Director holds every one of them.
_ESI_ROLES = frozenset(
    {
        "Director",
        "Accountant",
        "Junior_Accountant",
        "Factory_Manager",
        "Station_Manager",
        "Trader",
    }
)

_ROLES = {
    "assets": ("Director",),
    "blueprints": ("Director",),
    "wallets": ("Accountant", "Junior_Accountant"),
    "jobs": ("Factory_Manager",),
    "orders": ("Accountant", "Trader"),
    "killmails": ("Director",),
    "structures": ("Station_Manager",),
    "divisions": ("Director",),
    "mining_ledger": ("Accountant",),
    "mining_extractions": ("Station_Manager",),
}


def register(mcp: MCPServer, ctx: AppContext) -> None:
    @mcp.tool()
    @handled
    async def eve_corp_overview(character: CharacterArg = "") -> dict[str, Any]:
        """The corporation this character is in: ticker, wallets, roles, what you can read.

        The right first call before any other eve_corp_* tool. It tells you
        whether this is a player corp at all (NPC school/militia corps have no
        hangars on ESI), which roles the character actually holds, and which
        follow-up tools those roles unlock. Wallet balances are included when
        the character is an Accountant; hangar and wallet names when they are
        a Director.

        Location-specific roles (HQ / base / other) are listed separately
        because they do not unlock ESI — only roles granted everywhere do.

        Returns: corporation, ticker, alliance, member_count, ceo, tax_pct,
        roles, wallets[], available_tools[].
        """
        corp = await ctx.resolve_corporation(character)
        public = corp.public
        ids = [i for i in (public.get("alliance_id"), public.get("ceo_id")) if i]
        names = await ctx.resolver.names(ids, character_id=corp.character_id)

        out: dict[str, Any] = {
            "character": corp.character_name,
            "corporation": corp.corporation_name,
            "ticker": corp.ticker or None,
            "corporation_id": corp.corporation_id,
            "corporation_kind": "npc" if corp.is_npc else "player",
            "member_count": public.get("member_count"),
            "ceo": names.get(public.get("ceo_id")),
            "alliance": names.get(public.get("alliance_id")),
            "tax_pct": round(float(public.get("tax_rate") or 0.0) * 100, 2),
            **_roles_for_display(corp),
        }

        if corp.is_npc:
            out["note"] = (
                "NPC corporations have no hangars, wallets or jobs on ESI. "
                "The other eve_corp_* tools will refuse this character."
            )
            out["available_tools"] = []
            return {
                k: v
                for k, v in out.items()
                if v not in (None, "", [], {}) or k in ("roles", "available_tools")
            }

        divisions = await _divisions(ctx, corp)
        if divisions["wallet"] or divisions["hangar"]:
            out["wallet_divisions"] = [
                {"division": n, "name": divisions["wallet"].get(n) or f"Division {n}"}
                for n in range(1, 8)
            ]
            out["hangar_divisions"] = [
                {"division": n, "name": divisions["hangar"].get(n) or f"Hangar {n}"}
                for n in range(1, 8)
            ]

        if _can(corp, "wallets", "wallets"):
            try:
                wallets = await ctx.esi.get(
                    f"/corporations/{corp.corporation_id}/wallets",
                    character_id=corp.character_id,
                )
                rows = [
                    {
                        "division": w.get("division"),
                        "name": _wallet_label(w.get("division"), divisions["wallet"]),
                        "balance": isk(w.get("balance")),
                        "balance_isk": w.get("balance"),
                    }
                    for w in (wallets.data or [])
                    if isinstance(w, dict)
                ]
                out["wallets"] = rows
                out["wallet_total"] = isk(sum(float(r.get("balance_isk") or 0) for r in rows))
                out["wallet_age"] = wallets.stale_note
            except EsiError as exc:
                out["wallets_note"] = str(exc)

        out["available_tools"] = _available_tools(corp)
        missing_scopes = [s for s in CORP_READ_SCOPES if s not in corp.token.scopes]
        if missing_scopes:
            out["next_step"] = (
                f"{corp.character_name}'s token is missing {len(missing_scopes)} "
                "corporation scopes. Add those permissions on the EVE developer "
                "application, then call eve_auth_login_url and re-authorize. "
                "The roles above already look sufficient."
                if corp.has_role(*_ESI_ROLES)
                else (
                    f"{corp.character_name}'s token is missing {len(missing_scopes)} "
                    "corporation scopes. Add those permissions on the EVE developer "
                    "application, then call eve_auth_login_url and re-authorize."
                )
            )
        elif out["available_tools"] == ["eve_corp_overview"]:
            out["next_step"] = (
                "This character has no corp roles that ESI honours. "
                "Someone with Director / Accountant / Factory_Manager / "
                "Station_Manager granted everywhere has to authorize instead."
            )
        return {
            k: v
            for k, v in out.items()
            if v not in (None, "", [], {}) or k in ("roles", "available_tools")
        }

    @mcp.tool()
    @handled
    async def eve_corp_assets_list(
        character: CharacterArg = "",
        location: Annotated[
            str,
            Field(
                description=(
                    "Case-insensitive substring of a station or structure name, e.g. "
                    "'Jita' or '1DQ'. Empty means every location."
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
        """Corporation assets grouped by station or structure, with an ISK estimate.

        Same shape as eve_assets_list, but this is the corp hangar — the place
        most industrial stock actually lives. Needs the Director role granted
        everywhere. Nested containers and ship holds roll up into the station
        that ultimately holds them.

        Valuation uses CCP's global average price per type, not a hub quote.
        Fine for "roughly how much is parked here"; use eve_market_price for
        anything the corp might actually sell.

        Large corps are truncated after 80 ESI pages (~80k stacks); the
        response says so when that happens.

        Returns: total_estimated_value, locations[] sorted by value.
        """
        corp = await _open(ctx, character, "assets", "assets", "corporation assets")
        concise = response_format == "concise"
        result = await ctx.esi.get_all_pages(
            f"/corporations/{corp.corporation_id}/assets",
            character_id=corp.character_id,
            max_pages=_ASSET_PAGES,
        )
        assets = [i for i in (result.data or []) if isinstance(i, dict)]
        if not assets:
            return {
                **_who(corp),
                "locations": [],
                "note": "The corporation hangar is empty (or this character cannot see it).",
            }

        divisions = await _divisions(ctx, corp)
        roots = root_locations(assets)
        prices = await ctx.resolver.reference_prices()
        type_names = await ctx.resolver.names({i["type_id"] for i in assets})
        place_names = await ctx.resolver.names(set(roots.values()), character_id=corp.character_id)

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
        out = {
            **_who(corp),
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
        if result.truncated:
            out["totals_caveat"] = (
                f"Stopped after {_ASSET_PAGES} pages; totals cover the first "
                f"{len(assets):,} stacks, not the whole hangar."
            )
        if divisions["hangar"]:
            out["hangar_names"] = divisions["hangar"]
        return out

    @mcp.tool()
    @handled
    async def eve_corp_assets_find(
        name: Annotated[
            str,
            Field(
                description=(
                    "Case-insensitive substring of the item type name, e.g. 'Orca' "
                    "or 'Tritanium'. Partial names are fine — this is a substring "
                    "match over the corporation's own items, not the global index."
                ),
                min_length=1,
            ),
        ],
        character: CharacterArg = "",
        limit: _MatchingStacksLimit = 20,
        response_format: DetailArg = "concise",
    ) -> dict[str, Any]:
        """Locate a specific item across every corp hangar, container and ship hold.

        The corp twin of eve_assets_find. Use it when the personal search came
        back empty and the thing is likely sitting in a shared hangar. Needs
        the Director role. Each match reports which hangar division it is in
        (CorpSAG1–7) when that is known.

        Returns: total_units, total_stacks, matches[].
        """
        corp = await _open(ctx, character, "assets", "assets", "corporation assets")
        concise = response_format == "concise"
        result = await ctx.esi.get_all_pages(
            f"/corporations/{corp.corporation_id}/assets",
            character_id=corp.character_id,
            max_pages=_ASSET_PAGES,
        )
        items = [i for i in (result.data or []) if isinstance(i, dict)]
        type_names = await ctx.resolver.names({i["type_id"] for i in items})
        needle = name.strip().lower()
        matches = [i for i in items if needle in type_names.get(i["type_id"], "").lower()]
        if not matches:
            return {
                **_who(corp),
                "query": name,
                "matches": [],
                "note": (
                    "Nothing matching in corporation assets. Check the spelling with "
                    "eve_universe_search, or look in personal hangars with eve_assets_find."
                ),
            }

        divisions = await _divisions(ctx, corp)
        roots = root_locations(items)
        by_id = {i["item_id"]: i for i in items}
        place_names = await ctx.resolver.names(
            {roots[i["item_id"]] for i in matches if i["item_id"] in roots},
            character_id=corp.character_id,
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
                    "hangar": _hangar_label(item.get("location_flag"), divisions["hangar"]),
                    "estimated_value": isk(unit_price(prices, item["type_id"]) * qty),
                    "inside": type_names.get(container["type_id"]) if container else None,
                    "slot": item.get("location_flag"),
                    "packaged": not item.get("is_singleton", False),
                    "item_id": item["item_id"],
                }
            )
        rows.sort(key=lambda r: -r["quantity"])
        visible, meta = page(rows, limit)
        out = {
            **_who(corp),
            "query": name,
            "total_units": sum(r["quantity"] for r in rows),
            "total_stacks": len(rows),
            "data_age": result.stale_note,
            **meta,
            "matches": project(
                visible,
                ("item", "quantity", "location", "hangar", "estimated_value"),
                concise,
            ),
        }
        if result.truncated:
            out["totals_caveat"] = (
                f"Search covered the first {len(items):,} stacks only "
                f"({_ASSET_PAGES}-page cap)."
            )
        return out

    @mcp.tool()
    @handled
    async def eve_corp_blueprints(
        character: CharacterArg = "",
        limit: _BlueprintsLimit = 25,
        response_format: DetailArg = "concise",
    ) -> dict[str, Any]:
        """Corporation blueprints with material/time efficiency and remaining runs.

        Same two kinds as the personal list: originals (BPO) last forever and
        report `runs_left` absent, copies (BPC) are consumed. Material
        efficiency (0-10) cuts input materials; time efficiency (0-20) cuts
        job duration. Needs the Director role.

        Returns: originals, copies, blueprints[] sorted originals-first then by
        material efficiency.
        """
        corp = await _open(ctx, character, "blueprints", "blueprints", "corporation blueprints")
        concise = response_format == "concise"
        result = await ctx.esi.get_all_pages(
            f"/corporations/{corp.corporation_id}/blueprints",
            character_id=corp.character_id,
        )
        blueprints = [b for b in (result.data or []) if isinstance(b, dict)]
        if not blueprints:
            return {**_who(corp), "blueprints": [], "note": "The corporation holds no blueprints."}

        divisions = await _divisions(ctx, corp)
        type_names = await ctx.resolver.names({b["type_id"] for b in blueprints})
        place_names = await ctx.resolver.names(
            {b["location_id"] for b in blueprints}, character_id=corp.character_id
        )
        rows = [
            {
                "blueprint": type_names.get(b["type_id"]),
                "kind": "copy" if b.get("runs", -1) != -1 else "original",
                "material_efficiency": b.get("material_efficiency"),
                "time_efficiency": b.get("time_efficiency"),
                "runs_left": None if b.get("runs", -1) == -1 else b["runs"],
                "location": place_names.get(b["location_id"], f"#{b['location_id']}"),
                "hangar": _hangar_label(b.get("location_flag"), divisions["hangar"]),
                "quantity": b.get("quantity"),
            }
            for b in blueprints
        ]
        rows.sort(key=lambda r: (r["kind"] == "copy", -(r["material_efficiency"] or 0)))
        visible, meta = page(rows, limit)
        return {
            **_who(corp),
            "originals": sum(1 for r in rows if r["kind"] == "original"),
            "copies": sum(1 for r in rows if r["kind"] == "copy"),
            "data_age": result.stale_note,
            **meta,
            "blueprints": project(
                visible,
                (
                    "blueprint",
                    "kind",
                    "material_efficiency",
                    "time_efficiency",
                    "runs_left",
                    "hangar",
                ),
                concise,
            ),
        }

    @mcp.tool()
    @handled
    async def eve_corp_wallet(
        character: CharacterArg = "",
        kind: Annotated[
            Literal["balances", "journal", "transactions", "both"],
            Field(
                description=(
                    "'balances' is the seven division balances — the usual first "
                    "question. 'journal' is every ISK movement in one division, "
                    "with reason codes. 'transactions' is market trades in that "
                    "division. 'both' returns journal and transactions together."
                )
            ),
        ] = "balances",
        division: DivisionArg = 1,
        ref_type: Annotated[
            str,
            Field(
                description=(
                    "Journal only: keep just one reason code, e.g. 'bounty_prizes', "
                    "'player_trading', 'market_escrow'. The unfiltered call lists "
                    "which codes actually occur, so run it once first."
                )
            ),
        ] = "",
        limit: _IndividualEntriesLimit = 15,
        response_format: DetailArg = "concise",
    ) -> dict[str, Any]:
        """Corporation ISK: the seven wallet divisions, plus journal and market trades.

        Needs Accountant or Junior_Accountant. Division 1 is the master wallet;
        the others are the named corp wallets. ESI keeps roughly the last 30
        days of journal and transactions, same as the personal wallet.

        `gross_margin` on transactions is everything sold minus everything
        bought in the window — not per-item profit.

        Returns: for balances, wallets[] and a total. For journal/transactions,
        the same shape as eve_wallet_history scoped to one division.
        """
        corp = await _open(ctx, character, "wallets", "wallets", "corporation wallets")
        concise = response_format == "concise"
        divisions = await _divisions(ctx, corp)
        out: dict[str, Any] = {
            **_who(corp),
            "division": division,
            "division_name": _wallet_label(division, divisions["wallet"]),
        }

        if kind == "balances":
            wallets = await ctx.esi.get(
                f"/corporations/{corp.corporation_id}/wallets",
                character_id=corp.character_id,
            )
            rows = [
                {
                    "division": w.get("division"),
                    "name": _wallet_label(w.get("division"), divisions["wallet"]),
                    "balance": isk(w.get("balance")),
                    "balance_isk": w.get("balance"),
                }
                for w in (wallets.data or [])
                if isinstance(w, dict)
            ]
            return {
                **_who(corp),
                "wallet_total": isk(sum(float(r.get("balance_isk") or 0) for r in rows)),
                "data_age": wallets.stale_note,
                "wallets": rows,
                "note": (
                    "Pass kind='journal' or kind='transactions' with a division "
                    "(1-7) to see movements. ESI retains about 30 days."
                ),
            }

        out["period"] = "last ~30 days (ESI retention limit)"
        if kind in ("journal", "both"):
            out["journal_section"] = await _journal(
                ctx, corp, division, ref_type, limit, concise
            )
        if kind in ("transactions", "both"):
            out["transactions_section"] = await _transactions(ctx, corp, division, limit, concise)
        if kind == "journal":
            section = out.pop("journal_section")
            return {**out, **section}
        if kind == "transactions":
            section = out.pop("transactions_section")
            return {**out, **section}
        return out

    @mcp.tool()
    @handled
    async def eve_corp_industry_jobs(
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
        """Corporation manufacturing, research, invention and reaction jobs.

        Needs the Factory_Manager role. Jobs whose end time has passed show
        `ready: true` — finished but still sitting in the output hangar until
        someone delivers them. Each row names the installer, which the personal
        list does not need.

        Returns: active_jobs, ready_to_deliver, jobs[] sorted by end time.
        """
        corp = await _open(ctx, character, "jobs", "jobs", "corporation industry jobs")
        concise = response_format == "concise"
        result = await ctx.esi.get_all_pages(
            f"/corporations/{corp.corporation_id}/industry/jobs",
            character_id=corp.character_id,
            params={"include_completed": include_completed},
        )
        jobs = [j for j in (result.data or []) if isinstance(j, dict)]
        if not jobs:
            return {
                **_who(corp),
                "jobs": [],
                "note": "No corporation industry jobs. Pass include_completed=true to see finished ones.",
            }

        type_ids = {j["blueprint_type_id"] for j in jobs} | {
            j["product_type_id"] for j in jobs if j.get("product_type_id")
        }
        people = {j["installer_id"] for j in jobs if j.get("installer_id")}
        names = await ctx.resolver.names(type_ids | people)
        places = await ctx.resolver.names(
            {j.get("station_id") or j.get("output_location_id") for j in jobs if j},
            character_id=corp.character_id,
        )
        now = datetime.now(timezone.utc)
        rows = []
        for job in jobs:
            end = _parse(job.get("end_date"))
            ready = end is not None and end <= now
            rows.append(
                {
                    "activity": _ACTIVITIES.get(job.get("activity_id"), f"#{job.get('activity_id')}"),
                    "product": names.get(job.get("product_type_id"))
                    or names.get(job["blueprint_type_id"]),
                    "runs": job.get("runs"),
                    "ends_in": "ready to deliver"
                    if ready
                    else (_human_delta(end - now) if end else "unknown"),
                    "location": places.get(job.get("station_id") or job.get("output_location_id")),
                    "installer": names.get(job.get("installer_id")),
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
            **_who(corp),
            "active_jobs": sum(1 for r in rows if not r["ready"]),
            "ready_to_deliver": sum(1 for r in rows if r["ready"]),
            "data_age": result.stale_note,
            **meta,
            "jobs": project(
                visible,
                ("activity", "product", "runs", "ends_in", "location", "installer"),
                concise,
            ),
        }

    @mcp.tool()
    @handled
    async def eve_corp_mining(
        character: CharacterArg = "",
        limit: _OreTypesLimit = 15,
        response_format: DetailArg = "concise",
    ) -> dict[str, Any]:
        """Corporation moon-mining ledger and extraction timers.

        Two different roles, two different pictures: Accountant unlocks the
        observer ledger (who mined how much of what, last ~30 days, valued at
        CCP average prices). Station_Manager unlocks extraction timers on the
        corp's refineries — when the next chunk arrives and when it decays.
        Director sees both. Missing one role drops that half rather than
        failing the whole call.

        Values are an order of magnitude, not a buyer quote — follow up with
        eve_market_price for a real number on a specific ore.

        Returns: total_estimated_value, ores[], extractions[], top_miners[].
        """
        corp = await ctx.resolve_corporation(character)
        ctx.require_player_corp(corp)
        ctx.require_scope(corp.token, _SCOPES["mining"], "the corporation mining ledger")
        can_ledger = corp.has_role(*_ROLES["mining_ledger"])
        can_extract = corp.has_role(*_ROLES["mining_extractions"])
        if not can_ledger and not can_extract:
            ctx.require_corp_role(
                corp,
                ("Accountant", "Station_Manager"),
                "corporation mining (ledger needs Accountant, extractions need Station_Manager)",
            )

        concise = response_format == "concise"
        out: dict[str, Any] = {**_who(corp), "period": "last ~30 days"}

        if can_extract:
            try:
                out["extractions"] = await _extractions(ctx, corp)
            except EsiError as exc:
                out["extractions_note"] = str(exc)
        else:
            out["extractions_note"] = (
                "Extraction timers need Station_Manager (or Director) granted everywhere."
            )

        if can_ledger:
            try:
                ledger = await _mining_ledger(ctx, corp, limit, concise)
                out.update(ledger)
            except EsiError as exc:
                out["ledger_note"] = str(exc)
        else:
            out["ledger_note"] = (
                "The observer ledger needs Accountant (or Director) granted everywhere."
            )
        return out

    @mcp.tool()
    @handled
    async def eve_corp_orders(
        character: CharacterArg = "",
        limit: _OrdersLimit = 25,
        response_format: DetailArg = "concise",
    ) -> dict[str, Any]:
        """The corporation's open buy and sell orders, with fill progress and expiry.

        Needs Accountant or Trader. `buy_escrow_locked` is ISK tied up in buy
        orders across every wallet division; `wallet_division` on each row
        says which corp wallet posted it. Orders that lapse are cancelled and
        relisting costs another broker fee.

        Returns: open_orders, sell_side_value, buy_escrow_locked, orders[].
        """
        corp = await _open(ctx, character, "orders", "orders", "corporation market orders")
        concise = response_format == "concise"
        result = await ctx.esi.get_all_pages(
            f"/corporations/{corp.corporation_id}/orders",
            character_id=corp.character_id,
        )
        orders = [o for o in (result.data or []) if isinstance(o, dict)]
        if not orders:
            return {**_who(corp), "orders": [], "note": "No open corporation market orders."}

        divisions = await _divisions(ctx, corp)
        names = await ctx.resolver.names({o["type_id"] for o in orders})
        places = await ctx.resolver.names(
            {o["location_id"] for o in orders}, character_id=corp.character_id
        )
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
                    "wallet": _wallet_label(order.get("wallet_division"), divisions["wallet"]),
                    "range": order.get("range") if is_buy else None,
                    "escrow": isk(order.get("escrow")) if is_buy else None,
                }
            )
        rows.sort(key=lambda r: (r["side"], r["item"] or ""))
        visible, meta = page(rows, limit)
        return {
            **_who(corp),
            "open_orders": len(rows),
            "sell_side_value": isk(sell_value),
            "buy_escrow_locked": isk(buy_escrow),
            "data_age": result.stale_note,
            **meta,
            "orders": project(
                visible,
                (
                    "side",
                    "item",
                    "price",
                    "remaining",
                    "filled_pct",
                    "location",
                    "expires_in",
                    "wallet",
                ),
                concise,
            ),
        }

    @mcp.tool()
    @handled
    async def eve_corp_contracts(
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
        """Contracts issued by or assigned to the corporation, newest first.

        Needs the corporation-contracts scope; any member can read them. Courier
        contracts carry collateral and a reward — a courier nearing `expires`
        with undelivered cargo means the collateral is about to be lost.

        Returns: total, outstanding, contracts[]. 'detailed' adds volume,
        issue date and raw contract_id (needed by eve_ui_open_window).
        """
        corp = await _open(ctx, character, "contracts", (), "corporation contracts")
        concise = response_format == "concise"
        result = await ctx.esi.get_all_pages(
            f"/corporations/{corp.corporation_id}/contracts",
            character_id=corp.character_id,
        )
        contracts = [c for c in (result.data or []) if isinstance(c, dict)]
        if outstanding_only:
            contracts = [c for c in contracts if c.get("status") == "outstanding"]
        if not contracts:
            return {
                **_who(corp),
                "contracts": [],
                "note": (
                    "No outstanding corporation contracts. Pass outstanding_only=false "
                    "to include finished and expired ones."
                    if outstanding_only
                    else "This corporation has no contracts at all, in any state."
                ),
            }

        party_ids, place_ids = set(), set()
        for contract in contracts:
            party_ids.update(
                i
                for i in (
                    contract.get("issuer_id"),
                    contract.get("assignee_id"),
                    contract.get("issuer_corporation_id"),
                    contract.get("acceptor_id"),
                )
                if i
            )
            place_ids.update(
                i
                for i in (contract.get("start_location_id"), contract.get("end_location_id"))
                if i
            )
        names = await ctx.resolver.names(party_ids | place_ids, character_id=corp.character_id)
        rows = [
            {
                "type": c.get("type"),
                "status": c.get("status"),
                "title": c.get("title"),
                "issuer": names.get(c.get("issuer_id"))
                or names.get(c.get("issuer_corporation_id")),
                "assignee": names.get(c.get("assignee_id")),
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
            **_who(corp),
            "total": len(rows),
            "outstanding": sum(1 for r in rows if r["status"] == "outstanding"),
            "data_age": result.stale_note,
            **meta,
            "contracts": project(
                visible,
                (
                    "type",
                    "status",
                    "title",
                    "issuer",
                    "price",
                    "reward",
                    "collateral",
                    "from",
                    "to",
                    "expires",
                ),
                concise,
            ),
        }

    @mcp.tool()
    @handled
    async def eve_corp_killmails(
        character: CharacterArg = "",
        limit: _KillmailsLimit = 8,
        response_format: DetailArg = "concise",
    ) -> dict[str, Any]:
        """Recent kills and losses involving this corporation.

        Needs the Director role. `role` is 'loss' when the victim corporation
        is this one — including structures and NPCs that have no character.
        `hull_value` is the ship hull only; fitted modules and cargo are not
        counted, so a real loss is usually more expensive. Each row carries a
        zkillboard link for the full breakdown.

        Returns: kills, losses, killmails[] newest first.
        """
        corp = await _open(ctx, character, "killmails", "killmails", "corporation killmails")
        concise = response_format == "concise"
        result = await ctx.esi.get(
            f"/corporations/{corp.corporation_id}/killmails/recent",
            character_id=corp.character_id,
        )
        available = [k for k in (result.data or []) if isinstance(k, dict)]
        refs = available[:limit]
        if not refs:
            return {**_who(corp), "killmails": [], "note": "Nothing recent."}

        details = await asyncio.gather(
            *(ctx.esi.get(f"/killmails/{k['killmail_id']}/{k['killmail_hash']}") for k in refs),
            return_exceptions=True,
        )
        kills, failed = [], []
        for ref, detail in zip(refs, details):
            if isinstance(detail, BaseException):
                failed.append(ref.get("killmail_id"))
                log.warning("killmail %s could not be fetched: %s", ref.get("killmail_id"), detail)
            else:
                kills.append(detail.data)

        ids: set[int] = set()
        for kill in kills:
            victim = kill.get("victim", {})
            ids.update(
                i
                for i in (
                    victim.get("character_id"),
                    victim.get("corporation_id"),
                    victim.get("ship_type_id"),
                    kill.get("solar_system_id"),
                )
                if i
            )
        names = await ctx.resolver.names(ids)
        prices = await ctx.resolver.reference_prices()
        rows = []
        for kill in kills:
            victim = kill.get("victim", {})
            was_victim = victim.get("corporation_id") == corp.corporation_id
            hull_price = unit_price(prices, victim.get("ship_type_id"))
            rows.append(
                {
                    "role": "loss" if was_victim else "kill",
                    "time": kill.get("killmail_time"),
                    "system": names.get(kill.get("solar_system_id")),
                    "victim": names.get(victim.get("character_id"))
                    or names.get(victim.get("corporation_id")),
                    "ship_lost": names.get(victim.get("ship_type_id")),
                    "hull_value": isk(hull_price),
                    "attackers": len(kill.get("attackers", [])),
                    "zkill": f"https://zkillboard.com/kill/{kill.get('killmail_id')}/",
                }
            )
        rows.sort(key=lambda r: r.get("time") or "", reverse=True)
        visible, meta = page(rows, limit)
        if len(available) > limit:
            meta = {
                "returned": len(visible),
                "total_available": len(available),
                "truncated": True,
                "how_to_see_more": f"Raise `limit` (currently {limit}).",
            }
        out = {
            **_who(corp),
            **meta,
            "kills": sum(1 for r in rows if r["role"] == "kill"),
            "losses": sum(1 for r in rows if r["role"] == "loss"),
            "hull_value_caveat": "Hull only — fitted modules and cargo are not included.",
            "killmails": project(
                visible, ("role", "time", "system", "victim", "ship_lost"), concise
            ),
        }
        if failed:
            out["unavailable"] = len(failed)
            out["unavailable_note"] = (
                f"{len(failed)} of {len(refs)} killmails could not be fetched from ESI, "
                "so kills/losses below undercount by that many. Try again shortly."
            )
        return out

    @mcp.tool()
    @handled
    async def eve_corp_structures(
        character: CharacterArg = "",
        limit: _StructuresLimit = 15,
        response_format: DetailArg = "concise",
    ) -> dict[str, Any]:
        """Upwell structures this corporation owns: fuel, state and services.

        Needs the Station_Manager role. `fuel_expires_in` is the one to watch —
        an unfuelled structure goes into low power and then abandoned. `state`
        tells you whether it is vulnerable, reinforcing or anchoring.
        Structure names need docking access; unnamed ones come back as
        'Unknown #id'.

        Returns: structures[] with type, system, state and fuel. 'detailed'
        adds services and reinforce hour.
        """
        corp = await _open(ctx, character, "structures", "structures", "corporation structures")
        concise = response_format == "concise"
        result = await ctx.esi.get_all_pages(
            f"/corporations/{corp.corporation_id}/structures",
            character_id=corp.character_id,
        )
        structures = [s for s in (result.data or []) if isinstance(s, dict)]
        if not structures:
            return {**_who(corp), "structures": [], "note": "This corporation owns no Upwell structures."}

        type_ids = {s["type_id"] for s in structures if s.get("type_id")}
        system_ids = {s["system_id"] for s in structures if s.get("system_id")}
        structure_ids = {s["structure_id"] for s in structures if s.get("structure_id")}
        names = await ctx.resolver.names(
            type_ids | system_ids | structure_ids, character_id=corp.character_id
        )
        now = datetime.now(timezone.utc)
        rows = []
        for structure in structures:
            fuel = _parse(structure.get("fuel_expires"))
            services = [
                f"{svc.get('name')} ({svc.get('state')})"
                for svc in (structure.get("services") or [])
                if isinstance(svc, dict)
            ]
            rows.append(
                {
                    "structure": names.get(structure.get("structure_id")),
                    "type": names.get(structure.get("type_id")),
                    "system": names.get(structure.get("system_id")),
                    "state": structure.get("state"),
                    "fuel_expires_in": (
                        "UNFUELLED"
                        if fuel is not None and fuel <= now
                        else (_human_delta(fuel - now) if fuel else "unknown")
                    ),
                    "fuel_expires": structure.get("fuel_expires"),
                    "reinforce_hour": structure.get("reinforce_hour"),
                    "services": services or None,
                    "structure_id": structure.get("structure_id"),
                }
            )
        rows.sort(key=lambda r: (r.get("fuel_expires") or "", r.get("structure") or ""))
        visible, meta = page(rows, limit)
        return {
            **_who(corp),
            "structure_count": len(rows),
            "unfuelled": sum(1 for r in rows if r["fuel_expires_in"] == "UNFUELLED"),
            "data_age": result.stale_note,
            **meta,
            "structures": project(
                visible, ("structure", "type", "system", "state", "fuel_expires_in"), concise
            ),
        }

    @mcp.tool()
    @handled
    async def eve_corp_members(
        character: CharacterArg = "",
        limit: _MembersLimit = 25,
        response_format: DetailArg = "concise",
    ) -> dict[str, Any]:
        """Current corporation members, alphabetically.

        Needs the membership scope; any member can read the roster. 'detailed'
        adds each member's ESI roles when this character is a Director — that
        is the same list eve_corp_overview shows for one character, for
        everyone. Last-login tracking is a different scope and is not
        requested here.

        Returns: member_count, members[] with name. 'detailed' may add roles.
        """
        corp = await _open(ctx, character, "members", (), "corporation membership")
        concise = response_format == "concise"
        result = await ctx.esi.get_all_pages(
            f"/corporations/{corp.corporation_id}/members",
            character_id=corp.character_id,
        )
        member_ids = [int(i) for i in (result.data or []) if i]
        if not member_ids:
            return {**_who(corp), "members": [], "note": "ESI returned an empty roster."}

        names = await ctx.resolver.names(member_ids)
        role_map: dict[int, list[str]] = {}
        if not concise and corp.has_role("Director"):
            try:
                roles_result = await ctx.esi.get(
                    f"/corporations/{corp.corporation_id}/roles",
                    character_id=corp.character_id,
                )
                for row in roles_result.data or []:
                    if isinstance(row, dict) and row.get("character_id"):
                        role_map[int(row["character_id"])] = list(row.get("roles") or [])
            except EsiError as exc:
                log.info("could not read corporation roles roster: %s", exc)

        rows = [
            {
                "name": names.get(mid, f"#{mid}"),
                "character_id": mid,
                "roles": role_map.get(mid) or None,
            }
            for mid in member_ids
        ]
        rows.sort(key=lambda r: (r["name"] or "").lower())
        visible, meta = page(rows, limit)
        return {
            **_who(corp),
            "member_count": len(rows),
            "data_age": result.stale_note,
            **meta,
            "members": project(visible, ("name",), concise),
        }


# --------------------------------------------------------------------- helpers


async def _open(
    ctx: AppContext,
    character: str,
    scope_key: str,
    role_key: str | tuple[str, ...],
    what: str,
) -> Corporation:
    """Resolve the corp and fail closed on NPC / missing scope / missing role."""
    corp = await ctx.resolve_corporation(character)
    ctx.require_player_corp(corp)
    ctx.require_scope(corp.token, _SCOPES[scope_key], what)
    if isinstance(role_key, str):
        ctx.require_corp_role(corp, _ROLES[role_key], what)
    elif role_key:
        ctx.require_corp_role(corp, role_key, what)
    return corp


def _roles_for_display(corp: Corporation) -> dict[str, Any]:
    """Keep the overview small: only ESI-relevant roles, collapsed for Directors."""
    everywhere = set(corp.roles)
    if "Director" in everywhere:
        return {
            "roles": ["Director"],
            "role_note": (
                "Director unlocks every eve_corp_* endpoint. Only roles granted "
                "everywhere count; HQ/base/other grants do not."
            ),
        }
    esi = sorted(everywhere & _ESI_ROLES)
    loc = {
        "roles_at_hq": sorted((corp.roles_at_hq & _ESI_ROLES) - everywhere),
        "roles_at_base": sorted((corp.roles_at_base & _ESI_ROLES) - everywhere),
        "roles_at_other": sorted((corp.roles_at_other & _ESI_ROLES) - everywhere),
    }
    out: dict[str, Any] = {
        "roles": esi,
        "role_note": (
            "Only roles granted everywhere unlock ESI. HQ/base/other grants do not."
        ),
    }
    if not esi:
        out["roles_note"] = (
            "No ESI-relevant roles granted everywhere "
            f"(Director, Accountant, Factory_Manager, Station_Manager, Trader). "
            f"Raw role count: {len(everywhere)}."
        )
    for key, values in loc.items():
        if values:
            out[key] = values
    return out


def _who(corp: Corporation) -> dict[str, Any]:
    return {
        "character": corp.character_name,
        "corporation": corp.corporation_name,
        "ticker": corp.ticker or None,
    }


def _can(corp: Corporation, scope_key: str, role_key: str) -> bool:
    return _SCOPES[scope_key] in corp.token.scopes and corp.has_role(*_ROLES[role_key])


def _available_tools(corp: Corporation) -> list[str]:
    catalog = (
        ("eve_corp_assets_list", "assets", "assets"),
        ("eve_corp_assets_find", "assets", "assets"),
        ("eve_corp_blueprints", "blueprints", "blueprints"),
        ("eve_corp_wallet", "wallets", "wallets"),
        ("eve_corp_industry_jobs", "jobs", "jobs"),
        ("eve_corp_orders", "orders", "orders"),
        ("eve_corp_contracts", "contracts", ""),
        ("eve_corp_killmails", "killmails", "killmails"),
        ("eve_corp_structures", "structures", "structures"),
        ("eve_corp_members", "members", ""),
    )
    out = ["eve_corp_overview"]
    for name, scope_key, role_key in catalog:
        if _SCOPES[scope_key] not in corp.token.scopes:
            continue
        if role_key and not corp.has_role(*_ROLES[role_key]):
            continue
        out.append(name)
    if _SCOPES["mining"] in corp.token.scopes and (
        corp.has_role(*_ROLES["mining_ledger"]) or corp.has_role(*_ROLES["mining_extractions"])
    ):
        out.append("eve_corp_mining")
    return out


async def _divisions(ctx: AppContext, corp: Corporation) -> dict[str, dict[int, str]]:
    empty: dict[str, dict[int, str]] = {"wallet": {}, "hangar": {}}
    if not _can(corp, "divisions", "divisions"):
        return empty
    try:
        result = await ctx.esi.get(
            f"/corporations/{corp.corporation_id}/divisions",
            character_id=corp.character_id,
        )
    except EsiError as exc:
        log.info("could not read corporation divisions: %s", exc)
        return empty
    data = result.data or {}
    out: dict[str, dict[int, str]] = {"wallet": {}, "hangar": {}}
    for kind in ("wallet", "hangar"):
        for row in data.get(kind) or []:
            if not isinstance(row, dict) or not row.get("division"):
                continue
            name = (row.get("name") or "").strip()
            if name:
                out[kind][int(row["division"])] = name
    return out


def _wallet_label(division: int | None, names: dict[int, str]) -> str:
    if not division:
        return "unknown"
    return names.get(int(division)) or f"Division {division}"


def _hangar_label(flag: str | None, names: dict[int, str]) -> str | None:
    if not flag:
        return None
    number = _HANGAR_FLAGS.get(flag)
    if number is not None:
        return names.get(number) or f"Hangar {number}"
    if flag == "CorpDeliveries":
        return "Corp Deliveries"
    if flag == "Impounded":
        return "Impounded"
    return flag


async def _journal(
    ctx: AppContext,
    corp: Corporation,
    division: int,
    ref_type: str,
    limit: int,
    concise: bool,
) -> dict[str, Any]:
    result = await ctx.esi.get_all_pages(
        f"/corporations/{corp.corporation_id}/wallets/{division}/journal",
        character_id=corp.character_id,
        max_pages=_JOURNAL_PAGES,
    )
    entries = [e for e in (result.data or []) if isinstance(e, dict)]
    available_codes = sorted({e.get("ref_type", "unknown") for e in entries})
    if ref_type:
        entries = [e for e in entries if e.get("ref_type") == ref_type]
        if not entries:
            return {
                "journal": [],
                "error": (
                    f"No journal entries with ref_type {ref_type!r} in division "
                    f"{division}. Codes actually present: {available_codes}"
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
            f"Hit the {_JOURNAL_PAGES}-page read cap: the totals cover the newest "
            f"{len(entries):,} entries, not the full window."
        )
    return out


async def _transactions(
    ctx: AppContext, corp: Corporation, division: int, limit: int, concise: bool
) -> dict[str, Any]:
    result = await ctx.esi.get_cursor_pages(
        f"/corporations/{corp.corporation_id}/wallets/{division}/transactions",
        character_id=corp.character_id,
        cursor_param="from_id",
        cursor_key="transaction_id",
        batch_size=_TRANSACTION_BATCH,
        max_pages=_TRANSACTION_PAGES,
    )
    entries = [t for t in (result.data or []) if isinstance(t, dict)]
    if not entries:
        return {"transactions": [], "note": "No market trades in this division in the retained window."}

    type_names = await ctx.resolver.names({t["type_id"] for t in entries})
    place_names = await ctx.resolver.names(
        {t["location_id"] for t in entries}, character_id=corp.character_id
    )
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


async def _extractions(ctx: AppContext, corp: Corporation) -> list[dict[str, Any]]:
    result = await ctx.esi.get(
        f"/corporation/{corp.corporation_id}/mining/extractions",
        character_id=corp.character_id,
    )
    rows = [e for e in (result.data or []) if isinstance(e, dict)]
    if not rows:
        return []
    ids = {e.get("structure_id") for e in rows} | {e.get("moon_id") for e in rows}
    names = await ctx.resolver.names({i for i in ids if i}, character_id=corp.character_id)
    now = datetime.now(timezone.utc)
    out = []
    for extraction in rows:
        arrival = _parse(extraction.get("chunk_arrival_time"))
        decay = _parse(extraction.get("natural_decay_time"))
        out.append(
            {
                "structure": names.get(extraction.get("structure_id")),
                "moon": names.get(extraction.get("moon_id")),
                "chunk_arrives_in": (
                    "arrived" if arrival is not None and arrival <= now
                    else (_human_delta(arrival - now) if arrival else "unknown")
                ),
                "decays_in": (
                    "decayed" if decay is not None and decay <= now
                    else (_human_delta(decay - now) if decay else "unknown")
                ),
                "chunk_arrival_time": extraction.get("chunk_arrival_time"),
                "natural_decay_time": extraction.get("natural_decay_time"),
            }
        )
    out.sort(key=lambda r: r.get("chunk_arrival_time") or "")
    return out


async def _mining_ledger(
    ctx: AppContext, corp: Corporation, limit: int, concise: bool
) -> dict[str, Any]:
    observers_result = await ctx.esi.get_all_pages(
        f"/corporation/{corp.corporation_id}/mining/observers",
        character_id=corp.character_id,
    )
    observers = [o for o in (observers_result.data or []) if isinstance(o, dict)]
    if not observers:
        return {
            "ores": [],
            "note": "No mining observers with recorded events (idle refineries are hidden).",
            "data_age": observers_result.stale_note,
        }

    capped = observers[:_MINING_OBSERVER_CAP]
    ledgers = await asyncio.gather(
        *(
            ctx.esi.get_all_pages(
                f"/corporation/{corp.corporation_id}/mining/observers/{obs['observer_id']}",
                character_id=corp.character_id,
                max_pages=_MINING_OBSERVER_PAGES,
            )
            for obs in capped
            if obs.get("observer_id")
        ),
        return_exceptions=True,
    )

    totals: dict[int, int] = defaultdict(int)
    by_miner: dict[int, int] = defaultdict(int)
    by_observer: dict[int, int] = defaultdict(int)
    oldest = observers_result.age_seconds
    failed = 0
    truncated = observers_result.truncated or len(observers) > _MINING_OBSERVER_CAP
    for obs, ledger in zip(capped, ledgers):
        if isinstance(ledger, BaseException):
            failed += 1
            log.warning("mining observer %s failed: %s", obs.get("observer_id"), ledger)
            continue
        oldest = max(oldest, ledger.age_seconds)
        truncated = truncated or ledger.truncated
        observer_id = obs.get("observer_id")
        for entry in ledger.data or []:
            if not isinstance(entry, dict):
                continue
            qty = entry.get("quantity", 0)
            totals[entry["type_id"]] += qty
            if entry.get("character_id"):
                by_miner[entry["character_id"]] += qty
            if observer_id:
                by_observer[observer_id] += qty

    names = await ctx.resolver.names(
        set(totals) | set(by_miner) | set(by_observer), character_id=corp.character_id
    )
    prices = await ctx.resolver.reference_prices()
    rows = []
    grand_total = 0.0
    for type_id, qty in sorted(totals.items(), key=lambda kv: -kv[1]):
        value = unit_price(prices, type_id) * qty
        grand_total += value
        rows.append(
            {
                "ore": names.get(type_id, f"#{type_id}"),
                "units": qty,
                "estimated_value": isk(value),
            }
        )
    visible, meta = page(rows, limit)
    out: dict[str, Any] = {
        "total_estimated_value": isk(grand_total),
        "observer_count": len(observers),
        "top_miners": [
            {"miner": names.get(cid, f"#{cid}"), "units": q}
            for cid, q in sorted(by_miner.items(), key=lambda kv: -kv[1])[:5]
        ],
        "top_observers": [
            {"observer": names.get(oid, f"#{oid}"), "units": q}
            for oid, q in sorted(by_observer.items(), key=lambda kv: -kv[1])[:5]
        ],
        "valuation_basis": "CCP global average price per type, not a hub quote",
        "data_age": f"{int(oldest)}s old" if oldest < 60 else observers_result.stale_note,
        **meta,
        "ores": visible,
    }
    if failed:
        out["unavailable_observers"] = failed
    if truncated:
        out["totals_caveat"] = (
            f"Ledger walk was capped ({_MINING_OBSERVER_CAP} observers, "
            f"{_MINING_OBSERVER_PAGES} pages each); totals may be short."
        )
    return out
