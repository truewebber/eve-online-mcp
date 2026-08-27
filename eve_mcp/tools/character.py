"""Character sheet: skills, training queue, clones, standings."""
from __future__ import annotations

import asyncio
from datetime import datetime, timezone
from typing import Annotated, Any

from mcp.server.mcpserver import MCPServer
from pydantic import Field

from ..context import AppContext
from ..schema import CharacterArg, DetailArg, limit_arg
from ._common import handled, page, project

_SkillsLimit = limit_arg("skills", 400)
_StandingsEntriesLimit = limit_arg("standings entries", 300)

_ROMAN = {0: "0", 1: "I", 2: "II", 3: "III", 4: "IV", 5: "V"}


def register(mcp: MCPServer, ctx: AppContext) -> None:
    @mcp.tool()
    @handled
    async def eve_character_skills(
        character: CharacterArg = "",
        search: Annotated[
            str,
            Field(
                description=(
                    "Case-insensitive substring of the skill name, e.g. 'Gunnery' or "
                    "'Caldari'. Strongly recommended — a full skill list is hundreds "
                    "of rows and answers almost nothing on its own."
                )
            ),
        ] = "",
        trained_only: Annotated[
            bool,
            Field(
                description=(
                    "Hide skills that are injected but sitting at level 0. Turn off "
                    "only when checking whether a skill book has been injected at all."
                )
            ),
        ] = True,
        limit: _SkillsLimit = 20,
        response_format: DetailArg = "concise",
    ) -> dict[str, Any]:
        """Trained skills with levels and skill points.

        Prefer `search` over dumping everything: to answer "can I fly a Drake"
        you want the handful of relevant skills, not all 118.

        One subtlety worth surfacing to the user: `active_level` can be lower
        than `level`. That means the account is on an Alpha (free) clone, which
        caps many skills below what has actually been trained. If you see the
        two diverge, say so — the character is not getting the benefit of
        training they already paid for.

        Returns: total_sp, unallocated_sp, skills_known, at_level_5, skills[].
        'detailed' adds skillpoints and active_level per skill.
        """
        token = ctx.resolve_character(character)
        cid = token.character_id
        ctx.require_scope(token, "esi-skills.read_skills.v1", "skills")
        concise = response_format == "concise"

        result = await ctx.esi.get(f"/characters/{cid}/skills", character_id=cid)
        payload = result.data or {}
        skills = [s for s in payload.get("skills", []) if isinstance(s, dict)]
        names = await ctx.resolver.names({s["skill_id"] for s in skills})

        needle = search.strip().lower()
        rows = []
        capped = 0
        for skill in skills:
            name = names.get(skill["skill_id"], f"#{skill['skill_id']}")
            level = skill.get("trained_skill_level", 0)
            active = skill.get("active_skill_level", level)
            if active < level:
                capped += 1
            if trained_only and not level:
                continue
            if needle and needle not in name.lower():
                continue
            rows.append(
                {
                    "skill": name,
                    "level": _ROMAN.get(level, str(level)),
                    "skillpoints": skill.get("skillpoints_in_skill"),
                    "active_level": _ROMAN.get(active, str(active)),
                }
            )
        rows.sort(key=lambda r: (r["skill"]))
        visible, meta = page(rows, limit, "Narrow with `search`, or raise `limit`.")

        out = {
            "character": token.character_name,
            "total_sp": payload.get("total_sp"),
            "unallocated_sp": payload.get("unallocated_sp"),
            "skills_known": len(skills),
            "at_level_5": sum(1 for s in skills if s.get("trained_skill_level") == 5),
            "matching": len(rows),
            "data_age": result.stale_note,
            **meta,
            "skills": project(visible, ("skill", "level"), concise),
        }
        if capped:
            out["alpha_clone_warning"] = (
                f"{capped} skills have active_level below trained level — this account "
                "looks like it is on an Alpha clone, so trained levels are capped."
            )
        return out

    @mcp.tool()
    @handled
    async def eve_character_skill_queue(character: CharacterArg = "") -> dict[str, Any]:
        """The training queue: what is training now, what follows, and when it runs dry.

        An empty queue means the character is accruing nothing — always worth
        telling the user, because it is silent in game and easy to miss.

        Cheap to call (a dozen rows at most), so prefer it over eve_character_skills
        when the question is about training rather than what is already known.

        Returns: queued_skills, training_now, queue_empty_in, queue_ends, queue[].
        """
        token = ctx.resolve_character(character)
        cid = token.character_id
        ctx.require_scope(token, "esi-skills.read_skillqueue.v1", "the skill queue")

        result = await ctx.esi.get(f"/characters/{cid}/skillqueue", character_id=cid)
        entries = [e for e in (result.data or []) if isinstance(e, dict)]
        if not entries:
            return {
                "character": token.character_name,
                "queue": [],
                "warning": (
                    "The skill queue is empty. This character is accruing no skill "
                    "points at all until something is queued."
                ),
            }
        names = await ctx.resolver.names({e["skill_id"] for e in entries})
        now = datetime.now(timezone.utc)

        rows = []
        for entry in sorted(entries, key=lambda e: e.get("queue_position", 0)):
            finish = _parse(entry.get("finish_date"))
            rows.append(
                {
                    "position": entry.get("queue_position"),
                    "skill": names.get(entry["skill_id"], f"#{entry['skill_id']}"),
                    "to_level": _ROMAN.get(entry.get("finished_level", 0)),
                    "finishes_in": _human_delta(finish - now) if finish else "paused",
                    "finish_date": entry.get("finish_date"),
                }
            )
        last = _parse(rows[-1]["finish_date"]) if rows[-1]["finish_date"] else None
        return {
            "character": token.character_name,
            "queued_skills": len(rows),
            "training_now": f"{rows[0]['skill']} {rows[0]['to_level'] or ''}".strip(),
            "queue_empty_in": _human_delta(last - now) if last else "unknown",
            "queue_ends": rows[-1]["finish_date"],
            "data_age": result.stale_note,
            "queue": rows,
        }

    @mcp.tool()
    @handled
    async def eve_character_clones(character: CharacterArg = "") -> dict[str, Any]:
        """Jump clones with their locations and implants, plus the active clone's implants.

        Useful for "where can I jump to" and "what implants would I lose if I
        died right now" — implants in the active clone are destroyed on podding,
        the ones sitting in jump clones are not.

        Returns: home_station, last_clone_jump, active_implants[], jump_clones[].
        """
        token = ctx.resolve_character(character)
        cid = token.character_id
        ctx.require_scope(token, "esi-clones.read_clones.v1", "clones")
        # The implants call is a second endpoint with a second scope; without
        # this check it fails the whole tool with an unexplained 403.
        ctx.require_scope(token, "esi-clones.read_implants.v1", "the active clone's implants")

        clones_result, implants_result = await asyncio.gather(
            ctx.esi.get(f"/characters/{cid}/clones", character_id=cid),
            ctx.esi.get(f"/characters/{cid}/implants", character_id=cid),
        )
        clones = clones_result.data or {}
        active_implants = implants_result.data or []

        jump_clones = clones.get("jump_clones", [])
        implant_ids = set(active_implants)
        location_ids = set()
        for clone in jump_clones:
            implant_ids.update(clone.get("implants", []))
            location_ids.add(clone.get("location_id"))
        home = clones.get("home_location", {})
        if home.get("location_id"):
            location_ids.add(home["location_id"])

        names = await ctx.resolver.names(implant_ids | location_ids, character_id=cid)
        return {
            "character": token.character_name,
            "home_station": names.get(home.get("location_id")),
            "last_clone_jump": clones.get("last_clone_jump_date"),
            "active_implants": [names.get(i, f"#{i}") for i in active_implants],
            "jump_clones": [
                {
                    "name": clone.get("name") or f"Clone {clone.get('jump_clone_id')}",
                    "location": names.get(clone.get("location_id"), "unknown"),
                    "implants": [names.get(i, f"#{i}") for i in clone.get("implants", [])],
                }
                for clone in jump_clones
            ],
            "data_age": clones_result.stale_note,
        }

    @mcp.tool()
    @handled
    async def eve_character_standings(
        character: CharacterArg = "",
        limit: _StandingsEntriesLimit = 20,
    ) -> dict[str, Any]:
        """NPC faction and corporation standings, plus loyalty point balances.

        Standings run -10 to +10 and gate agent access, broker fees and whether
        a faction's navy shoots you. Loyalty points are the currency earned from
        that faction's missions, spendable in their LP store.

        Returns: loyalty_points[], standings[] sorted best-first.
        """
        token = ctx.resolve_character(character)
        cid = token.character_id
        ctx.require_scope(token, "esi-characters.read_standings.v1", "standings")
        # Loyalty points are a separate endpoint with a separate scope. Missing it
        # is not fatal — standings still answer — so note it instead of raising.
        lp_scope = "esi-characters.read_loyalty.v1"
        lp_granted = lp_scope in token.scopes

        calls = [ctx.esi.get(f"/characters/{cid}/standings", character_id=cid)]
        if lp_granted:
            # Skipping the call when the scope is absent avoids spending a
            # guaranteed 403 against the shared ESI error budget every time.
            calls.append(ctx.esi.get(f"/characters/{cid}/loyalty/points", character_id=cid))
        results = await asyncio.gather(*calls, return_exceptions=True)
        standings_result = results[0]
        lp_result = results[1] if lp_granted else None
        standings = getattr(standings_result, "data", []) or []
        loyalty = getattr(lp_result, "data", []) or []

        ids = {s["from_id"] for s in standings} | {l["corporation_id"] for l in loyalty}
        names = await ctx.resolver.names(ids)

        rows = sorted(
            (
                {
                    "entity": names.get(s["from_id"], f"#{s['from_id']}"),
                    "type": s.get("from_type"),
                    "standing": round(s.get("standing", 0.0), 2),
                }
                for s in standings
            ),
            key=lambda r: -r["standing"],
        )
        visible, meta = page(rows, limit)
        out = {
            "character": token.character_name,
            "loyalty_points": [
                {"corporation": names.get(l["corporation_id"]), "lp": l.get("loyalty_points")}
                for l in sorted(loyalty, key=lambda l: -l.get("loyalty_points", 0))
            ],
            **meta,
            "standings": visible,
        }
        if isinstance(standings_result, Exception):
            # An empty list here would be indistinguishable from "no standings".
            out["standings_note"] = (
                f"Standings could not be read: {standings_result}. The list above is "
                "empty because the call failed, not because there are none."
            )
        if isinstance(lp_result, Exception):
            out["loyalty_points_note"] = (
                f"Loyalty points could not be read: {lp_result}."
            )
        elif not lp_granted:
            out["loyalty_points_note"] = (
                f"{token.character_name} was not authorized with '{lp_scope}', so "
                "loyalty point balances are not available. Re-run the login for this "
                "character to include them."
            )
        return out


def _parse(value: str | None) -> datetime | None:
    if not value:
        return None
    try:
        return datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        return None


def _human_delta(delta) -> str:
    seconds = int(delta.total_seconds())
    if seconds < 0:
        return "done"
    days, rem = divmod(seconds, 86400)
    hours, rem = divmod(rem, 3600)
    minutes = rem // 60
    if days:
        return f"{days}d {hours}h"
    if hours:
        return f"{hours}h {minutes}m"
    return f"{minutes}m"
