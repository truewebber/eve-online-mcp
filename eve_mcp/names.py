"""Turn ESI's bare numeric ids into names the LLM can actually reason about."""
from __future__ import annotations

import asyncio
import logging
import time
from collections import defaultdict
from dataclasses import dataclass
from typing import Any, Iterable, Sequence

from .cache import Store
from .config import JITA_4_4_STATION_ID, THE_FORGE_REGION_ID
from .esi import EsiClient, EsiError

log = logging.getLogger("eve_mcp.names")

_NAME_BATCH = 900
#: /universe/ids rejects duplicates and caps a request at 500 names.
_IDS_BATCH = 500
#: Location ids at or above this are player structures, which /universe/names
#: refuses to resolve; they need the authenticated structure endpoint instead.
#: Upwell structures are item ids and start at 1e12 — everything below that,
#: characters (2.1e9) very much included, belongs to /universe/names.
_STRUCTURE_ID_FLOOR = 1_000_000_000_000
#: Market prices change slowly enough that an hour-old snapshot is fine.
_PRICE_TTL = 3600.0

#: Categories POST /universe/ids answers with, mapped to the label used in
#: error strings and confirmation previews.
ID_CATEGORIES: dict[str, str] = {
    "agents": "agent",
    "alliances": "alliance",
    "characters": "character",
    "constellations": "constellation",
    "corporations": "corporation",
    "factions": "faction",
    "inventory_types": "item type",
    "regions": "region",
    "stations": "station",
    "systems": "solar system",
}


def _article(kind: str) -> str:
    return f"{'an' if kind[:1].lower() in 'aeiou' else 'a'} {kind}"


@dataclass(frozen=True)
class NameMatch:
    id: int
    name: str
    #: The /universe/ids key this came from, e.g. "inventory_types".
    category: str
    #: Human label for that category, e.g. "item type".
    kind: str


@dataclass(frozen=True)
class NameResolution:
    query: str
    chosen: NameMatch | None
    alternatives: tuple[NameMatch, ...] = ()

    @property
    def ambiguous(self) -> bool:
        return bool(self.alternatives)

    def describe(self) -> str:
        """'Rifter' is an item type (#587) and also a character (#187399875)."""
        if self.chosen is None:
            return f"{self.query!r} matched nothing"
        others = ", ".join(f"{_article(m.kind)} (#{m.id})" for m in self.alternatives)
        chosen = f"{_article(self.chosen.kind)} (#{self.chosen.id})"
        return f"{self.query!r} is {chosen} and also {others}"


class Resolver:
    def __init__(self, esi: EsiClient, store: Store) -> None:
        self._esi = esi
        self._store = store
        self._prices: dict[int, dict[str, float]] | None = None
        self._prices_at = 0.0
        self._price_lock = asyncio.Lock()

    # ------------------------------------------------------------------ names

    async def names(self, ids: Iterable[int], character_id: int | None = None) -> dict[int, str]:
        """Resolve any mix of character/corp/type/system/station ids to names."""
        wanted = {int(i) for i in ids if i}
        if not wanted:
            return {}

        cached = await self._store.get_names(list(wanted))
        out = {i: v["name"] for i, v in cached.items()}
        missing = sorted(wanted - set(out))
        if not missing:
            return out

        universal = [i for i in missing if i < _STRUCTURE_ID_FLOOR]
        structures = [i for i in missing if i >= _STRUCTURE_ID_FLOOR]

        for start in range(0, len(universal), _NAME_BATCH):
            chunk = universal[start : start + _NAME_BATCH]
            try:
                result = await self._esi.post("/universe/names", json_body=chunk)
            except EsiError as exc:
                # A single unresolvable id makes the whole batch 404; degrade quietly.
                log.info("bulk name lookup failed for %d ids: %s", len(chunk), exc)
                continue
            entries = [
                (int(row["id"]), row["name"], row.get("category"))
                for row in result or []
                if row.get("id") and row.get("name")
            ]
            await self._store.put_names(entries)
            out.update({i: n for i, n, _ in entries})

        if structures and character_id is not None:
            resolved = await asyncio.gather(
                *(self._structure_name(sid, character_id) for sid in structures),
                return_exceptions=True,
            )
            entries = []
            for sid, name in zip(structures, resolved):
                if isinstance(name, str):
                    entries.append((sid, name, "structure"))
                    out[sid] = name
            await self._store.put_names(entries)

        for missing_id in wanted - set(out):
            out[missing_id] = f"Unknown #{missing_id}"
        return out

    async def name(self, id_: int, character_id: int | None = None) -> str:
        return (await self.names([id_], character_id)).get(id_, f"Unknown #{id_}")

    async def _structure_name(self, structure_id: int, character_id: int) -> str:
        result = await self._esi.get(
            f"/universe/structures/{structure_id}", character_id=character_id
        )
        return result.data.get("name", f"Structure #{structure_id}")

    async def ids_from_names(self, names: Sequence[str]) -> dict[str, Any]:
        """Reverse lookup: exact names -> ids, grouped by category.

        ESI rejects the whole request with a 400 when the list holds duplicates
        or more than 500 entries, and callers legitimately produce both — a fit
        with two identical guns, a route whose `avoid` repeats the origin. Both
        are handled here so no call site has to remember.
        """
        unique = list(dict.fromkeys(n.strip() for n in names if n and n.strip()))
        if not unique:
            return {}
        out: dict[str, Any] = {}
        for start in range(0, len(unique), _IDS_BATCH):
            part = await self._esi.post(
                "/universe/ids", json_body=unique[start : start + _IDS_BATCH]
            )
            for key, rows in (part or {}).items():
                out.setdefault(key, []).extend(rows or [])
        return out

    async def resolve_names(
        self,
        names: Sequence[str],
        *,
        prefer: Sequence[str] = (),
        only: Sequence[str] = (),
    ) -> dict[str, NameResolution]:
        """Resolve exact names to one entity each, keyed by lower-cased query.

        `/universe/ids` answers grouped by category, and one string can match in
        several at once — 'Rifter' is both a player character and a ship type.
        Taking entries[0] of a category therefore picks a coin-flip winner, and
        looping every category picks several. Regrouping by name turns that into
        a value the caller has to deal with.

        `only` restricts matching to those categories: an `item` parameter can
        only ever mean an inventory type, so a same-named character is not an
        ambiguity there. `prefer` orders them when several still match; unlisted
        categories sort last, then by category name, then by ascending id — so
        the choice is always deterministic.
        """
        lookup = await self.ids_from_names(names)
        buckets: dict[str, list[NameMatch]] = defaultdict(list)
        for key, entries in (lookup or {}).items():
            if only and key not in only:
                continue
            kind = ID_CATEGORIES.get(key, key)
            for entry in entries or []:
                if not entry.get("id") or not entry.get("name"):
                    continue
                buckets[entry["name"].strip().lower()].append(
                    NameMatch(int(entry["id"]), entry["name"], key, kind)
                )

        rank = {key: i for i, key in enumerate(prefer)}
        out: dict[str, NameResolution] = {}
        for asked in names:
            wanted = (asked or "").strip().lower()
            matches = sorted(
                buckets.get(wanted, []),
                key=lambda m: (rank.get(m.category, len(rank)), m.category, m.id),
            )
            out[wanted] = NameResolution(
                query=(asked or "").strip(),
                chosen=matches[0] if matches else None,
                alternatives=tuple(matches[1:]),
            )
        return out

    # ------------------------------------------------------------------ types

    async def type_info(self, type_id: int) -> dict[str, Any]:
        key = f"type:{type_id}"
        cached = await self._store.get_blob(key, max_age=30 * 86400)
        if cached is not None:
            return cached
        result = await self._esi.get(f"/universe/types/{type_id}")
        await self._store.put_blob(key, result.data)
        return result.data

    async def group_name(self, group_id: int) -> str:
        """Groups have their own id space, separate from /universe/names."""
        if not group_id:
            return "unknown"
        key = f"group:{group_id}"
        cached = await self._store.get_blob(key, max_age=30 * 86400)
        if cached is None:
            try:
                result = await self._esi.get(f"/universe/groups/{group_id}")
            except EsiError:
                return f"Group #{group_id}"
            cached = result.data
            await self._store.put_blob(key, cached)
        return (cached or {}).get("name", f"Group #{group_id}")

    async def type_infos(self, type_ids: Iterable[int]) -> dict[int, dict[str, Any]]:
        unique = sorted({int(t) for t in type_ids if t})
        results = await asyncio.gather(
            *(self.type_info(t) for t in unique), return_exceptions=True
        )
        out: dict[int, dict[str, Any]] = {}
        for type_id, info in zip(unique, results):
            if isinstance(info, dict):
                out[type_id] = info
        return out

    # ----------------------------------------------------------------- prices

    async def reference_prices(self) -> dict[int, dict[str, float]]:
        """CCP's global average/adjusted price per type — the appraisal default."""
        async with self._price_lock:
            # The in-memory copy needs the same TTL as the blob, or the process
            # pins the first snapshot it ever fetched for as long as it runs.
            if self._prices is not None and time.time() - self._prices_at < _PRICE_TTL:
                return self._prices
            cached = await self._store.get_blob("markets:prices", max_age=_PRICE_TTL)
            if cached is None:
                result = await self._esi.get("/markets/prices")
                cached = {
                    str(row["type_id"]): {
                        "average": float(row.get("average_price") or 0.0),
                        "adjusted": float(row.get("adjusted_price") or 0.0),
                    }
                    for row in result.data or []
                }
                await self._store.put_blob("markets:prices", cached)
            self._prices = {int(k): v for k, v in cached.items()}
            self._prices_at = time.time()
            return self._prices

    async def reference_price(self, type_id: int) -> float:
        prices = await self.reference_prices()
        entry = prices.get(int(type_id), {})
        return entry.get("average") or entry.get("adjusted") or 0.0

    async def hub_quotes(
        self,
        type_id: int,
        region_id: int = THE_FORGE_REGION_ID,
        station_id: int | None = JITA_4_4_STATION_ID,
    ) -> dict[str, Any]:
        """Best buy/sell prices for one type at a trade hub, from live orders."""
        result = await self._esi.get_all_pages(
            f"/markets/{region_id}/orders",
            params={"type_id": type_id, "order_type": "all"},
            max_pages=10,
        )
        orders = [o for o in (result.data or []) if isinstance(o, dict)]
        if station_id is not None:
            at_hub = [o for o in orders if o.get("location_id") == station_id]
            if at_hub:
                orders = at_hub
        buys = sorted(
            (o["price"] for o in orders if o.get("is_buy_order")), reverse=True
        )
        sell_prices = sorted(o["price"] for o in orders if not o.get("is_buy_order"))
        return {
            "type_id": type_id,
            "region_id": region_id,
            "station_id": station_id,
            "best_sell": sell_prices[0] if sell_prices else None,
            "best_buy": buys[0] if buys else None,
            "sell_order_count": len(sell_prices),
            "buy_order_count": len(buys),
            "sell_volume": sum(
                o.get("volume_remain", 0) for o in orders if not o.get("is_buy_order")
            ),
            "buy_volume": sum(
                o.get("volume_remain", 0) for o in orders if o.get("is_buy_order")
            ),
            "data_age": result.stale_note,
        }
