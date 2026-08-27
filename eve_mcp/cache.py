"""SQLite-backed HTTP cache and id->name store.

ESI bans clients that circumvent its caching, so every GET goes through here:
inside the ``expires`` window we never touch the network at all, and afterwards
we revalidate with ``If-None-Match`` instead of refetching the body.
"""
from __future__ import annotations

import asyncio
import json
import sqlite3
import time
from dataclasses import dataclass
from pathlib import Path
from typing import Any

_SCHEMA = """
CREATE TABLE IF NOT EXISTS http_cache (
    key        TEXT PRIMARY KEY,
    etag       TEXT,
    expires_at REAL NOT NULL,
    stored_at  REAL NOT NULL,
    pages      INTEGER,
    body       TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS http_cache_expires ON http_cache (expires_at);

CREATE TABLE IF NOT EXISTS names (
    id       INTEGER PRIMARY KEY,
    name     TEXT NOT NULL,
    category TEXT
);

CREATE TABLE IF NOT EXISTS blobs (
    key       TEXT PRIMARY KEY,
    stored_at REAL NOT NULL,
    value     TEXT NOT NULL
);
"""


@dataclass
class CachedResponse:
    body: Any
    etag: str | None
    expires_at: float
    stored_at: float
    pages: int | None

    @property
    def fresh(self) -> bool:
        return time.time() < self.expires_at

    @property
    def age_seconds(self) -> float:
        return max(0.0, time.time() - self.stored_at)


class Store:
    """Thin async wrapper over a single sqlite connection."""

    def __init__(self, path: Path) -> None:
        path.parent.mkdir(parents=True, exist_ok=True)
        self._conn = sqlite3.connect(path, check_same_thread=False)
        self._conn.row_factory = sqlite3.Row
        self._conn.execute("PRAGMA journal_mode=WAL")
        self._conn.execute("PRAGMA synchronous=NORMAL")
        self._conn.executescript(_SCHEMA)
        self._conn.commit()
        self._lock = asyncio.Lock()

    async def close(self) -> None:
        async with self._lock:
            self._conn.close()

    # ------------------------------------------------------------------ http

    async def get_http(self, key: str) -> CachedResponse | None:
        async with self._lock:
            row = self._conn.execute(
                "SELECT etag, expires_at, stored_at, pages, body FROM http_cache WHERE key = ?",
                (key,),
            ).fetchone()
        if row is None:
            return None
        try:
            body = json.loads(row["body"])
        except json.JSONDecodeError:
            return None
        return CachedResponse(
            body=body,
            etag=row["etag"],
            expires_at=row["expires_at"],
            stored_at=row["stored_at"],
            pages=row["pages"],
        )

    async def put_http(
        self,
        key: str,
        body: Any,
        etag: str | None,
        expires_at: float,
        pages: int | None = None,
    ) -> None:
        now = time.time()
        async with self._lock:
            self._conn.execute(
                "INSERT INTO http_cache (key, etag, expires_at, stored_at, pages, body) "
                "VALUES (?, ?, ?, ?, ?, ?) "
                "ON CONFLICT(key) DO UPDATE SET etag=excluded.etag, "
                "expires_at=excluded.expires_at, stored_at=excluded.stored_at, "
                "pages=excluded.pages, body=excluded.body",
                (key, etag, expires_at, now, pages, json.dumps(body)),
            )
            self._conn.commit()

    async def touch_http(self, key: str, expires_at: float) -> None:
        """Extend the freshness window after a 304 Not Modified."""
        async with self._lock:
            self._conn.execute(
                "UPDATE http_cache SET expires_at = ?, stored_at = ? WHERE key = ?",
                (expires_at, time.time(), key),
            )
            self._conn.commit()

    async def purge_expired(self, older_than_days: float = 30.0) -> int:
        cutoff = time.time() - older_than_days * 86400
        async with self._lock:
            cur = self._conn.execute("DELETE FROM http_cache WHERE stored_at < ?", (cutoff,))
            self._conn.commit()
            return cur.rowcount

    # ----------------------------------------------------------------- names

    async def get_names(self, ids: list[int]) -> dict[int, dict[str, Any]]:
        if not ids:
            return {}
        out: dict[int, dict[str, Any]] = {}
        async with self._lock:
            for chunk_start in range(0, len(ids), 500):
                chunk = ids[chunk_start : chunk_start + 500]
                placeholders = ",".join("?" * len(chunk))
                rows = self._conn.execute(
                    f"SELECT id, name, category FROM names WHERE id IN ({placeholders})",
                    chunk,
                ).fetchall()
                for row in rows:
                    out[row["id"]] = {"name": row["name"], "category": row["category"]}
        return out

    async def put_names(self, entries: list[tuple[int, str, str | None]]) -> None:
        if not entries:
            return
        async with self._lock:
            self._conn.executemany(
                "INSERT INTO names (id, name, category) VALUES (?, ?, ?) "
                "ON CONFLICT(id) DO UPDATE SET name=excluded.name, category=excluded.category",
                entries,
            )
            self._conn.commit()

    # ----------------------------------------------------------------- blobs

    async def get_blob(self, key: str, max_age: float | None = None) -> Any | None:
        async with self._lock:
            row = self._conn.execute(
                "SELECT stored_at, value FROM blobs WHERE key = ?", (key,)
            ).fetchone()
        if row is None:
            return None
        if max_age is not None and time.time() - row["stored_at"] > max_age:
            return None
        try:
            return json.loads(row["value"])
        except json.JSONDecodeError:
            return None

    async def put_blob(self, key: str, value: Any) -> None:
        async with self._lock:
            self._conn.execute(
                "INSERT INTO blobs (key, stored_at, value) VALUES (?, ?, ?) "
                "ON CONFLICT(key) DO UPDATE SET stored_at=excluded.stored_at, value=excluded.value",
                (key, time.time(), json.dumps(value)),
            )
            self._conn.commit()
