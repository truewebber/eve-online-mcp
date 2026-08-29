"""Shared application context handed to every tool module."""
from __future__ import annotations

import logging
from dataclasses import dataclass
from typing import Any


import httpx

from .auth import AuthError, CharacterToken, SsoClient
from .cache import Store
from .config import CORP_READ_SCOPES, Settings
from .esi import EsiClient, EsiError
from .names import Resolver
from .safety import WriteGuard

log = logging.getLogger("eve_mcp")

#: Player-created corporations start here. Everything below is an NPC corp
#: (school, militia, faction warfare) whose hangars ESI will not open.
PLAYER_CORP_ID_FLOOR = 98_000_000


class CharacterNotFound(RuntimeError):
    pass


@dataclass
class Corporation:
    """The corporation the chosen character is in, plus that character's roles."""

    token: CharacterToken
    corporation_id: int
    corporation_name: str
    ticker: str
    public: dict[str, Any]
    roles: frozenset[str]
    roles_at_hq: frozenset[str]
    roles_at_base: frozenset[str]
    roles_at_other: frozenset[str]

    @property
    def character_id(self) -> int:
        return self.token.character_id

    @property
    def character_name(self) -> str:
        return self.token.character_name

    @property
    def is_npc(self) -> bool:
        return self.corporation_id < PLAYER_CORP_ID_FLOOR

    def has_role(self, *needed: str) -> bool:
        """True if the character has Director, or any of the named roles, everywhere.

        Location-specific grants (HQ / base / other) do not unlock ESI.
        """
        if "Director" in self.roles:
            return True
        return any(role in self.roles for role in needed)


@dataclass
class AppContext:
    settings: Settings
    http: httpx.AsyncClient
    store: Store
    sso: SsoClient
    esi: EsiClient
    resolver: Resolver
    guard: WriteGuard

    # -------------------------------------------------------- character lookup

    def resolve_character(self, spec: str | int | None = None) -> CharacterToken:
        """Pick a character by id, by name, or implicitly when only one is authorized."""
        tokens = self.sso.store.all()
        if not tokens:
            raise CharacterNotFound(
                "No characters are authorized yet. Call eve_login_url and open the link "
                "in a browser to authorize one."
            )
        if spec is None or spec == "":
            if len(tokens) == 1:
                return tokens[0]
            names = ", ".join(f"{t.character_name} ({t.character_id})" for t in tokens)
            raise CharacterNotFound(
                f"Several characters are authorized, so 'character' is required. "
                f"Available: {names}"
            )
        if isinstance(spec, int) or (isinstance(spec, str) and spec.isdigit()):
            token = self.sso.store.get(int(spec))
            if token is None:
                raise CharacterNotFound(f"Character id {spec} is not authorized.")
            return token
        token = self.sso.store.find_by_name(str(spec))
        if token is None:
            names = ", ".join(t.character_name for t in tokens)
            raise CharacterNotFound(f"No authorized character matches {spec!r}. Have: {names}")
        return token

    def require_scope(self, token: CharacterToken, scope: str, what: str) -> None:
        if scope not in token.scopes:
            extra = ""
            if scope in CORP_READ_SCOPES:
                extra = (
                    " That is a corporation scope: set EVE_CORP_SCOPES=true, add the "
                    "matching permissions on the EVE developer application, restart, "
                    "and re-authorize this character with eve_auth_login_url."
                )
            raise AuthError(
                f"{token.character_name} was not authorized with '{scope}', which is "
                f"required to read {what}. Re-run the login for this character.{extra}"
            )

    async def resolve_corporation(self, spec: str | int | None = None) -> Corporation:
        """Character's player or NPC corporation, plus the roles ESI will honour."""
        token = self.resolve_character(spec)
        sheet = await self.esi.get(f"/characters/{token.character_id}")
        corp_id = (sheet.data or {}).get("corporation_id")
        if not corp_id:
            raise AuthError(
                f"{token.character_name} has no corporation_id from ESI. "
                "Try again shortly."
            )

        public_result = await self.esi.get(f"/corporations/{corp_id}")
        public = public_result.data or {}
        roles: set[str] = set()
        roles_at_hq: set[str] = set()
        roles_at_base: set[str] = set()
        roles_at_other: set[str] = set()
        if "esi-characters.read_corporation_roles.v1" in token.scopes:
            try:
                granted = await self.esi.get(
                    f"/characters/{token.character_id}/roles",
                    character_id=token.character_id,
                )
                payload = granted.data or {}
                roles = set(payload.get("roles") or [])
                roles_at_hq = set(payload.get("roles_at_hq") or [])
                roles_at_base = set(payload.get("roles_at_base") or [])
                roles_at_other = set(payload.get("roles_at_other") or [])
            except EsiError as exc:
                log.info("could not read corporation roles for %s: %s", token.character_name, exc)

        return Corporation(
            token=token,
            corporation_id=int(corp_id),
            corporation_name=public.get("name") or f"Corporation #{corp_id}",
            ticker=public.get("ticker") or "",
            public=public,
            roles=frozenset(roles),
            roles_at_hq=frozenset(roles_at_hq),
            roles_at_base=frozenset(roles_at_base),
            roles_at_other=frozenset(roles_at_other),
        )

    def require_player_corp(self, corp: Corporation) -> None:
        if corp.is_npc:
            raise AuthError(
                f"{corp.character_name} is in the NPC corporation "
                f"{corp.corporation_name} [{corp.ticker}] (#{corp.corporation_id}). "
                "ESI corporation hangars, wallets and jobs only exist for "
                "player-created corporations. There is nothing for eve_corp_* to read."
            )

    def require_corp_role(self, corp: Corporation, needed: tuple[str, ...], what: str) -> None:
        """Fail closed before hitting ESI — a 403 burns the shared error budget."""
        if not needed or corp.has_role(*needed):
            return
        have = ", ".join(sorted(corp.roles)) or "none"
        need = " or ".join(needed)
        raise AuthError(
            f"{corp.character_name} has no {need} role (nor Director) in "
            f"{corp.corporation_name}, which ESI requires to read {what}. "
            f"Roles granted everywhere: {have}. Location-specific roles "
            f"(HQ/base/other) do not unlock these endpoints. "
            "eve_corp_overview lists this character's roles."
        )


async def build_context(settings: Settings) -> AppContext:
    http = httpx.AsyncClient(
        timeout=httpx.Timeout(settings.request_timeout),
        follow_redirects=True,
        headers={"User-Agent": settings.user_agent},
        limits=httpx.Limits(max_connections=settings.max_concurrency * 2),
    )
    store = Store(settings.cache_file)
    purged = await store.purge_expired()
    if purged:
        log.info("purged %d stale cache rows", purged)
    sso = SsoClient(settings, http)
    esi = EsiClient(settings, http, store, sso)
    resolver = Resolver(esi, store)
    guard = WriteGuard(settings)
    return AppContext(
        settings=settings,
        http=http,
        store=store,
        sso=sso,
        esi=esi,
        resolver=resolver,
        guard=guard,
    )


async def close_context(ctx: AppContext) -> None:
    await ctx.http.aclose()
    await ctx.store.close()
