"""Configuration, scope catalogue and write-capability definitions."""
from __future__ import annotations

import os
from dataclasses import dataclass
from pathlib import Path

ESI_BASE = "https://esi.evetech.net"
SSO_BASE = "https://login.eveonline.com"
AUTHORIZE_URL = f"{SSO_BASE}/v2/oauth/authorize"
TOKEN_URL = f"{SSO_BASE}/v2/oauth/token"
REVOKE_URL = f"{SSO_BASE}/v2/oauth/revoke"
JWKS_URL = f"{SSO_BASE}/oauth/jwks"
TOKEN_AUDIENCE = "EVE Online"
TOKEN_ISSUERS = ("login.eveonline.com", "https://login.eveonline.com")

# Pinned ESI compatibility date. Response shapes for every endpoint this server
# touches are identical to the 2020-01-01 baseline, but this date also unlocks
# the newer routes (projects, freelance jobs, skyhooks, ...).
DEFAULT_COMPAT_DATE = "2026-08-18"

# Well-known trade hub ids, used as appraisal defaults.
THE_FORGE_REGION_ID = 10000002
JITA_SYSTEM_ID = 30000142
JITA_4_4_STATION_ID = 60003760

READ_SCOPES: tuple[str, ...] = (
    "esi-assets.read_assets.v1",
    "esi-calendar.read_calendar_events.v1",
    "esi-characters.read_agents_research.v1",
    "esi-characters.read_blueprints.v1",
    "esi-characters.read_contacts.v1",
    "esi-characters.read_corporation_roles.v1",
    "esi-characters.read_fatigue.v1",
    "esi-characters.read_fw_stats.v1",
    "esi-characters.read_loyalty.v1",
    "esi-characters.read_medals.v1",
    "esi-characters.read_notifications.v1",
    "esi-characters.read_standings.v1",
    "esi-characters.read_titles.v1",
    "esi-clones.read_clones.v1",
    "esi-clones.read_implants.v1",
    "esi-contracts.read_character_contracts.v1",
    "esi-fittings.read_fittings.v1",
    "esi-fleets.read_fleet.v1",
    "esi-industry.read_character_jobs.v1",
    "esi-industry.read_character_mining.v1",
    "esi-killmails.read_killmails.v1",
    "esi-location.read_location.v1",
    "esi-location.read_online.v1",
    "esi-location.read_ship_type.v1",
    "esi-mail.read_mail.v1",
    "esi-markets.read_character_orders.v1",
    "esi-markets.structure_markets.v1",
    "esi-planets.manage_planets.v1",
    "esi-search.search_structures.v1",
    "esi-skills.read_skillqueue.v1",
    "esi-skills.read_skills.v1",
    "esi-universe.read_structures.v1",
    "esi-wallet.read_character_wallet.v1",
)

CORP_READ_SCOPES: tuple[str, ...] = (
    "esi-assets.read_corporation_assets.v1",
    "esi-contracts.read_corporation_contracts.v1",
    "esi-corporations.read_blueprints.v1",
    "esi-corporations.read_corporation_membership.v1",
    "esi-corporations.read_divisions.v1",
    "esi-corporations.read_structures.v1",
    "esi-industry.read_corporation_jobs.v1",
    "esi-industry.read_corporation_mining.v1",
    "esi-killmails.read_corporation_killmails.v1",
    "esi-markets.read_corporation_orders.v1",
    "esi-wallet.read_corporation_wallets.v1",
)


@dataclass(frozen=True)
class WriteCapability:
    """One toggleable group of mutating operations."""

    name: str
    scopes: tuple[str, ...]
    summary: str
    #: True when the action is visible to other players or costs ISK.
    outward_facing: bool = False


WRITE_CAPABILITIES: dict[str, WriteCapability] = {
    c.name: c
    for c in (
        WriteCapability(
            "waypoint",
            ("esi-ui.write_waypoint.v1",),
            "Set autopilot waypoints in the running game client.",
        ),
        WriteCapability(
            "openwindow",
            ("esi-ui.open_window.v1",),
            "Open market / info / contract / new-mail windows in the client.",
        ),
        WriteCapability(
            "fittings",
            ("esi-fittings.write_fittings.v1",),
            "Save and delete saved ship fittings.",
        ),
        WriteCapability(
            "calendar",
            ("esi-calendar.respond_calendar_events.v1",),
            "Respond to calendar events (accept / decline / tentative).",
            outward_facing=True,
        ),
        WriteCapability(
            "mail_organize",
            ("esi-mail.organize_mail.v1",),
            "Mark mail read, manage labels, delete mail.",
        ),
        WriteCapability(
            "mail_send",
            ("esi-mail.send_mail.v1",),
            "Send in-game EVE mail to other players. Off by default.",
            outward_facing=True,
        ),
        WriteCapability(
            "contacts",
            ("esi-characters.write_contacts.v1",),
            "Add, edit and delete character contacts and standings. Off by default.",
            outward_facing=True,
        ),
    )
}

#: Capabilities enabled unless EVE_WRITE_ALLOW says otherwise. The two most
#: socially consequential groups (mail_send, contacts) are deliberately absent.
DEFAULT_WRITE_ALLOW = ("waypoint", "openwindow", "fittings", "mail_organize")

WRITE_MODES = ("off", "confirm", "on")


def _env(name: str, default: str = "") -> str:
    return os.environ.get(name, default).strip()


def _env_bool(name: str, default: bool = False) -> bool:
    raw = _env(name).lower()
    if not raw:
        return default
    return raw in ("1", "true", "yes", "on")


def _env_int(name: str, default: int) -> int:
    raw = _env(name)
    try:
        return int(raw) if raw else default
    except ValueError:
        return default


def _env_csv(name: str) -> tuple[str, ...] | None:
    raw = _env(name)
    if not raw:
        return None
    return tuple(part.strip() for part in raw.split(",") if part.strip())


@dataclass
class Settings:
    client_id: str
    client_secret: str
    callback_url: str
    user_agent: str
    compat_date: str
    data_dir: Path
    host: str
    port: int
    mcp_path: str
    bearer_token: str
    write_mode: str
    write_allow: frozenset[str]
    corp_scopes: bool
    write_budget_per_hour: int
    mail_budget_per_hour: int
    confirm_ttl_seconds: int
    request_timeout: float
    max_concurrency: int

    @property
    def token_file(self) -> Path:
        return self.data_dir / "tokens.json"

    @property
    def cache_file(self) -> Path:
        return self.data_dir / "cache.sqlite3"

    @property
    def audit_file(self) -> Path:
        return self.data_dir / "audit.jsonl"

    @property
    def writes_enabled(self) -> bool:
        return self.write_mode != "off" and bool(self.write_allow)

    def capability_enabled(self, name: str) -> bool:
        return self.write_mode != "off" and name in self.write_allow

    def requested_scopes(self) -> list[str]:
        """Scopes to ask for at login: reads plus whatever writes are enabled."""
        scopes = list(READ_SCOPES)
        if self.corp_scopes:
            scopes += list(CORP_READ_SCOPES)
        if self.write_mode != "off":
            for name in sorted(self.write_allow):
                cap = WRITE_CAPABILITIES.get(name)
                if cap:
                    scopes += list(cap.scopes)
        return sorted(set(scopes))


def load_settings() -> Settings:
    data_dir = Path(_env("EVE_DATA_DIR", "/data"))
    data_dir.mkdir(parents=True, exist_ok=True)

    port = _env_int("EVE_PORT", 8765)
    callback = _env("EVE_CALLBACK_URL") or f"http://localhost:{port}/auth/callback"

    contact = _env("EVE_CONTACT")
    user_agent = _env("EVE_USER_AGENT")
    if not user_agent:
        suffix = f" {contact}" if contact else ""
        user_agent = f"eve-mcp/0.1.0{suffix}"

    mode = _env("EVE_WRITE_MODE", "confirm").lower()
    if mode not in WRITE_MODES:
        raise ValueError(f"EVE_WRITE_MODE must be one of {WRITE_MODES}, got {mode!r}")

    allow_raw = _env_csv("EVE_WRITE_ALLOW")
    if allow_raw is None:
        allow = set(DEFAULT_WRITE_ALLOW)
    elif allow_raw == ("all",):
        allow = set(WRITE_CAPABILITIES)
    elif allow_raw in ((), ("none",)):
        allow = set()
    else:
        unknown = set(allow_raw) - set(WRITE_CAPABILITIES)
        if unknown:
            raise ValueError(
                f"Unknown EVE_WRITE_ALLOW entries: {sorted(unknown)}. "
                f"Valid: {sorted(WRITE_CAPABILITIES)}"
            )
        allow = set(allow_raw)

    return Settings(
        client_id=_env("EVE_CLIENT_ID"),
        client_secret=_env("EVE_CLIENT_SECRET"),
        callback_url=callback,
        user_agent=user_agent,
        compat_date=_env("EVE_COMPAT_DATE", DEFAULT_COMPAT_DATE),
        data_dir=data_dir,
        host=_env("EVE_HOST", "0.0.0.0"),
        port=port,
        mcp_path=_env("EVE_MCP_PATH", "/mcp"),
        bearer_token=_env("EVE_MCP_TOKEN"),
        write_mode=mode,
        write_allow=frozenset(allow),
        corp_scopes=_env_bool("EVE_CORP_SCOPES", False),
        write_budget_per_hour=_env_int("EVE_WRITE_BUDGET_PER_HOUR", 40),
        mail_budget_per_hour=_env_int("EVE_MAIL_BUDGET_PER_HOUR", 5),
        confirm_ttl_seconds=_env_int("EVE_CONFIRM_TTL", 300),
        request_timeout=float(_env("EVE_REQUEST_TIMEOUT", "30")),
        max_concurrency=_env_int("EVE_MAX_CONCURRENCY", 8),
    )
