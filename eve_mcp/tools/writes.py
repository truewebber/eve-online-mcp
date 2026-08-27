"""Mutating tools.

Every tool here runs the same three-step dance:

    preview = {...}                          # what the user will see
    blocked = ctx.guard.authorize(...)       # capability + scope + budget + confirm
    if blocked: return blocked               # -> confirmation_required payload
    result = await ctx.esi.post(...)         # the actual call
    ctx.guard.record(...)                    # budget spend + audit line

Tools are only registered when their capability is enabled, so the tool list
itself is the outer security boundary.
"""
from __future__ import annotations

from typing import Annotated, Any, Literal

from mcp.server.mcpserver import MCPServer
from pydantic import Field

from ..context import AppContext
from ..esi import EsiError
from ..schema import CharacterArg, ConfirmTokenArg
from ._common import handled

_RECIPIENT_KEYS = {
    "characters": "character",
    "corporations": "corporation",
    "alliances": "alliance",
}

_MAX_MAIL_RECIPIENTS = 20

_CLIENT_CAVEAT = (
    "Takes effect only while the EVE client is running and logged in on this "
    "character. With the client closed the call reports success and nothing "
    "visible happens."
)


def register(mcp: MCPServer, ctx: AppContext) -> None:
    enabled = ctx.settings.capability_enabled

    if enabled("waypoint"):
        _register_waypoint(mcp, ctx)
    if enabled("openwindow"):
        _register_openwindow(mcp, ctx)
    if enabled("fittings"):
        _register_fittings(mcp, ctx)
    if enabled("mail_organize"):
        _register_mail_organize(mcp, ctx)
    if enabled("mail_send"):
        _register_mail_send(mcp, ctx)
    if enabled("contacts"):
        _register_contacts(mcp, ctx)
    if enabled("calendar"):
        _register_calendar(mcp, ctx)


# --------------------------------------------------------------------- waypoint


def _register_waypoint(mcp: MCPServer, ctx: AppContext) -> None:
    @mcp.tool()
    @handled
    async def eve_ui_set_waypoint(
        destination: Annotated[
            str,
            Field(
                description=(
                    "Exact system, station or structure name. Stations and player "
                    "structures set the waypoint to that dock specifically; a system "
                    "name routes to the system."
                ),
                min_length=1,
            ),
        ],
        character: CharacterArg = "",
        clear_other_waypoints: Annotated[
            bool,
            Field(
                description=(
                    "True replaces the whole existing route. False appends to it, "
                    "which is what you want when building a multi-stop trip."
                )
            ),
        ] = True,
        add_to_beginning: Annotated[
            bool,
            Field(description="Insert as the very next hop rather than the final stop."),
        ] = False,
        confirm_token: ConfirmTokenArg = "",
    ) -> dict[str, Any]:
        """Set an autopilot waypoint in the running game client.

        This only moves the route marker on the map. It never undocks, flies or
        activates autopilot — the player still does all of that.

        Destructive in one small way worth previewing honestly: the default
        `clear_other_waypoints=true` wipes a route the player may have spent
        time building.
        """
        token = ctx.resolve_character(character)
        target = await _resolve_destination(ctx, destination, token.character_id)
        if "error" in target:
            return target

        args = {
            "destination_id": target["id"],
            "character_id": token.character_id,
            "clear_other_waypoints": clear_other_waypoints,
            "add_to_beginning": add_to_beginning,
        }
        preview = {
            "action": "Set an autopilot waypoint in the game client",
            "character": token.character_name,
            "destination": f"{target['name']} ({target['kind']})",
            "clears_existing_route": clear_other_waypoints,
            "position": "next hop" if add_to_beginning else "final stop",
        }
        if target.get("ambiguity"):
            preview["ambiguous_name"] = (
                target["ambiguity"] + " — this routes to the first. Cancel and use "
                "eve_universe_search if the other one was meant."
            )
        blocked = ctx.guard.authorize(
            tool="eve_ui_set_waypoint",
            capability="waypoint",
            args=args,
            preview=preview,
            confirm_token=confirm_token or None,
            granted_scopes=token.scopes,
        )
        if blocked is not None:
            return blocked

        await ctx.esi.post(
            "/ui/autopilot/waypoint",
            character_id=token.character_id,
            params={
                "destination_id": target["id"],
                "clear_other_waypoints": clear_other_waypoints,
                "add_to_beginning": add_to_beginning,
            },
        )
        ctx.guard.record(
            tool="eve_ui_set_waypoint", capability="waypoint", args=args, result="ok"
        )
        return {
            "status": "done",
            "waypoint_set_to": target["name"],
            "note": _CLIENT_CAVEAT,
        }


# ------------------------------------------------------------------ open window


def _register_openwindow(mcp: MCPServer, ctx: AppContext) -> None:
    @mcp.tool()
    @handled
    async def eve_ui_open_window(
        window: Annotated[
            Literal["market", "info", "contract"],
            Field(
                description=(
                    "'market' opens the market details for an item type. 'info' opens "
                    "the Show Info panel for a character, corp, item or place. "
                    "'contract' opens one contract."
                )
            ),
        ],
        target: Annotated[
            str,
            Field(
                description=(
                    "For 'market', an exact item name. For 'info', an exact name of "
                    "any entity. For 'contract', the numeric contract_id from "
                    "eve_market_contracts."
                ),
                min_length=1,
            ),
        ],
        character: CharacterArg = "",
        confirm_token: ConfirmTokenArg = "",
    ) -> dict[str, Any]:
        """Open a window in the running game client.

        Good for handing something off to the player: rather than describing
        where to find an item on the market, put it on their screen. Changes
        nothing in game and costs nothing.
        """
        token = ctx.resolve_character(character)
        kind = window.strip().lower()

        if kind == "contract":
            if not target.strip().isdigit():
                return {
                    "error": (
                        "For window='contract', `target` must be the numeric "
                        "contract_id from eve_market_contracts (run it with "
                        "response_format='detailed' to get the id)."
                    )
                }
            path, params = "/ui/openwindow/contract", {"contract_id": int(target)}
            label = f"contract #{target}"
        else:
            resolved = await _resolve_entity(ctx, target, token.character_id, kind)
            if "error" in resolved:
                return resolved
            if kind == "market":
                path, params = "/ui/openwindow/marketdetails", {"type_id": resolved["id"]}
            else:
                path, params = "/ui/openwindow/information", {"target_id": resolved["id"]}
            kind_label = resolved.get("kind")
            label = (
                f"{resolved['name']} ({kind_label})"
                if kind_label and kind_label != "id"
                else resolved["name"]
            )

        args = {"window": kind, "params": params, "character_id": token.character_id}
        preview = {
            "action": f"Open the {kind} window in the game client",
            "character": token.character_name,
            "target": label,
        }
        if isinstance(resolved, dict) and resolved.get("ambiguity"):
            preview["ambiguous_name"] = (
                resolved["ambiguity"] + " — this opens the first. Cancel and use "
                "eve_universe_search if the other one was meant."
            )
        blocked = ctx.guard.authorize(
            tool="eve_ui_open_window",
            capability="openwindow",
            args=args,
            preview=preview,
            confirm_token=confirm_token or None,
            granted_scopes=token.scopes,
        )
        if blocked is not None:
            return blocked

        await ctx.esi.post(path, character_id=token.character_id, params=params)
        ctx.guard.record(
            tool="eve_ui_open_window", capability="openwindow", args=args, result="ok"
        )
        return {"status": "done", "opened": label, "note": _CLIENT_CAVEAT}


# --------------------------------------------------------------------- fittings


def _register_fittings(mcp: MCPServer, ctx: AppContext) -> None:
    @mcp.tool()
    @handled
    async def eve_fitting_save(
        name: Annotated[
            str,
            Field(description="Fitting name as it will appear in game.", min_length=1, max_length=50),
        ],
        ship: Annotated[
            str,
            Field(description="Exact hull name, e.g. 'Rifter'.", min_length=1),
        ],
        modules: Annotated[
            list[dict[str, Any]],
            Field(
                description=(
                    "Modules as objects: {'name': '200mm AutoCannon II', 'flag': "
                    "'HiSlot0', 'quantity': 1}. Names must be exact. Valid flags: "
                    "HiSlot0-7, MedSlot0-7, LoSlot0-7, RigSlot0-2, SubSystemSlot0-4, "
                    "DroneBay, FighterBay, Cargo. Each fitted module needs its own "
                    "slot number — two modules cannot share HiSlot0."
                ),
                min_length=1,
            ),
        ],
        description: Annotated[
            str, Field(description="Optional note stored with the fitting.", max_length=500)
        ] = "",
        character: CharacterArg = "",
        confirm_token: ConfirmTokenArg = "",
    ) -> dict[str, Any]:
        """Save a ship fitting to the character's in-game fitting list.

        Does not buy, move or fit anything — it stores a template the player can
        apply later. Unknown module names are rejected before anything is saved,
        with the offending names listed.
        """
        token = ctx.resolve_character(character)

        wanted = [ship] + [m.get("name", "") for m in modules]
        resolutions = await ctx.resolver.resolve_names(wanted, only=("inventory_types",))
        by_name = {k: r.chosen.id for k, r in resolutions.items() if r.chosen}

        ship_id = by_name.get(ship.strip().lower())
        if ship_id is None:
            return {
                "error": (
                    f"No hull is named exactly {ship!r}. Check the spelling with "
                    "eve_universe_search."
                )
            }

        items, unknown = [], []
        for module in modules:
            module_name = (module.get("name") or "").strip()
            type_id = by_name.get(module_name.lower())
            if type_id is None:
                unknown.append(module_name)
                continue
            items.append(
                {
                    "type_id": type_id,
                    "flag": module.get("flag", "Cargo"),
                    "quantity": int(module.get("quantity", 1)),
                }
            )
        if unknown:
            return {
                "error": (
                    f"These module names do not exist exactly as written: {unknown}. "
                    "Look each one up with eve_universe_search first."
                )
            }

        body = {
            "name": name[:50],
            "description": description[:500],
            "ship_type_id": ship_id,
            "items": items,
        }
        preview = {
            "action": "Save a new fitting to the in-game fitting list",
            "character": token.character_name,
            "fitting_name": body["name"],
            "hull": ship,
            "modules": [
                f"{m.get('name')} x{m.get('quantity', 1)} [{m.get('flag')}]" for m in modules
            ],
        }
        # The digest must cover WHICH character acts, or a preview approved for one
        # can be redeemed against another. Kept separate from `body`: that dict is
        # the ESI request payload and must not grow an unexpected field.
        args = {**body, "character_id": token.character_id}
        blocked = ctx.guard.authorize(
            tool="eve_fitting_save",
            capability="fittings",
            args=args,
            preview=preview,
            confirm_token=confirm_token or None,
            granted_scopes=token.scopes,
        )
        if blocked is not None:
            return blocked

        result = await ctx.esi.post(
            f"/characters/{token.character_id}/fittings",
            character_id=token.character_id,
            json_body=body,
        )
        ctx.guard.record(tool="eve_fitting_save", capability="fittings", args=args, result=result)
        return {
            "status": "done",
            "fitting_id": (result or {}).get("fitting_id"),
            "name": body["name"],
        }

    @mcp.tool()
    @handled
    async def eve_fitting_delete(
        fitting_id: Annotated[
            int,
            Field(
                description=(
                    "Fitting id from eve_fitting_list. Run that first — the preview "
                    "will name the fitting so the user can confirm it is the right one."
                ),
                ge=1,
            ),
        ],
        character: CharacterArg = "",
        confirm_token: ConfirmTokenArg = "",
    ) -> dict[str, Any]:
        """Delete a saved fitting. Permanent — there is no undo in game.

        The preview names the fitting and its module count, so the user can
        check it is the one they meant before confirming.
        """
        token = ctx.resolve_character(character)
        existing = await ctx.esi.get(
            f"/characters/{token.character_id}/fittings", character_id=token.character_id
        )
        match = next(
            (f for f in (existing.data or []) if f.get("fitting_id") == fitting_id), None
        )
        if match is None:
            return {
                "error": (
                    f"{token.character_name} has no fitting with id {fitting_id}. "
                    "Call eve_fitting_list to see the real ids."
                )
            }

        args = {"fitting_id": fitting_id, "character_id": token.character_id}
        preview = {
            "action": "Permanently delete a saved fitting",
            "character": token.character_name,
            "fitting_name": match.get("name"),
            "modules": len(match.get("items", [])),
        }
        blocked = ctx.guard.authorize(
            tool="eve_fitting_delete",
            capability="fittings",
            args=args,
            preview=preview,
            confirm_token=confirm_token or None,
            granted_scopes=token.scopes,
        )
        if blocked is not None:
            return blocked

        await ctx.esi.delete(
            f"/characters/{token.character_id}/fittings/{fitting_id}",
            character_id=token.character_id,
        )
        ctx.guard.record(
            tool="eve_fitting_delete", capability="fittings", args=args, result="ok"
        )
        return {"status": "done", "deleted": match.get("name")}


# ---------------------------------------------------------------- mail organize


def _register_mail_organize(mcp: MCPServer, ctx: AppContext) -> None:
    @mcp.tool()
    @handled
    async def eve_mail_mark(
        mail_id: Annotated[
            int, Field(description="Mail id from eve_mail_list.", ge=1)
        ],
        read: Annotated[
            bool, Field(description="True marks it read, False marks it unread.")
        ] = True,
        character: CharacterArg = "",
        confirm_token: ConfirmTokenArg = "",
    ) -> dict[str, Any]:
        """Change the read flag on one mail.

        This does not return the mail's contents — use eve_mail_read for that.
        Named `mark` rather than `read` precisely so the two are not confused.
        """
        token = ctx.resolve_character(character)
        args = {"mail_id": mail_id, "read": read, "character_id": token.character_id}
        preview = {
            "action": f"Mark mail #{mail_id} as {'read' if read else 'unread'}",
            "character": token.character_name,
        }
        blocked = ctx.guard.authorize(
            tool="eve_mail_mark",
            capability="mail_organize",
            args=args,
            preview=preview,
            confirm_token=confirm_token or None,
            granted_scopes=token.scopes,
        )
        if blocked is not None:
            return blocked

        await ctx.esi.put(
            f"/characters/{token.character_id}/mail/{mail_id}",
            character_id=token.character_id,
            json_body={"read": read},
        )
        ctx.guard.record(
            tool="eve_mail_mark", capability="mail_organize", args=args, result="ok"
        )
        return {"status": "done", "mail_id": mail_id, "read": read}

    @mcp.tool()
    @handled
    async def eve_mail_delete(
        mail_id: Annotated[
            int, Field(description="Mail id from eve_mail_list.", ge=1)
        ],
        character: CharacterArg = "",
        confirm_token: ConfirmTokenArg = "",
    ) -> dict[str, Any]:
        """Delete one mail. Permanent — deleted EVE mail cannot be recovered.

        The preview shows the sender, subject and date so the user can verify
        which mail is about to go.
        """
        token = ctx.resolve_character(character)
        header = await ctx.esi.get(
            f"/characters/{token.character_id}/mail/{mail_id}", character_id=token.character_id
        )
        mail = header.data or {}
        sender = await ctx.resolver.name(mail.get("from", 0))

        args = {"mail_id": mail_id, "character_id": token.character_id}
        preview = {
            "action": "Permanently delete a mail",
            "character": token.character_name,
            "subject": mail.get("subject"),
            "from": sender,
            "timestamp": mail.get("timestamp"),
        }
        blocked = ctx.guard.authorize(
            tool="eve_mail_delete",
            capability="mail_organize",
            args=args,
            preview=preview,
            confirm_token=confirm_token or None,
            granted_scopes=token.scopes,
        )
        if blocked is not None:
            return blocked

        await ctx.esi.delete(
            f"/characters/{token.character_id}/mail/{mail_id}",
            character_id=token.character_id,
        )
        ctx.guard.record(
            tool="eve_mail_delete", capability="mail_organize", args=args, result="ok"
        )
        return {"status": "done", "deleted_subject": mail.get("subject")}


# -------------------------------------------------------------------- mail send


def _register_mail_send(mcp: MCPServer, ctx: AppContext) -> None:
    @mcp.tool()
    @handled
    async def eve_mail_send(
        to: Annotated[
            list[str],
            Field(
                description=(
                    "Exact character, corporation or alliance names. Every name must "
                    "resolve or nothing is sent. Capped at 20 recipients per mail."
                ),
                min_length=1,
            ),
        ],
        subject: Annotated[
            str, Field(description="Mail subject.", min_length=1, max_length=1000)
        ],
        body: Annotated[
            str, Field(description="Mail body text.", min_length=1, max_length=10000)
        ],
        character: CharacterArg = "",
        approved_cost: Annotated[
            int,
            Field(
                description=(
                    "ISK you accept paying for CSPA charges — a fee some players set "
                    "for receiving mail from strangers. 0 refuses to pay anything and "
                    "the send fails instead of silently charging."
                ),
                ge=0,
            ),
        ] = 0,
        confirm_token: ConfirmTokenArg = "",
    ) -> dict[str, Any]:
        """Send an in-game EVE mail from this character to other players.

        The most consequential tool on this server. The mail is signed with the
        user's character name, arrives in another human's inbox, cannot be
        recalled, and can be screenshotted and shared.

        Show the preview to the user word for word — including the full body —
        and get an explicit yes before confirming. Do not paraphrase what the
        mail says. Do not compose and send in a single turn on the user's
        general instruction; they should see the exact text first.
        """
        token = ctx.resolve_character(character)
        if len(to) > _MAX_MAIL_RECIPIENTS:
            return {
                "error": (
                    f"Refusing to mail {len(to)} recipients at once; the cap is "
                    f"{_MAX_MAIL_RECIPIENTS}. Send in smaller batches."
                )
            }

        resolutions = await ctx.resolver.resolve_names(to, only=tuple(_RECIPIENT_KEYS))
        recipients, resolved_names, unknown, ambiguous, seen = [], [], [], [], set()
        for asked in to:
            match = resolutions[asked.strip().lower()]
            if match.chosen is None:
                unknown.append(asked)
            elif match.ambiguous:
                ambiguous.append(match)
            elif match.chosen.id not in seen:
                seen.add(match.chosen.id)
                recipients.append(
                    {
                        "recipient_id": match.chosen.id,
                        "recipient_type": _RECIPIENT_KEYS[match.chosen.category],
                    }
                )
                resolved_names.append(f"{match.chosen.name} ({match.chosen.kind})")
        if unknown:
            return {
                "error": (
                    f"Could not resolve recipient(s): {unknown}. Names must match "
                    "exactly; check them with eve_universe_search. Nothing was sent."
                )
            }
        if ambiguous:
            return {
                "error": (
                    "Refusing to send — "
                    + "; ".join(m.describe() for m in ambiguous)
                    + ". EVE mail cannot be recalled, so confirm which one is meant "
                    "with eve_universe_search (categories='character,corporation,"
                    "alliance') and send again naming only that one. Nothing was sent."
                )
            }

        payload = {
            "recipients": recipients,
            "subject": subject[:1000],
            "body": body[:10000],
            "approved_cost": max(0, int(approved_cost)),
        }
        preview = {
            "action": "SEND AN IN-GAME MAIL — another player will receive this and it cannot be recalled",
            "from": token.character_name,
            "to": resolved_names,
            "subject": payload["subject"],
            "body": payload["body"],
            "approved_cspa_cost_isk": payload["approved_cost"],
        }
        # Same reason as eve_fitting_save, and it matters more here: the preview says
        # "from: <name>" but that was never enforced, so an approval could be
        # redeemed while acting as a different character. Also puts the sender in
        # the audit log, which previously recorded the mail but not who sent it.
        args = {**payload, "character_id": token.character_id}
        blocked = ctx.guard.authorize(
            tool="eve_mail_send",
            capability="mail_send",
            args=args,
            preview=preview,
            confirm_token=confirm_token or None,
            granted_scopes=token.scopes,
        )
        if blocked is not None:
            return blocked

        mail_id = await ctx.esi.post(
            f"/characters/{token.character_id}/mail",
            character_id=token.character_id,
            json_body=payload,
        )
        ctx.guard.record(
            tool="eve_mail_send", capability="mail_send", args=args, result=mail_id
        )
        return {"status": "sent", "mail_id": mail_id, "to": resolved_names}


# --------------------------------------------------------------------- contacts


def _register_contacts(mcp: MCPServer, ctx: AppContext) -> None:
    @mcp.tool()
    @handled
    async def eve_contacts_set(
        names: Annotated[
            list[str],
            Field(
                description="Exact character, corporation or alliance names.",
                min_length=1,
            ),
        ],
        standing: Annotated[
            float,
            Field(
                description=(
                    "-10.0 to 10.0. Negative marks hostiles (they show red in the "
                    "overview), positive marks friends. Applied to every name in one "
                    "call, so split the call if they need different standings."
                ),
                ge=-10.0,
                le=10.0,
            ),
        ],
        watched: Annotated[
            bool,
            Field(description="Add to the watch list, so you see when they log in. Characters only."),
        ] = False,
        character: CharacterArg = "",
        confirm_token: ConfirmTokenArg = "",
    ) -> dict[str, Any]:
        """Add or update contacts with a standing.

        Standings are not private in effect: a negative standing colours that
        player red in the character's overview, and setting one on a corp or
        alliance affects how their members appear. Treat it as a visible social
        act rather than a private note.
        """
        token = ctx.resolve_character(character)

        matches, failure = await _resolve_contacts(ctx, names)
        if failure is not None:
            return failure
        contact_ids = [m.id for m in matches]
        resolved = [m.name for m in matches]
        # ESI only accepts watched=true for characters; sending it for a corp or
        # alliance 400s the whole batch, so the two groups go in separate calls.
        watchable = {m.id for m in matches if m.category == "characters"}

        existing = await ctx.esi.get_all_pages(
            f"/characters/{token.character_id}/contacts", character_id=token.character_id
        )
        known = {c["contact_id"] for c in (existing.data or [])}
        updating = [i for i in contact_ids if i in known]

        args = {
            "contact_ids": sorted(contact_ids),
            "standing": standing,
            "watched": watched,
            "character_id": token.character_id,
        }
        preview = {
            "action": "Set contact standings (visible in the character's overview)",
            "character": token.character_name,
            "contacts": resolved,
            "standing": standing,
            "watched": watched,
            "new_contacts": len(contact_ids) - len(updating),
            "updated_contacts": len(updating),
        }
        if watched and len(watchable) != len(contact_ids):
            preview["watched_note"] = (
                f"Only {len(watchable)} of {len(contact_ids)} are characters; the rest "
                "are corporations or alliances, which cannot be watched and will be "
                "added without it."
            )
        blocked = ctx.guard.authorize(
            tool="eve_contacts_set",
            capability="contacts",
            args=args,
            preview=preview,
            confirm_token=confirm_token or None,
            granted_scopes=token.scopes,
        )
        if blocked is not None:
            return blocked

        new = [i for i in contact_ids if i not in known]
        # One authorize() covers the whole logical operation, but each ESI call is
        # its own mutation and gets its own record(), so a half-applied run is
        # still fully audited and fully budgeted.
        operations = []
        for verb, ids in (("update", updating), ("add", new)):
            if not ids:
                continue
            if not watched:
                # Nothing to split on: one call carries the whole group.
                operations.append((verb, ids, False))
                continue
            for is_watched in (True, False):
                group = [i for i in ids if (i in watchable) == is_watched]
                if group:
                    operations.append((verb, group, is_watched))

        path = f"/characters/{token.character_id}/contacts"
        applied: dict[str, list[int]] = {"updated": [], "added": []}
        try:
            for verb, ids, flag in operations:
                call = ctx.esi.put if verb == "update" else ctx.esi.post
                await call(
                    path,
                    character_id=token.character_id,
                    params={"standing": standing, "watched": flag},
                    json_body=ids,
                )
                applied["updated" if verb == "update" else "added"].extend(ids)
                ctx.guard.record(
                    tool="eve_contacts_set",
                    capability="contacts",
                    args={**args, "phase": verb, "contact_ids": ids, "watched": flag},
                    result="ok",
                )
        except EsiError as exc:
            if not (applied["updated"] or applied["added"]):
                raise
            ctx.guard.audit(
                {
                    "event": "partial_write",
                    "tool": "eve_contacts_set",
                    "capability": "contacts",
                    "completed": applied,
                    "error": str(exc),
                }
            )
            return {
                "error": (
                    f"Partially applied. Standing {standing} reached "
                    f"{len(applied['updated'])} existing and {len(applied['added'])} new "
                    f"contact(s) before this failed: {exc}. Call eve_contacts_set again "
                    "with the same arguments — it re-reads the contact list first, so "
                    "what already landed is skipped and only the rest is retried."
                ),
                "kind": "EsiError",
                "status": exc.status,
            }
        return {"status": "done", "contacts": resolved, "standing": standing}

    @mcp.tool()
    @handled
    async def eve_contacts_delete(
        names: Annotated[
            list[str],
            Field(description="Exact contact names to remove.", min_length=1),
        ],
        character: CharacterArg = "",
        confirm_token: ConfirmTokenArg = "",
    ) -> dict[str, Any]:
        """Remove contacts from the character's contact list.

        Removing a contact drops any standing set on them, which can change who
        shows as hostile in the overview.
        """
        token = ctx.resolve_character(character)
        matches, failure = await _resolve_contacts(ctx, names)
        if failure is not None:
            return failure
        contact_ids = [m.id for m in matches]
        resolved = [m.name for m in matches]

        args = {"contact_ids": sorted(contact_ids), "character_id": token.character_id}
        preview = {
            "action": "Delete contacts and the standings set on them",
            "character": token.character_name,
            "contacts": resolved,
        }
        blocked = ctx.guard.authorize(
            tool="eve_contacts_delete",
            capability="contacts",
            args=args,
            preview=preview,
            confirm_token=confirm_token or None,
            granted_scopes=token.scopes,
        )
        if blocked is not None:
            return blocked

        await ctx.esi.delete(
            f"/characters/{token.character_id}/contacts",
            character_id=token.character_id,
            params={"contact_ids": contact_ids},
        )
        ctx.guard.record(
            tool="eve_contacts_delete", capability="contacts", args=args, result="ok"
        )
        return {"status": "done", "removed": resolved}


# --------------------------------------------------------------------- calendar


def _register_calendar(mcp: MCPServer, ctx: AppContext) -> None:
    @mcp.tool()
    @handled
    async def eve_calendar_respond(
        event_id: Annotated[
            int, Field(description="Event id from the in-game calendar.", ge=1)
        ],
        response: Annotated[
            Literal["accepted", "declined", "tentative"],
            Field(description="The answer to send."),
        ],
        character: CharacterArg = "",
        confirm_token: ConfirmTokenArg = "",
    ) -> dict[str, Any]:
        """Respond to a calendar event invitation.

        The organiser and other invitees see the answer, and fleet operations
        often plan around the accepted headcount — so this commits the user to
        something other people act on.
        """
        token = ctx.resolve_character(character)
        detail = await ctx.esi.get(
            f"/characters/{token.character_id}/calendar/{event_id}",
            character_id=token.character_id,
        )
        event = detail.data or {}

        args = {
            "event_id": event_id,
            "response": response,
            "character_id": token.character_id,
        }
        preview = {
            "action": "Respond to a calendar invitation — the organiser is notified",
            "character": token.character_name,
            "event": event.get("title"),
            "date": event.get("date"),
            "owner": event.get("owner_name"),
            "response": response,
        }
        blocked = ctx.guard.authorize(
            tool="eve_calendar_respond",
            capability="calendar",
            args=args,
            preview=preview,
            confirm_token=confirm_token or None,
            granted_scopes=token.scopes,
        )
        if blocked is not None:
            return blocked

        await ctx.esi.put(
            f"/characters/{token.character_id}/calendar/{event_id}",
            character_id=token.character_id,
            json_body={"response": response},
        )
        ctx.guard.record(
            tool="eve_calendar_respond", capability="calendar", args=args, result="ok"
        )
        return {"status": "done", "event": event.get("title"), "response": response}


# ---------------------------------------------------------------------- helpers


async def _resolve_contacts(ctx: AppContext, names: list[str]) -> tuple[list, dict[str, Any] | None]:
    """Resolve contact names all-or-nothing, refusing anything ambiguous.

    A standing is a visible social act — it colours someone red in the overview
    — so acting on a coin-flip between a character and a same-named corporation
    is not acceptable, and neither is silently skipping a typo.
    """
    resolutions = await ctx.resolver.resolve_names(names, only=tuple(_RECIPIENT_KEYS))
    matches, unknown, ambiguous, seen = [], [], [], set()
    for asked in names:
        match = resolutions[asked.strip().lower()]
        if match.chosen is None:
            unknown.append(asked)
        elif match.ambiguous:
            ambiguous.append(match)
        elif match.chosen.id not in seen:
            seen.add(match.chosen.id)
            matches.append(match.chosen)
    if unknown:
        return [], {
            "error": (
                f"Could not resolve: {unknown}. Names must be exact — check them with "
                "eve_universe_search. Nothing was changed."
            )
        }
    if ambiguous:
        return [], {
            "error": (
                "Refusing to act — "
                + "; ".join(m.describe() for m in ambiguous)
                + ". Confirm which one is meant with eve_universe_search "
                "(categories='character,corporation,alliance') and call again naming "
                "only that one. Nothing was changed."
            )
        }
    return matches, None


async def _resolve_destination(ctx: AppContext, name: str, character_id: int) -> dict[str, Any]:
    """Waypoints accept systems, stations and player structures."""
    order = ("stations", "systems")
    match = (await ctx.resolver.resolve_names([name], prefer=order, only=order))[
        name.strip().lower()
    ]
    if match.chosen is not None:
        return {
            "id": match.chosen.id,
            "name": match.chosen.name,
            "kind": match.chosen.kind,
            "ambiguity": match.describe() if match.ambiguous else None,
        }

    search = await ctx.esi.get(
        f"/characters/{character_id}/search",
        character_id=character_id,
        params={"categories": ["structure"], "search": name, "strict": False},
    )
    structures = (search.data or {}).get("structure") or []
    if structures:
        structure_id = structures[0]
        structure_name = await ctx.resolver.name(structure_id, character_id=character_id)
        return {"id": structure_id, "name": structure_name, "kind": "structure"}
    return {
        "error": (
            f"No system, station or visible structure is named exactly {name!r}. "
            "Check the spelling with eve_universe_search."
        )
    }


async def _resolve_entity(
    ctx: AppContext, name: str, character_id: int, kind: str
) -> dict[str, Any]:
    if name.strip().isdigit():
        return {
            "id": int(name),
            "name": await ctx.resolver.name(int(name), character_id),
            "kind": "id",
        }
    if kind == "market":
        prefer = only = ("inventory_types",)
    else:
        prefer = (
            "characters", "corporations", "alliances",
            "inventory_types", "systems", "stations",
        )
        only = ()
    match = (await ctx.resolver.resolve_names([name], prefer=prefer, only=only))[
        name.strip().lower()
    ]
    if match.chosen is not None:
        return {
            "id": match.chosen.id,
            "name": match.chosen.name,
            "kind": match.chosen.kind,
            "ambiguity": match.describe() if match.ambiguous else None,
        }
    return {
        "error": (
            f"Could not resolve {name!r} for the {kind} window. Check the exact name "
            "with eve_universe_search."
        )
    }
