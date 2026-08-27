"""Helpers shared by the tool modules."""
from __future__ import annotations

import functools
import inspect
import logging
from typing import Any, Awaitable, Callable, Iterable, Sequence

from ..auth import AuthError
from ..context import CharacterNotFound
from ..esi import EsiError
from ..safety import WriteBlocked

log = logging.getLogger("eve_mcp.tools")


def handled(fn: Callable[..., Awaitable[Any]]) -> Callable[..., Awaitable[Any]]:
    """Turn expected failures into structured results instead of tracebacks.

    Also normalises the docstring: MCP ships `__doc__` verbatim, so without
    this every description carries the function's Python indentation.
    """

    @functools.wraps(fn)
    async def wrapper(*args: Any, **kwargs: Any) -> Any:
        try:
            return await fn(*args, **kwargs)
        except (AuthError, CharacterNotFound, WriteBlocked) as exc:
            return {"error": str(exc), "kind": type(exc).__name__}
        except EsiError as exc:
            return {"error": str(exc), "kind": "EsiError", "status": exc.status}

    wrapper.__doc__ = inspect.cleandoc(fn.__doc__ or "")
    return wrapper


def isk(value: float | int | None) -> str:
    """Format ISK the way the game does: 1.23b / 45.6m / 789.0k."""
    if value is None:
        return "—"
    amount = float(value)
    for limit, suffix in ((1e12, "t"), (1e9, "b"), (1e6, "m"), (1e3, "k")):
        if abs(amount) >= limit:
            return f"{amount / limit:,.2f}{suffix}"
    return f"{amount:,.2f}"


def unit_price(prices: dict[int, dict[str, float]], type_id: int | None) -> float:
    """CCP's reference price for one unit of a type.

    `average_price` is absent for anything that does not trade on the open
    market — 1421 types, capital hulls among them — so `adjusted_price` has to
    back it up. Reading `average` alone values an Erebus hull at zero.
    """
    entry = prices.get(int(type_id or 0), {})
    return entry.get("average") or entry.get("adjusted") or 0.0


def project(rows: Sequence[dict[str, Any]], keep: Iterable[str], concise: bool) -> list[dict]:
    """Drop secondary fields in concise mode, and empty values in either mode.

    Empty keys are pure context cost — the model learns nothing from
    `"reason": null` that it doesn't learn from the key's absence.
    """
    keep_set = set(keep)
    out = []
    for row in rows:
        picked = {k: v for k, v in row.items() if not concise or k in keep_set}
        out.append({k: v for k, v in picked.items() if v not in (None, "", [], {})})
    return out


def page(
    rows: list[dict[str, Any]], limit: int, hint: str = ""
) -> tuple[list[dict[str, Any]], dict[str, Any]]:
    """Cut a list to `limit` and describe what was cut.

    Returns the visible rows plus metadata to splice into the response, so
    every list tool reports truncation the same way.
    """
    if len(rows) <= limit:
        return rows, {"returned": len(rows), "truncated": False}
    meta = {
        "returned": limit,
        "total_available": len(rows),
        "truncated": True,
        "how_to_see_more": hint or f"Raise `limit` (currently {limit}).",
    }
    return rows[:limit], meta
