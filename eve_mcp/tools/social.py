"""Mail, notifications, killmails and saved fittings."""
from __future__ import annotations

import asyncio
import logging
import re
from typing import Annotated, Any

from mcp.server.mcpserver import MCPServer
from pydantic import Field

from ..context import AppContext
from ..schema import CharacterArg, DetailArg, limit_arg
from ._common import handled, isk, page, project, unit_price

log = logging.getLogger("eve_mcp.tools.social")

_MailHeadersLimit = limit_arg("mail headers", 50)
_NotificationsLimit = limit_arg("notifications", 100)
_KillmailsLimit = limit_arg("killmails", 50)
_FittingsLimit = limit_arg("fittings", 100)


def register(mcp: MCPServer, ctx: AppContext) -> None:
    @mcp.tool()
    @handled
    async def eve_mail_list(
        character: CharacterArg = "",
        unread_only: Annotated[
            bool,
            Field(description="Only list mail that has not been read yet."),
        ] = False,
        limit: _MailHeadersLimit = 15,
        response_format: DetailArg = "concise",
    ) -> dict[str, Any]:
        """Mail headers only — sender, subject, date, read state. Bodies are not included.

        To read the actual text of one mail, follow up with eve_mail_read using
        the `mail_id` from here. That two-step split is deliberate: mail bodies
        are long and you rarely need all of them.

        Not to be confused with eve_mail_mark, which changes the read flag
        rather than reading anything.

        Returns: unread count, mails[] with mail_id for follow-up.
        """
        token = ctx.resolve_character(character)
        cid = token.character_id
        ctx.require_scope(token, "esi-mail.read_mail.v1", "mail")
        concise = response_format == "concise"

        result = await ctx.esi.get(f"/characters/{cid}/mail", character_id=cid)
        mails = [m for m in (result.data or []) if isinstance(m, dict)]
        # Count before filtering, or `unread` just restates len(mails).
        unread_count = sum(1 for m in mails if not m.get("is_read"))
        if unread_only:
            mails = [m for m in mails if not m.get("is_read")]
        if not mails:
            return {"character": token.character_name, "mails": [], "note": "Nothing to show."}

        senders = await ctx.resolver.names({m["from"] for m in mails if m.get("from")})
        rows = [
            {
                "mail_id": m.get("mail_id"),
                "from": senders.get(m.get("from"), f"#{m.get('from')}"),
                "subject": m.get("subject"),
                "timestamp": m.get("timestamp"),
                "read": m.get("is_read", False),
                "labels": m.get("labels"),
            }
            for m in sorted(mails, key=lambda m: m.get("timestamp", ""), reverse=True)
        ]
        visible, meta = page(rows, limit)
        return {
            "character": token.character_name,
            "unread": unread_count,
            "data_age": result.stale_note,
            **meta,
            "mails": project(
                visible, ("mail_id", "from", "subject", "timestamp", "read"), concise
            ),
        }

    @mcp.tool()
    @handled
    async def eve_mail_read(
        mail_id: Annotated[
            int, Field(description="Mail id from eve_mail_list.", ge=1)
        ],
        character: CharacterArg = "",
    ) -> dict[str, Any]:
        """Fetch the full body text of one mail.

        Read-only: this does not mark the mail as read in game. Use eve_mail_mark
        for that.

        Returns: from, to[], subject, timestamp, body (markup stripped).
        """
        token = ctx.resolve_character(character)
        cid = token.character_id
        ctx.require_scope(token, "esi-mail.read_mail.v1", "mail")

        result = await ctx.esi.get(f"/characters/{cid}/mail/{mail_id}", character_id=cid)
        mail = result.data or {}
        party_ids = {mail.get("from")} | {
            r.get("recipient_id") for r in mail.get("recipients", []) or []
        }
        names = await ctx.resolver.names({p for p in party_ids if p})
        return {
            "mail_id": mail_id,
            "from": names.get(mail.get("from")),
            "to": [names.get(r.get("recipient_id")) for r in mail.get("recipients", []) or []],
            "subject": mail.get("subject"),
            "timestamp": mail.get("timestamp"),
            "body": _strip_markup(mail.get("body", "")),
        }

    @mcp.tool()
    @handled
    async def eve_social_notifications(
        character: CharacterArg = "",
        limit: _NotificationsLimit = 15,
        response_format: DetailArg = "concise",
    ) -> dict[str, Any]:
        """In-game notifications: structure attacks, war decs, corp and contract events.

        This is where genuinely time-critical things surface — a structure under
        attack, a POCO reinforced, a war declared. Check it when the user asks
        whether anything needs attention.

        The `detail` field is raw YAML straight from the game and contains
        unresolved numeric ids. Read what you can from it, but do not present
        those ids to the user as if they were names.

        Returns: unread count, notifications[] newest first.
        """
        token = ctx.resolve_character(character)
        cid = token.character_id
        ctx.require_scope(token, "esi-characters.read_notifications.v1", "notifications")
        concise = response_format == "concise"

        result = await ctx.esi.get(f"/characters/{cid}/notifications", character_id=cid)
        notes = [n for n in (result.data or []) if isinstance(n, dict)]
        if not notes:
            return {"character": token.character_name, "notifications": []}

        senders = await ctx.resolver.names({n["sender_id"] for n in notes if n.get("sender_id")})
        rows = [
            {
                "type": n.get("type"),
                "from": senders.get(n.get("sender_id"), n.get("sender_type")),
                "timestamp": n.get("timestamp"),
                "read": n.get("is_read", False),
                "detail": (n.get("text") or "").replace("\n", " ")[:300] or None,
            }
            for n in sorted(notes, key=lambda n: n.get("timestamp", ""), reverse=True)
        ]
        visible, meta = page(rows, limit)
        return {
            "character": token.character_name,
            "unread": sum(1 for r in rows if not r["read"]),
            "data_age": result.stale_note,
            **meta,
            "notifications": project(visible, ("type", "from", "timestamp", "read"), concise),
        }

    @mcp.tool()
    @handled
    async def eve_social_killmails(
        character: CharacterArg = "",
        limit: _KillmailsLimit = 8,
        response_format: DetailArg = "concise",
    ) -> dict[str, Any]:
        """Recent kills and losses involving this character.

        Each row says whether the character was on the killing side or was the
        one who died (`role`). `hull_value` covers the ship hull only — fitted
        modules, cargo and implants are not counted, so a real loss is usually
        considerably more expensive than this number suggests.

        Each row carries a zkillboard link, which has the full breakdown if the
        user wants it.

        Returns: kills, losses, killmails[] newest first. 'detailed' adds the
        victim's ship value, attacker count and the zkillboard link.
        """
        token = ctx.resolve_character(character)
        cid = token.character_id
        ctx.require_scope(token, "esi-killmails.read_killmails.v1", "killmails")
        concise = response_format == "concise"

        result = await ctx.esi.get(f"/characters/{cid}/killmails/recent", character_id=cid)
        available = [k for k in (result.data or []) if isinstance(k, dict)]
        refs = available[:limit]
        if not refs:
            return {"character": token.character_name, "killmails": [], "note": "Nothing recent."}

        details = await asyncio.gather(
            *(ctx.esi.get(f"/killmails/{k['killmail_id']}/{k['killmail_hash']}") for k in refs),
            return_exceptions=True,
        )
        # Each killmail is a separate request. Dropping the failures silently
        # would undercount kills/losses with nothing to show it happened.
        kills, failed = [], []
        for ref, detail in zip(refs, details):
            if isinstance(detail, BaseException):
                failed.append(ref.get("killmail_id"))
                log.warning("killmail %s could not be fetched: %s", ref.get("killmail_id"), detail)
            else:
                kills.append(detail.data)

        ids = set()
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
            was_victim = victim.get("character_id") == cid
            hull_price = unit_price(prices, victim.get("ship_type_id"))
            rows.append(
                {
                    "role": "loss" if was_victim else "kill",
                    "time": kill.get("killmail_time"),
                    "system": names.get(kill.get("solar_system_id")),
                    # Structure and NPC kills have no victim character, only a corp.
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
            # `available` was sliced before the detail fetch, so page() only ever
            # sees `limit` rows and would always report truncated: false.
            meta = {
                "returned": len(visible),
                "total_available": len(available),
                "truncated": True,
                "how_to_see_more": f"Raise `limit` (currently {limit}).",
            }
        out = {
            "character": token.character_name,
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
    async def eve_fitting_list(
        character: CharacterArg = "",
        limit: _FittingsLimit = 10,
        response_format: DetailArg = "concise",
    ) -> dict[str, Any]:
        """Saved ship fittings with their module lists.

        In 'concise' mode module lists are omitted and only the hull and module
        count are returned, because a full fitting is 20-40 lines. Ask for
        'detailed' when the user wants to see or copy an actual fit.

        Returns: fittings[] with fitting_id (needed by eve_fitting_delete).
        """
        token = ctx.resolve_character(character)
        cid = token.character_id
        ctx.require_scope(token, "esi-fittings.read_fittings.v1", "fittings")
        concise = response_format == "concise"

        result = await ctx.esi.get(f"/characters/{cid}/fittings", character_id=cid)
        fittings = [f for f in (result.data or []) if isinstance(f, dict)]
        if not fittings:
            return {"character": token.character_name, "fittings": [], "note": "None saved."}

        type_ids = {f["ship_type_id"] for f in fittings}
        for fitting in fittings:
            type_ids.update(i["type_id"] for i in fitting.get("items", []))
        names = await ctx.resolver.names(type_ids)

        rows = [
            {
                "fitting_id": f.get("fitting_id"),
                "name": f.get("name"),
                "ship": names.get(f["ship_type_id"]),
                "module_count": len(f.get("items", [])),
                "description": (f.get("description") or "")[:200] or None,
                "modules": [
                    f"{names.get(i['type_id'], i['type_id'])} x{i.get('quantity', 1)} [{i.get('flag')}]"
                    for i in f.get("items", [])
                ],
            }
            for f in fittings
        ]
        visible, meta = page(rows, limit)
        return {
            "character": token.character_name,
            "data_age": result.stale_note,
            **meta,
            "fittings": project(
                visible, ("fitting_id", "name", "ship", "module_count"), concise
            ),
        }


_TAG = re.compile(r"<[^>]+>")
_BR = re.compile(r"<br\s*/?>", re.IGNORECASE)


def _strip_markup(body: str) -> str:
    """EVE mail bodies are pseudo-HTML; flatten them for reading."""
    return _TAG.sub("", _BR.sub("\n", body)).strip()
