"""Shared application context handed to every tool module."""
from __future__ import annotations

import logging
from dataclasses import dataclass


import httpx

from .auth import AuthError, CharacterToken, SsoClient
from .cache import Store
from .config import Settings
from .esi import EsiClient
from .names import Resolver
from .safety import WriteGuard

log = logging.getLogger("eve_mcp")


class CharacterNotFound(RuntimeError):
    pass


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
            raise AuthError(
                f"{token.character_name} was not authorized with '{scope}', which is "
                f"required to read {what}. Re-run the login for this character."
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
