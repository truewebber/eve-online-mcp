"""ESI HTTP client: caching, error-limit backoff, pagination.

Everything CCP's best-practices page asks for lives here — identifying
User-Agent, pinned compatibility date, ``expires``/ETag caching, and a hard
stop when the error limit runs low.
"""
from __future__ import annotations

import asyncio
import email.utils
import hashlib
import json
import logging
import random
import time
from dataclasses import dataclass
from typing import Any, Literal, Mapping

import httpx

from .auth import SsoClient
from .cache import Store
from .config import ESI_BASE, Settings

log = logging.getLogger("eve_mcp.esi")

#: Stop issuing requests when fewer than this many errors remain in the window.
_ERROR_FLOOR = 15
#: Cap for how long we will sleep waiting for an error window to reset.
_MAX_BACKOFF = 60.0
#: Refuse to trust an Expires header further out than this. A server-relative TTL
#: cannot be sanity-checked against the local clock, so it needs its own ceiling.
_MAX_CACHE_TTL = 86400.0

Method = Literal["GET", "POST", "PUT", "DELETE"]


class EsiError(RuntimeError):
    """An ESI call failed in a way the caller should surface to the user."""

    def __init__(self, message: str, status: int | None = None, body: Any = None) -> None:
        super().__init__(message)
        self.status = status
        self.body = body


@dataclass
class EsiResult:
    data: Any
    from_cache: bool
    age_seconds: float
    expires_at: float
    pages: int | None = None
    #: True when a page cap stopped the walk with more data still behind it.
    truncated: bool = False

    @property
    def stale_note(self) -> str:
        if self.age_seconds < 60:
            return f"{int(self.age_seconds)}s old"
        if self.age_seconds < 3600:
            return f"{int(self.age_seconds // 60)}m old"
        return f"{self.age_seconds / 3600:.1f}h old"


class EsiClient:
    def __init__(self, settings: Settings, http: httpx.AsyncClient, store: Store, sso: SsoClient):
        self._settings = settings
        self._http = http
        self._store = store
        self._sso = sso
        self._sem = asyncio.Semaphore(settings.max_concurrency)
        self._error_limit_remain = 100
        self._error_limit_reset_at = 0.0
        self._error_lock = asyncio.Lock()

    # ------------------------------------------------------------- public API

    async def get(
        self,
        path: str,
        *,
        character_id: int | None = None,
        params: Mapping[str, Any] | None = None,
        cache_ttl: float | None = None,
    ) -> EsiResult:
        """GET a single page, served from cache whenever it is still fresh."""
        return await self._cached_get(path, character_id, params or {}, cache_ttl)

    async def get_all_pages(
        self,
        path: str,
        *,
        character_id: int | None = None,
        params: Mapping[str, Any] | None = None,
        max_pages: int = 40,
    ) -> EsiResult:
        """GET every page of a paginated endpoint and concatenate the results."""
        params = dict(params or {})
        first = await self._cached_get(path, character_id, {**params, "page": 1}, None)
        total_pages = first.pages or 1
        if total_pages <= 1 or not isinstance(first.data, list):
            return first

        capped = min(total_pages, max_pages)
        if capped < total_pages:
            log.warning("%s has %d pages, fetching first %d", path, total_pages, capped)

        rest = await asyncio.gather(
            *(
                self._cached_get(path, character_id, {**params, "page": page}, None)
                for page in range(2, capped + 1)
            )
        )
        data = list(first.data)
        oldest = first.age_seconds
        for result in rest:
            if isinstance(result.data, list):
                data.extend(result.data)
            oldest = max(oldest, result.age_seconds)
        return EsiResult(
            data=data,
            from_cache=first.from_cache and all(r.from_cache for r in rest),
            age_seconds=oldest,
            expires_at=first.expires_at,
            pages=total_pages,
            truncated=capped < total_pages,
        )

    async def get_cursor_pages(
        self,
        path: str,
        *,
        character_id: int | None = None,
        params: Mapping[str, Any] | None = None,
        cursor_param: str,
        cursor_key: str,
        batch_size: int,
        max_pages: int = 4,
    ) -> EsiResult:
        """GET a backwards-cursor endpoint, walking until it runs out.

        ESI paginates two ways. ``get_all_pages`` handles the ``page`` /
        ``X-Pages`` kind. This handles the other kind — ``/wallet/transactions``
        (``from_id``), ``/mail`` (``last_mail_id``) — where every response is a
        fixed-size batch, newest first, and the next request asks for ids
        *strictly lower* than the smallest one just seen.

        Pages must be fetched serially: page N+1's cursor is unknown until page
        N returns, so ``max_pages`` costs wall-clock as well as requests.

        ``batch_size`` is the endpoint's documented page size and is only an
        optimisation — it lets a short final page end the walk without spending
        a request to discover an empty one.

        On the result, ``pages`` is how many requests were made; a cursor
        endpoint never reveals how many exist. ``truncated`` is True when
        ``max_pages`` stopped a walk that still had a full page behind it.
        """
        base = dict(params or {})
        cursor: Any = base.get(cursor_param)
        data: list[Any] = []
        seen: set[Any] = set()
        oldest = 0.0
        expires_at = 0.0
        all_cached = True
        fetched = 0
        truncated = False
        limit = max(1, max_pages)
        batch_size = max(1, batch_size)

        for index in range(limit):
            result = await self._cached_get(
                path, character_id, {**base, cursor_param: cursor}, None
            )
            fetched += 1
            if fetched == 1:
                expires_at = result.expires_at
            all_cached = all_cached and result.from_cache
            oldest = max(oldest, result.age_seconds)

            rows = result.data if isinstance(result.data, list) else []
            if not rows:
                break
            # Tolerate ESI raising its page size without a code change here.
            batch_size = max(batch_size, len(rows))

            next_cursor = None
            for row in rows:
                marker = row.get(cursor_key) if isinstance(row, dict) else None
                if marker is not None:
                    # Never double-count: callers sum ISK over these rows.
                    if marker in seen:
                        continue
                    seen.add(marker)
                    next_cursor = marker if next_cursor is None else min(next_cursor, marker)
                data.append(row)

            if len(rows) < batch_size:
                break
            if index == limit - 1:
                truncated = True
                break
            if next_cursor is None or (cursor is not None and next_cursor >= cursor):
                log.warning("%s: %s did not advance past %s; stopping", path, cursor_param, cursor)
                break
            cursor = next_cursor

        return EsiResult(
            data=data,
            from_cache=all_cached,
            age_seconds=oldest,
            expires_at=expires_at,
            pages=fetched,
            truncated=truncated,
        )

    async def post(
        self,
        path: str,
        *,
        character_id: int | None = None,
        params: Mapping[str, Any] | None = None,
        json_body: Any = None,
    ) -> Any:
        return await self._write("POST", path, character_id, params, json_body)

    async def put(
        self,
        path: str,
        *,
        character_id: int | None = None,
        params: Mapping[str, Any] | None = None,
        json_body: Any = None,
    ) -> Any:
        return await self._write("PUT", path, character_id, params, json_body)

    async def delete(
        self,
        path: str,
        *,
        character_id: int | None = None,
        params: Mapping[str, Any] | None = None,
        json_body: Any = None,
    ) -> Any:
        return await self._write("DELETE", path, character_id, params, json_body)

    # -------------------------------------------------------------- internals

    def _cache_key(self, path: str, character_id: int | None, params: Mapping[str, Any]) -> str:
        canonical = json.dumps(
            {"p": path, "c": character_id, "q": _normalise_params(params), "d": self._settings.compat_date},
            sort_keys=True,
        )
        return hashlib.sha256(canonical.encode()).hexdigest()

    async def _headers(self, character_id: int | None) -> dict[str, str]:
        headers = {
            "User-Agent": self._settings.user_agent,
            "X-Compatibility-Date": self._settings.compat_date,
            "Accept": "application/json",
        }
        if character_id is not None:
            token = await self._sso.access_token(character_id)
            headers["Authorization"] = f"Bearer {token.access_token}"
        return headers

    async def _cached_get(
        self,
        path: str,
        character_id: int | None,
        params: Mapping[str, Any],
        cache_ttl: float | None,
    ) -> EsiResult:
        key = self._cache_key(path, character_id, params)
        cached = await self._store.get_http(key)
        if cached is not None and cached.fresh:
            return EsiResult(
                data=cached.body,
                from_cache=True,
                age_seconds=cached.age_seconds,
                expires_at=cached.expires_at,
                pages=cached.pages,
            )

        headers = await self._headers(character_id)
        if cached is not None and cached.etag:
            headers["If-None-Match"] = cached.etag

        resp = await self._request("GET", path, params=params, headers=headers)

        if resp.status_code == 304 and cached is not None:
            expires_at = _expires_at(resp, cache_ttl)
            await self._store.touch_http(key, expires_at)
            return EsiResult(
                data=cached.body,
                from_cache=True,
                age_seconds=0.0,
                expires_at=expires_at,
                pages=cached.pages,
            )

        if resp.status_code >= 400:
            if cached is not None and 500 <= resp.status_code < 600:
                log.warning("%s returned %s, serving stale cache", path, resp.status_code)
                return EsiResult(
                    data=cached.body,
                    from_cache=True,
                    age_seconds=cached.age_seconds,
                    expires_at=cached.expires_at,
                    pages=cached.pages,
                )
            raise _http_error(resp, path)

        body = _decode(resp)
        pages = _int_header(resp, "x-pages")
        expires_at = _expires_at(resp, cache_ttl)
        await self._store.put_http(key, body, resp.headers.get("etag"), expires_at, pages)
        return EsiResult(
            data=body, from_cache=False, age_seconds=0.0, expires_at=expires_at, pages=pages
        )

    async def _write(
        self,
        method: Method,
        path: str,
        character_id: int | None,
        params: Mapping[str, Any] | None,
        json_body: Any,
    ) -> Any:
        headers = await self._headers(character_id)
        if json_body is not None:
            headers["Content-Type"] = "application/json"
        resp = await self._request(
            method, path, params=params or {}, headers=headers, json_body=json_body
        )
        if resp.status_code >= 400:
            raise _http_error(resp, path)
        return _decode(resp)

    async def _request(
        self,
        method: Method,
        path: str,
        *,
        params: Mapping[str, Any],
        headers: Mapping[str, str],
        json_body: Any = None,
        attempt: int = 0,
    ) -> httpx.Response:
        await self._await_error_budget()
        url = f"{ESI_BASE}{path}"
        async with self._sem:
            try:
                resp = await self._http.request(
                    method,
                    url,
                    params=_normalise_params(params),
                    headers=dict(headers),
                    json=json_body,
                )
            except httpx.HTTPError as exc:
                if attempt < 2 and _safe_to_retry(method, exc):
                    await asyncio.sleep(_backoff(attempt))
                    return await self._request(
                        method, path, params=params, headers=headers,
                        json_body=json_body, attempt=attempt + 1,
                    )
                if method != "GET":
                    raise EsiError(
                        f"Network error calling {path}: {exc}. The request may or may "
                        "not have reached EVE — check the current state with the "
                        "matching read tool before trying again, because repeating it "
                        "could apply the change twice."
                    ) from exc
                raise EsiError(f"Network error calling {path}: {exc}") from exc

        self._note_error_headers(resp)

        retryable = resp.status_code in (500, 502, 503, 504)
        if resp.status_code == 420 or (resp.status_code == 429 and attempt < 3):
            wait = min(_MAX_BACKOFF, _retry_after(resp))
            log.warning("%s throttled (%s); sleeping %.1fs", path, resp.status_code, wait)
            await asyncio.sleep(wait)
            if attempt < 3:
                return await self._request(
                    method, path, params=params, headers=headers,
                    json_body=json_body, attempt=attempt + 1,
                )
        elif retryable and attempt < 2 and method == "GET":
            await asyncio.sleep(_backoff(attempt))
            return await self._request(
                method, path, params=params, headers=headers,
                json_body=json_body, attempt=attempt + 1,
            )
        return resp

    def _note_error_headers(self, resp: httpx.Response) -> None:
        remain = _int_header(resp, "x-esi-error-limit-remain")
        reset = _int_header(resp, "x-esi-error-limit-reset")
        if remain is None:
            return
        self._error_limit_remain = remain
        self._error_limit_reset_at = time.time() + (reset or 0)
        if remain < _ERROR_FLOOR:
            log.warning("ESI error budget low: %d remaining, resets in %ss", remain, reset)

    async def _await_error_budget(self) -> None:
        """Hold requests when the shared error budget is nearly spent."""
        async with self._error_lock:
            if self._error_limit_remain >= _ERROR_FLOOR:
                return
            wait = min(_MAX_BACKOFF, max(0.0, self._error_limit_reset_at - time.time()) + 1)
            if wait <= 0:
                self._error_limit_remain = 100
                return
            log.warning("pausing %.1fs to let the ESI error limit reset", wait)
            await asyncio.sleep(wait)
            self._error_limit_remain = 100


def _normalise_params(params: Mapping[str, Any]) -> dict[str, Any]:
    out: dict[str, Any] = {}
    for key, value in params.items():
        if value is None:
            continue
        if isinstance(value, bool):
            out[key] = "true" if value else "false"
        elif isinstance(value, (list, tuple, set)):
            out[key] = ",".join(str(v) for v in value)
        else:
            out[key] = value
    return out


def _decode(resp: httpx.Response) -> Any:
    if resp.status_code == 204 or not resp.content:
        return None
    try:
        return resp.json()
    except json.JSONDecodeError:
        return resp.text


def _int_header(resp: httpx.Response, name: str) -> int | None:
    raw = resp.headers.get(name)
    if raw is None:
        return None
    try:
        return int(raw)
    except ValueError:
        return None


def _expires_at(resp: httpx.Response, fallback_ttl: float | None) -> float:
    """Absolute local time at which this response goes stale.

    The TTL comes from ``Expires - Date``, *both taken from the response*, and
    is only then anchored to the local clock. That is CCP's guidance and the
    only way a skewed container clock cannot break caching: comparing
    ``Expires`` against ``time.time()`` means a fast clock throws away cache
    that is still fresh — hammering ESI — and a slow clock serves stale data.
    """
    ttl = _server_ttl(resp)
    if ttl is None:
        ttl = _max_age(resp)
    if ttl is None:
        ttl = fallback_ttl if fallback_ttl is not None else 60.0
    # A non-positive TTL means ESI has declared the body stale already; clamp to
    # zero rather than falling back, so it is not cached past its own window.
    return time.time() + max(0.0, min(ttl, _MAX_CACHE_TTL))


def _header_date(resp: httpx.Response, name: str) -> float | None:
    raw = resp.headers.get(name)
    if not raw:
        return None
    try:
        parsed = email.utils.parsedate_to_datetime(raw)
    except (TypeError, ValueError):
        return None
    return parsed.timestamp() if parsed is not None else None


def _server_ttl(resp: httpx.Response) -> float | None:
    """``Expires - Date``: freshness measured entirely on ESI's own clock."""
    expires = _header_date(resp, "expires")
    if expires is None:
        return None
    served = _header_date(resp, "date")
    if served is None:
        # Nothing to subtract against; eat the skew rather than discard Expires.
        return expires - time.time()
    return expires - served


def _max_age(resp: httpx.Response) -> float | None:
    for part in resp.headers.get("cache-control", "").split(","):
        part = part.strip()
        if part.startswith("max-age="):
            try:
                return float(part.split("=", 1)[1])
            except ValueError:
                return None
    return None


def _retry_after(resp: httpx.Response, default: float = 10.0) -> float:
    """Seconds to wait, from a Retry-After that may be a count or an HTTP date."""
    raw = resp.headers.get("retry-after")
    if not raw:
        return default
    try:
        # Zero or negative would make the throttle sleep a no-op, defeating the
        # point of honouring Retry-After at all.
        seconds = float(raw)
        return seconds if seconds > 0 else default
    except ValueError:
        pass
    when = _header_date(resp, "retry-after")
    if when is None:
        return default
    served = _header_date(resp, "date")
    seconds = when - (served if served is not None else time.time())
    return seconds if seconds > 0 else default


#: Transport failures that prove the request never reached ESI, so replaying it
#: cannot duplicate a side effect. A ReadTimeout on a POST proves nothing: the
#: mail may already have been sent.
_NEVER_SENT = (httpx.ConnectError, httpx.ConnectTimeout, httpx.PoolTimeout)


def _safe_to_retry(method: Method, exc: httpx.HTTPError) -> bool:
    return method == "GET" or isinstance(exc, _NEVER_SENT)


def _backoff(attempt: int) -> float:
    return min(8.0, (2**attempt)) * (0.5 + random.random() / 2)


def _http_error(resp: httpx.Response, path: str) -> EsiError:
    body = _decode(resp)
    detail = body.get("error") if isinstance(body, dict) else None
    detail = detail or (json.dumps(body)[:300] if body else resp.text[:300])
    hints = {
        401: "the access token was rejected — the character may need to log in again",
        403: "missing scope or in-game role for this endpoint",
        404: "not found (wrong id, or the character has no such data)",
        420: "ESI error limit exceeded — slow down",
    }
    hint = hints.get(resp.status_code)
    message = f"ESI {resp.status_code} on {path}: {detail}"
    if hint:
        message += f" ({hint})"
    return EsiError(message, status=resp.status_code, body=body)
