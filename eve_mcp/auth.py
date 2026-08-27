"""EVE SSO: PKCE authorization-code flow, token storage and refresh."""
from __future__ import annotations

import asyncio
import base64
import hashlib
import json
import logging
import os
import secrets
import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any
from urllib.parse import urlencode

import httpx
import jwt
from jwt import PyJWKClient

from .config import (
    AUTHORIZE_URL,
    JWKS_URL,
    REVOKE_URL,
    Settings,
    TOKEN_AUDIENCE,
    TOKEN_ISSUERS,
    TOKEN_URL,
)

log = logging.getLogger("eve_mcp.auth")

#: Refresh this many seconds before the access token actually expires.
_REFRESH_MARGIN = 60.0
#: Pending logins are dropped after this long.
_LOGIN_TTL = 900.0


class AuthError(RuntimeError):
    pass


def _b64url(raw: bytes) -> str:
    return base64.urlsafe_b64encode(raw).rstrip(b"=").decode("ascii")


@dataclass
class PendingLogin:
    state: str
    verifier: str
    scopes: list[str]
    created_at: float = field(default_factory=time.time)


@dataclass
class CharacterToken:
    character_id: int
    character_name: str
    refresh_token: str
    scopes: list[str]
    owner_hash: str = ""
    access_token: str = ""
    access_expires_at: float = 0.0
    added_at: float = field(default_factory=time.time)

    def to_json(self) -> dict[str, Any]:
        return {
            "character_id": self.character_id,
            "character_name": self.character_name,
            "refresh_token": self.refresh_token,
            "scopes": self.scopes,
            "owner_hash": self.owner_hash,
            "added_at": self.added_at,
        }

    @classmethod
    def from_json(cls, raw: dict[str, Any]) -> "CharacterToken":
        return cls(
            character_id=int(raw["character_id"]),
            character_name=raw.get("character_name", ""),
            refresh_token=raw["refresh_token"],
            scopes=list(raw.get("scopes", [])),
            owner_hash=raw.get("owner_hash", ""),
            added_at=float(raw.get("added_at", time.time())),
        )


class TokenStore:
    """Persists refresh tokens to a JSON file inside the mounted data volume."""

    def __init__(self, path: Path) -> None:
        self._path = path
        self._tokens: dict[int, CharacterToken] = {}
        self._lock = asyncio.Lock()
        self._load()

    def _load(self) -> None:
        if not self._path.exists():
            return
        try:
            raw = json.loads(self._path.read_text())
        except (json.JSONDecodeError, OSError) as exc:
            log.error("could not read token file %s: %s", self._path, exc)
            return
        for entry in raw.get("characters", []):
            try:
                token = CharacterToken.from_json(entry)
            except (KeyError, ValueError) as exc:
                log.warning("skipping malformed token entry: %s", exc)
                continue
            self._tokens[token.character_id] = token

    def _flush(self) -> None:
        payload = {
            "version": 1,
            "characters": [t.to_json() for t in self._tokens.values()],
        }
        tmp = self._path.with_suffix(".tmp")
        tmp.write_text(json.dumps(payload, indent=2))
        os.chmod(tmp, 0o600)
        tmp.replace(self._path)

    async def upsert(self, token: CharacterToken) -> None:
        async with self._lock:
            existing = self._tokens.get(token.character_id)
            if existing:
                # Keep a live access token around across re-logins.
                token.access_token = token.access_token or existing.access_token
                token.access_expires_at = token.access_expires_at or existing.access_expires_at
                token.added_at = existing.added_at
            self._tokens[token.character_id] = token
            self._flush()

    async def remove(self, character_id: int) -> bool:
        async with self._lock:
            if character_id not in self._tokens:
                return False
            del self._tokens[character_id]
            self._flush()
            return True

    async def save(self) -> None:
        async with self._lock:
            self._flush()

    def get(self, character_id: int) -> CharacterToken | None:
        return self._tokens.get(character_id)

    def all(self) -> list[CharacterToken]:
        return sorted(self._tokens.values(), key=lambda t: t.character_name.lower())

    def find_by_name(self, name: str) -> CharacterToken | None:
        lowered = name.strip().lower()
        for token in self._tokens.values():
            if token.character_name.lower() == lowered:
                return token
        for token in self._tokens.values():
            if lowered and lowered in token.character_name.lower():
                return token
        return None


class SsoClient:
    """Drives the PKCE flow and hands out fresh access tokens."""

    def __init__(self, settings: Settings, http: httpx.AsyncClient) -> None:
        self._settings = settings
        self._http = http
        self.store = TokenStore(settings.token_file)
        self._pending: dict[str, PendingLogin] = {}
        self._refresh_locks: dict[int, asyncio.Lock] = {}
        self._jwks: PyJWKClient | None = None

    # ------------------------------------------------------------ login flow

    def build_login(self, scopes: list[str] | None = None) -> tuple[str, str]:
        if not self._settings.client_id:
            raise AuthError(
                "EVE_CLIENT_ID is not set. Register an application at "
                "https://developers.eveonline.com/applications and pass its Client ID."
            )
        self._expire_pending()
        scopes = scopes if scopes is not None else self._settings.requested_scopes()
        verifier = _b64url(secrets.token_bytes(32))
        challenge = _b64url(hashlib.sha256(verifier.encode("ascii")).digest())
        state = _b64url(secrets.token_bytes(16))
        self._pending[state] = PendingLogin(state=state, verifier=verifier, scopes=scopes)
        query = {
            "response_type": "code",
            "redirect_uri": self._settings.callback_url,
            "client_id": self._settings.client_id,
            "scope": " ".join(scopes),
            "state": state,
            "code_challenge": challenge,
            "code_challenge_method": "S256",
        }
        return f"{AUTHORIZE_URL}?{urlencode(query)}", state

    def _expire_pending(self) -> None:
        cutoff = time.time() - _LOGIN_TTL
        for state in [s for s, p in self._pending.items() if p.created_at < cutoff]:
            self._pending.pop(state, None)

    async def complete_login(self, code: str, state: str) -> CharacterToken:
        pending = self._pending.pop(state, None)
        if pending is None:
            raise AuthError("Unknown or expired login state — start the login again.")
        data = {
            "grant_type": "authorization_code",
            "code": code,
            "client_id": self._settings.client_id,
            "code_verifier": pending.verifier,
            "redirect_uri": self._settings.callback_url,
        }
        payload = await self._token_request(data)
        token = await self._token_from_payload(payload)
        await self.store.upsert(token)
        log.info("authorized %s (%s) with %d scopes",
                 token.character_name, token.character_id, len(token.scopes))
        return token

    # --------------------------------------------------------------- refresh

    async def access_token(self, character_id: int) -> CharacterToken:
        token = self.store.get(character_id)
        if token is None:
            raise AuthError(
                f"Character {character_id} is not authorized. Run the login flow first."
            )
        if token.access_token and time.time() < token.access_expires_at - _REFRESH_MARGIN:
            return token

        lock = self._refresh_locks.setdefault(character_id, asyncio.Lock())
        async with lock:
            # Another waiter may have refreshed while we queued.
            token = self.store.get(character_id)
            if token is None:
                raise AuthError(f"Character {character_id} was removed during refresh.")
            if token.access_token and time.time() < token.access_expires_at - _REFRESH_MARGIN:
                return token
            return await self._refresh(token)

    async def _refresh(self, token: CharacterToken) -> CharacterToken:
        data = {
            "grant_type": "refresh_token",
            "refresh_token": token.refresh_token,
            "client_id": self._settings.client_id,
        }
        try:
            payload = await self._token_request(data)
        except AuthError as exc:
            if "invalid_grant" in str(exc):
                await self.store.remove(token.character_id)
                raise AuthError(
                    f"Refresh token for {token.character_name} was revoked or expired. "
                    "Log this character in again."
                ) from exc
            raise
        refreshed = await self._token_from_payload(payload, fallback=token)
        await self.store.upsert(refreshed)
        return refreshed

    async def revoke(self, character_id: int) -> None:
        token = self.store.get(character_id)
        if token is None:
            return
        data = {"token_type_hint": "refresh_token", "token": token.refresh_token,
                "client_id": self._settings.client_id}
        try:
            await self._http.post(REVOKE_URL, data=data, auth=self._basic_auth())
        except httpx.HTTPError as exc:  # revocation is best-effort
            log.warning("revoke call failed for %s: %s", character_id, exc)
        await self.store.remove(character_id)

    # --------------------------------------------------------------- helpers

    def _basic_auth(self) -> tuple[str, str] | None:
        if self._settings.client_secret:
            return (self._settings.client_id, self._settings.client_secret)
        return None

    async def _token_request(self, data: dict[str, str]) -> dict[str, Any]:
        headers = {
            "Content-Type": "application/x-www-form-urlencoded",
            "Host": "login.eveonline.com",
            "User-Agent": self._settings.user_agent,
        }
        auth = self._basic_auth()
        if auth is not None:
            # With a confidential client the id travels in the Basic header only.
            data = {k: v for k, v in data.items() if k != "client_id"}
        resp = await self._http.post(TOKEN_URL, data=data, headers=headers, auth=auth)
        if resp.status_code >= 400:
            raise AuthError(
                f"SSO token request failed ({resp.status_code}): {_sso_detail(resp)}"
            )
        return resp.json()

    def _jwks_client(self) -> PyJWKClient:
        if self._jwks is None:
            self._jwks = PyJWKClient(JWKS_URL, cache_keys=True, lifespan=3600)
        return self._jwks

    def _decode(self, access_token: str) -> dict[str, Any]:
        """Verify the access token signature and return its claims.

        Fetching the signing key can fail for reasons that say nothing about the
        token — JWKS unreachable, a cold cache during an outage — and a login
        should survive those. A signature or claim that does not check out is a
        different matter: catching both in one block, as this used to, meant a
        forged token was accepted with only a warning in the log.
        """
        try:
            key = self._jwks_client().get_signing_key_from_jwt(access_token)
        except Exception as exc:  # noqa: BLE001 - key retrieval only, never the token
            log.warning("JWKS unavailable (%s); accepting SSO token on TLS alone", exc)
            try:
                claims = jwt.decode(access_token, options={"verify_signature": False})
            except jwt.InvalidTokenError as decode_exc:
                raise AuthError(f"Malformed token from the SSO: {decode_exc}") from decode_exc
        else:
            try:
                claims = jwt.decode(
                    access_token,
                    key.key,
                    algorithms=["RS256", "ES256"],
                    audience=TOKEN_AUDIENCE,
                    leeway=30,  # tolerate small clock drift, nothing more
                    options={"verify_iss": False},
                )
            except jwt.InvalidTokenError as exc:
                raise AuthError(
                    f"The SSO token failed verification ({exc}). Nothing was stored. "
                    "If this persists, CCP may have rotated their signing keys — "
                    "check for an updated version of this server."
                ) from exc
        issuer = claims.get("iss", "")
        if issuer not in TOKEN_ISSUERS:
            raise AuthError(f"Unexpected token issuer: {issuer!r}")
        return claims

    async def _token_from_payload(
        self, payload: dict[str, Any], fallback: CharacterToken | None = None
    ) -> CharacterToken:
        access_token = payload.get("access_token")
        refresh_token = payload.get("refresh_token") or (
            fallback.refresh_token if fallback else None
        )
        if not access_token or not refresh_token:
            raise AuthError("SSO response was missing access_token or refresh_token.")

        claims = self._decode(access_token)
        subject = str(claims.get("sub", ""))
        if not subject.startswith("CHARACTER:EVE:"):
            raise AuthError(f"Unexpected token subject: {subject!r}")
        character_id = int(subject.rsplit(":", 1)[1])

        raw_scopes = claims.get("scp", [])
        scopes = [raw_scopes] if isinstance(raw_scopes, str) else list(raw_scopes)

        expires_in = float(payload.get("expires_in", 1200))
        return CharacterToken(
            character_id=character_id,
            character_name=claims.get("name") or (fallback.character_name if fallback else ""),
            refresh_token=refresh_token,
            scopes=scopes,
            owner_hash=claims.get("owner", "") or (fallback.owner_hash if fallback else ""),
            access_token=access_token,
            access_expires_at=time.time() + expires_in,
        )


def _sso_detail(resp: httpx.Response) -> str:
    """The SSO answers some failures with a full HTML page; keep the useful bit."""
    content_type = resp.headers.get("content-type", "")
    if "json" in content_type:
        try:
            payload = resp.json()
        except ValueError:
            payload = {}
        error = payload.get("error") or payload.get("message") or ""
        description = payload.get("error_description", "")
        detail = " - ".join(p for p in (error, description) if p)
        if detail:
            return detail
    if "html" in content_type:
        return (
            "the SSO rejected the request (bad client_id, wrong callback URL, or a "
            "refresh token that is no longer valid)"
        )
    return resp.text[:200]
