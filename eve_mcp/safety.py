"""Guard rails for every mutating ESI call.

Three independent layers, because an LLM holding a write token deserves all of
them:

1. **Capability gate** — a write scope is only ever requested at login if the
   operator enabled that capability, so a disabled group is unreachable even if
   the model tries.
2. **Two-step confirmation** — in the default ``confirm`` mode a write tool
   first returns a human-readable preview plus a single-use token. Nothing
   reaches ESI until the same tool is called again with that token and byte
   identical arguments.
3. **Budgets and an audit log** — a rolling per-hour cap on writes (with a
   tighter one for outgoing mail), and an append-only JSONL record of every
   attempt.
"""
from __future__ import annotations

import hashlib
import json
import logging
import secrets
import time
from collections import deque
from dataclasses import dataclass, field

from typing import Any

from .config import Settings, WRITE_CAPABILITIES

log = logging.getLogger("eve_mcp.safety")


class WriteBlocked(RuntimeError):
    """A write was refused by policy. The message is meant for the user."""


@dataclass
class PendingWrite:
    token: str
    tool: str
    capability: str
    args_digest: str
    preview: dict[str, Any]
    created_at: float = field(default_factory=time.time)


def _digest(payload: Any) -> str:
    canonical = json.dumps(payload, sort_keys=True, default=str)
    return hashlib.sha256(canonical.encode()).hexdigest()[:16]


class WriteGuard:
    def __init__(self, settings: Settings) -> None:
        self._settings = settings
        self._pending: dict[str, PendingWrite] = {}
        self._recent_writes: deque[float] = deque()
        self._recent_mail: deque[float] = deque()

    # ------------------------------------------------------------------ gates

    def check_capability(self, capability: str) -> None:
        cap = WRITE_CAPABILITIES.get(capability)
        if cap is None:
            raise WriteBlocked(f"Unknown write capability {capability!r}.")
        if self._settings.write_mode == "off":
            raise WriteBlocked(
                "Writes are disabled on this server (EVE_WRITE_MODE=off). "
                "Restart the container with EVE_WRITE_MODE=confirm to enable them."
            )
        if capability not in self._settings.write_allow:
            enabled = sorted(self._settings.write_allow) or ["<none>"]
            raise WriteBlocked(
                f"The '{capability}' capability is not enabled on this server "
                f"({cap.summary}). Enabled capabilities: {', '.join(enabled)}. "
                f"Add it to EVE_WRITE_ALLOW and re-authorize the character to change this."
            )

    def check_scope(self, capability: str, granted_scopes: list[str]) -> None:
        cap = WRITE_CAPABILITIES[capability]
        missing = [s for s in cap.scopes if s not in granted_scopes]
        if missing:
            raise WriteBlocked(
                f"This character was not authorized with {', '.join(missing)}. "
                "Log the character in again after enabling the capability."
            )

    def _check_budget(self, capability: str) -> None:
        now = time.time()
        _trim(self._recent_writes, now, 3600)
        if len(self._recent_writes) >= self._settings.write_budget_per_hour:
            raise WriteBlocked(
                f"Write budget exhausted: {self._settings.write_budget_per_hour} writes "
                "in the last hour. This is a safety cap, not an ESI limit — wait, or "
                "raise EVE_WRITE_BUDGET_PER_HOUR."
            )
        if capability == "mail_send":
            _trim(self._recent_mail, now, 3600)
            if len(self._recent_mail) >= self._settings.mail_budget_per_hour:
                raise WriteBlocked(
                    f"Mail budget exhausted: {self._settings.mail_budget_per_hour} mails "
                    "in the last hour."
                )

    def _spend_budget(self, capability: str) -> None:
        now = time.time()
        self._recent_writes.append(now)
        if capability == "mail_send":
            self._recent_mail.append(now)

    # ---------------------------------------------------------- confirm cycle

    def authorize(
        self,
        *,
        tool: str,
        capability: str,
        args: dict[str, Any],
        preview: dict[str, Any],
        confirm_token: str | None,
        granted_scopes: list[str],
    ) -> dict[str, Any] | None:
        """Return a confirmation payload, or None when the write may proceed.

        Callers do::

            blocked = guard.authorize(...)
            if blocked is not None:
                return blocked
            ... perform the write ...
            guard.record(...)
        """
        self.check_capability(capability)
        self.check_scope(capability, granted_scopes)
        self._check_budget(capability)
        self._expire_pending()

        args_digest = _digest(args)

        if self._settings.write_mode == "on":
            return None

        if confirm_token:
            pending = self._pending.get(confirm_token)
            if pending is None:
                raise WriteBlocked(
                    "That confirm_token is unknown or has expired. Call the tool again "
                    "without a token to get a fresh preview."
                )
            if pending.tool != tool:
                raise WriteBlocked(
                    f"confirm_token was issued for '{pending.tool}', not '{tool}'."
                )
            if pending.args_digest != args_digest:
                del self._pending[confirm_token]
                raise WriteBlocked(
                    "The arguments changed since the preview was generated, so the token "
                    "was discarded. Request a new preview and confirm that one."
                )
            del self._pending[confirm_token]
            return None

        token = secrets.token_urlsafe(9)
        self._pending[token] = PendingWrite(
            token=token,
            tool=tool,
            capability=capability,
            args_digest=args_digest,
            preview=preview,
        )
        self.audit(
            {"event": "preview", "tool": tool, "capability": capability, "preview": preview}
        )
        return {
            "status": "confirmation_required",
            "tool": tool,
            "capability": capability,
            "will_do": preview,
            "confirm_token": token,
            "expires_in_seconds": self._settings.confirm_ttl_seconds,
            "next_step": (
                f"Show 'will_do' to the user and get their explicit go-ahead, then call "
                f"{tool} again with identical arguments plus confirm_token='{token}'."
            ),
        }

    def record(self, *, tool: str, capability: str, args: dict[str, Any], result: Any) -> None:
        self._spend_budget(capability)
        self.audit(
            {
                "event": "write",
                "tool": tool,
                "capability": capability,
                "args": args,
                "result": _truncate(result),
            }
        )

    def _expire_pending(self) -> None:
        cutoff = time.time() - self._settings.confirm_ttl_seconds
        for token in [t for t, p in self._pending.items() if p.created_at < cutoff]:
            del self._pending[token]

    # ------------------------------------------------------------------ audit

    def audit(self, entry: dict[str, Any]) -> None:
        record = {"ts": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()), **entry}
        try:
            with self._settings.audit_file.open("a", encoding="utf-8") as handle:
                handle.write(json.dumps(record, default=str) + "\n")
        except OSError as exc:
            log.error("could not write audit log: %s", exc)

    # ------------------------------------------------------------------ intro

    def status(self) -> dict[str, Any]:
        now = time.time()
        _trim(self._recent_writes, now, 3600)
        _trim(self._recent_mail, now, 3600)
        return {
            "write_mode": self._settings.write_mode,
            "enabled_capabilities": sorted(self._settings.write_allow),
            "disabled_capabilities": sorted(
                set(WRITE_CAPABILITIES) - set(self._settings.write_allow)
            ),
            "capability_reference": {
                name: cap.summary for name, cap in sorted(WRITE_CAPABILITIES.items())
            },
            "writes_last_hour": len(self._recent_writes),
            "write_budget_per_hour": self._settings.write_budget_per_hour,
            "mails_last_hour": len(self._recent_mail),
            "mail_budget_per_hour": self._settings.mail_budget_per_hour,
            "pending_confirmations": len(self._pending),
            "audit_log": str(self._settings.audit_file),
        }


def _trim(bucket: deque[float], now: float, window: float) -> None:
    while bucket and bucket[0] < now - window:
        bucket.popleft()


def _truncate(value: Any, limit: int = 500) -> Any:
    text = json.dumps(value, default=str)
    return value if len(text) <= limit else text[:limit] + "…"
