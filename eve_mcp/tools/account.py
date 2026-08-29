"""Session state and the at-a-glance character overview."""
from __future__ import annotations

import asyncio
from typing import Any

from mcp.server.mcpserver import MCPServer

from ..config import CORP_READ_SCOPES, WRITE_CAPABILITIES
from ..context import AppContext
from ..esi import EsiError
from ..schema import CharacterArg
from ._common import handled, isk

_ALL_WRITE_SCOPES = {s for cap in WRITE_CAPABILITIES.values() for s in cap.scopes}


def register(mcp: MCPServer, ctx: AppContext) -> None:
    @mcp.tool()
    @handled
    async def eve_server_status() -> dict[str, Any]:
        """Tranquility server status: player count, build version, uptime, VIP mode.

        Also the cheapest way to confirm this server can reach ESI at all. EVE
        has a daily downtime around 11:00 UTC; a low player count right after it
        is normal, not a bug.

        Returns: server_version, players, vip, start_time, data_age.
        """
        result = await ctx.esi.get("/status")
        return {**result.data, "data_age": result.stale_note}

    @mcp.tool()
    @handled
    async def eve_auth_status() -> dict[str, Any]:
        """Who is authorized here, and which in-game changes this server permits.

        Call this before anything else when you do not know the setup, and
        always before promising the user an in-game change. It answers three
        questions at once: which characters you can query, which mutating
        capabilities the operator enabled, and how much of the hourly write
        budget is left.

        Returns: characters[], default_character, write_mode, enabled_capabilities,
        disabled_capabilities, capability_reference, corporation_scopes_requested,
        budgets, audit_log.
        """
        tokens = ctx.sso.store.all()
        policy = ctx.guard.status()
        policy["outward_facing_capabilities"] = sorted(
            name for name, cap in WRITE_CAPABILITIES.items() if cap.outward_facing
        )
        policy["write_mode_meaning"] = {
            "off": "no writes at all; write scopes are never even requested at login",
            "confirm": "each write returns a preview plus a one-time token; a second call executes it",
            "on": "writes execute immediately (still budgeted and audited)",
        }[ctx.settings.write_mode]

        if not tokens:
            return {
                "characters": [],
                "next_step": (
                    "Nobody is authorized. Call eve_auth_login_url and give the user "
                    "the link to open in a browser."
                ),
                "corporation_scopes_requested": ctx.settings.corp_scopes,
                **policy,
            }
        return {
            "characters": [
                {
                    "name": t.character_name,
                    "character_id": t.character_id,
                    "scope_count": len(t.scopes),
                    # Derive from the capability model, not a substring match: the
                    # old filter silently missed esi-mail.organize_mail.v1 and
                    # esi-calendar.respond_calendar_events.v1.
                    "write_scopes": sorted(set(t.scopes) & _ALL_WRITE_SCOPES),
                    "corporation_scopes": sorted(set(t.scopes) & set(CORP_READ_SCOPES)),
                }
                for t in tokens
            ],
            "default_character": tokens[0].character_name if len(tokens) == 1 else None,
            "corporation_scopes_requested": ctx.settings.corp_scopes,
            **policy,
        }

    @mcp.tool()
    @handled
    async def eve_auth_login_url() -> dict[str, Any]:
        """Generate an EVE SSO link the user must open to authorize a character.

        You cannot complete this yourself — hand the URL to the user. They log
        in with their EVE account, approve the scope list, and the server stores
        the resulting token. One-time per character; several characters can be
        authorized by repeating it.

        Returns: login_url, scope_count, write_capabilities_requested,
        corporation_scopes_requested, instructions.
        """
        url, state = ctx.sso.build_login()
        scopes = ctx.settings.requested_scopes()
        return {
            "login_url": url,
            "state": state,
            "scope_count": len(scopes),
            "write_capabilities_requested": sorted(ctx.settings.write_allow)
            if ctx.settings.write_mode != "off"
            else [],
            "corporation_scopes_requested": ctx.settings.corp_scopes,
            "instructions": (
                "Open login_url in a browser, pick the character, approve. "
                "The link is valid for 15 minutes and works once."
            ),
        }

    @mcp.tool()
    @handled
    async def eve_auth_logout(character: CharacterArg) -> dict[str, Any]:
        """Revoke this server's access to one character and delete its stored token.

        Irreversible in the sense that re-authorizing needs another browser
        login, but it destroys nothing in-game.

        Returns: removed, character_id.
        """
        token = ctx.resolve_character(character)
        await ctx.sso.revoke(token.character_id)
        return {"removed": token.character_name, "character_id": token.character_id}

    @mcp.tool()
    @handled
    async def eve_character_overview(character: CharacterArg = "") -> dict[str, Any]:
        """Everything you would glance at on logging in: corp, ISK, location, ship, training.

        The best first call for almost any question about how the character is
        doing — it fuses seven ESI endpoints into roughly 200 tokens and tells
        you what to drill into next. It already includes the wallet balance and
        what is training, so there is no need to ask for those separately.

        Partial results are normal: if one underlying endpoint fails, the rest
        still come back rather than the whole call erroring.

        Returns: name, corporation, alliance, security_status, wallet_isk,
        online, solar_system, docked_at, ship_type, training_now, queue_ends,
        remaps_available. `location_age` is separate because location is cached
        for only 5 seconds while other fields are cached far longer.
        """
        token = ctx.resolve_character(character)
        cid = token.character_id

        async def maybe(coro):
            try:
                return await coro
            except (EsiError, Exception):  # noqa: BLE001 - partial overview beats none
                return None

        public, wallet, location, ship, online, queue, attributes = await asyncio.gather(
            maybe(ctx.esi.get(f"/characters/{cid}")),
            maybe(ctx.esi.get(f"/characters/{cid}/wallet", character_id=cid)),
            maybe(ctx.esi.get(f"/characters/{cid}/location", character_id=cid)),
            maybe(ctx.esi.get(f"/characters/{cid}/ship", character_id=cid)),
            maybe(ctx.esi.get(f"/characters/{cid}/online", character_id=cid)),
            maybe(ctx.esi.get(f"/characters/{cid}/skillqueue", character_id=cid)),
            maybe(ctx.esi.get(f"/characters/{cid}/attributes", character_id=cid)),
        )

        out: dict[str, Any] = {"character_id": cid, "name": token.character_name}

        if public is not None:
            info = public.data
            ids = [i for i in (info.get("corporation_id"), info.get("alliance_id")) if i]
            names = await ctx.resolver.names(ids)
            out["corporation"] = names.get(info.get("corporation_id"))
            if info.get("alliance_id"):
                out["alliance"] = names.get(info["alliance_id"])
            out["security_status"] = round(info.get("security_status", 0.0), 2)
            out["birthday"] = info.get("birthday")

        if wallet is not None:
            out["wallet_isk"] = wallet.data
            out["wallet"] = isk(wallet.data)

        if online is not None:
            out["online"] = online.data.get("online")
            out["last_login"] = online.data.get("last_login")

        if location is not None:
            loc = location.data
            place_ids = [
                i
                for i in (loc.get("solar_system_id"), loc.get("station_id"), loc.get("structure_id"))
                if i
            ]
            names = await ctx.resolver.names(place_ids, character_id=cid)
            out["solar_system"] = names.get(loc.get("solar_system_id"))
            docked_at = loc.get("station_id") or loc.get("structure_id")
            out["docked_at"] = names.get(docked_at) if docked_at else "in space"
            out["location_age"] = location.stale_note

        if ship is not None:
            out["ship_type"] = await ctx.resolver.name(ship.data.get("ship_type_id", 0))
            ship_name = ship.data.get("ship_name")
            if ship_name and ship_name != out["ship_type"]:
                out["ship_name"] = ship_name

        if queue is not None:
            entries = [e for e in queue.data or [] if e.get("finish_date")]
            if entries:
                first = entries[0]
                skill_name = await ctx.resolver.name(first["skill_id"])
                out["training_now"] = f"{skill_name} {_roman(first.get('finished_level'))}"
                out["training_finishes"] = first.get("finish_date")
                out["queue_length"] = len(entries)
                out["queue_ends"] = entries[-1].get("finish_date")
            else:
                out["training_now"] = None
                out["warning"] = "Skill queue is empty — training time is being wasted."

        if attributes is not None:
            out["remaps_available"] = attributes.data.get("bonus_remaps")

        return {k: v for k, v in out.items() if v not in (None, "", [], {})}


def _roman(level: int | None) -> str:
    return {1: "I", 2: "II", 3: "III", 4: "IV", 5: "V"}.get(level or 0, str(level))
