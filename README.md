# eve-mcp

A single Go binary that exposes one player's EVE Online account to an LLM
through CCP's official ESI API, plus guarded write access back into the game.

Clients (Cursor, Claude Code, Claude Desktop) connect to one URL. There is no
`.env` on the client, no Docker, and no local rebuild.

35 tools: assets, wallet, skills, industry, PI, market, contracts, mail,
killmails, routes, live hub prices. Plus guarded writes: waypoints, in-client
windows, fittings, mail, contacts. Corporation hangars, wallets and jobs
appear when `corp_scopes` is on.

---

## Quick start

### 1. Register an application

https://developers.eveonline.com/applications → **Create New Application**.

| Field | Value |
|---|---|
| Connection Type | **Authentication & API Access** |
| Callback URL | `http://localhost:8765/auth/callback` — exactly as written |
| Permissions | the `read` scopes you want, plus `esi-ui.write_waypoint.v1` and `esi-ui.open_window.v1` for waypoints |

Copy the **Client ID**. No Client Secret is needed — the server uses PKCE.

### 2. Build and install

```bash
go build -o eve-mcp ./cmd/eve-mcp
./eve-mcp install
```

That copies the binary to `~/.local/bin/eve-mcp` and starts a user service
(launchd on macOS, systemd --user on Linux). It keeps running across reboots.

First run writes `config.toml` into the OS user config directory:

- macOS: `~/Library/Application Support/eve-mcp/config.toml`
- Linux: `~/.config/eve-mcp/config.toml`

If a repo-local `.env` with `EVE_CLIENT_ID` is present, it is imported once
and then ignored. After that, clients never see credentials.

Open http://127.0.0.1:8765/ — if `client_id` is still empty, fill the setup
form, restart (`eve-mcp uninstall && eve-mcp install`, or just kill the
process; KeepAlive restarts it after you save).

### 3. Authorize a character

http://127.0.0.1:8765/ → **Authorize a character**. Log in to EVE, approve
the scopes, come back. Tokens live next to `config.toml`. Several characters
are supported — just repeat the login.

### 4. Connect a client

The only thing a client needs is the URL. There is no `client_id` in the
client config. The first connection returns `401` and Cursor / Claude show
**Authentication required**. That opens a page where the player pastes
**their** EVE application Client ID and logs in through EVE SSO.

**Cursor** — `~/.cursor/mcp.json`:

```json
{
  "mcpServers": {
    "eve": {
      "url": "http://127.0.0.1:8765/mcp"
    }
  }
}
```

**Claude Code**

```bash
claude mcp add --transport http eve http://127.0.0.1:8765/mcp
```

On a public host, use `https://your.example/mcp` and set `public_url` /
`-public-url` so the OAuth metadata and the EVE callback match.

---

## On the network

Same binary, same client config — only the URL changes:

```bash
eve-mcp -listen 127.0.0.1:8765 -public-url https://eve.example.com
```

Each player registers their own CCP application with callback
`https://eve.example.com/auth/callback`. Cursor/Claude run MCP OAuth
against this server; EVE SSO runs with that player's Client ID.

If ESI returns `420` / error-limit headers, tools answer
`kind: EsiRateLimited` with `retry_at` and `retry_after_seconds`. Wait
until then. The bucket is per public IP.

---

## Tools

Start with `eve_character_overview` — corporation, wallet, location, ship and
training in a single call.

| Domain | Tools |
|---|---|
| Account | `eve_auth_status` `eve_auth_login_url` `eve_auth_logout` `eve_server_status` `eve_character_overview` |
| Character | `eve_character_skills` `eve_character_skill_queue` `eve_character_clones` `eve_character_standings` |
| Assets | `eve_assets_list` `eve_assets_find` `eve_assets_blueprints` |
| Wallet | `eve_wallet_history` |
| Industry | `eve_industry_jobs` `eve_industry_planets` `eve_industry_mining` |
| Market | `eve_market_price` `eve_market_orders` `eve_market_contracts` |
| Social | `eve_mail_list` `eve_mail_read` `eve_social_notifications` `eve_social_killmails` `eve_fitting_list` |
| Universe | `eve_universe_search` `eve_universe_item` `eve_universe_system` `eve_universe_route` `eve_universe_hotspots` |
| Corporation | `eve_corp_overview` `eve_corp_assets_list` `eve_corp_assets_find` `eve_corp_blueprints` `eve_corp_wallet` `eve_corp_industry_jobs` `eve_corp_mining` `eve_corp_orders` `eve_corp_contracts` `eve_corp_killmails` `eve_corp_structures` `eve_corp_members` — registered only when `corp_scopes` is on |
| Writes | `eve_ui_set_waypoint` `eve_ui_open_window` `eve_fitting_save` `eve_fitting_delete` `eve_mail_mark` `eve_mail_delete` `eve_mail_send` `eve_contacts_set` `eve_contacts_delete` `eve_calendar_respond` |

List tools return a few rows in concise form by default. Full data comes from
`limit` and `response_format="detailed"`. Every response carries `data_age`:
assets are cached for an hour, market for 5 minutes, location for 5 seconds.

---

## Writing to the game

`write_allow` in `config.toml` lists the permitted groups. Anything else is
neither registered as a tool nor requested as a scope at login.

| Capability | What it does | Default |
|---|---|---|
| `waypoint` | autopilot waypoints | yes |
| `openwindow` | market / info / contract windows in the client | yes |
| `fittings` | save and delete fittings | yes |
| `mail_organize` | mark read, delete mail | yes |
| `calendar` | respond to calendar invitations | no |
| `mail_send` | send mail to other players | no |
| `contacts` | edit contacts and standings | no |

To enable a disabled one: add it to `write_allow`, restart, and re-authorize
the character — new scopes are required.

With `write_mode = "confirm"` (the default) the first call does nothing and
returns a preview plus a single-use `confirm_token`. Show `will_do` to the
user, get an explicit yes, then call again with the same arguments plus the
token.

`write_mode = "off"` removes writes entirely; `"on"` executes immediately.

Rolling one-hour window: `write_budget_per_hour` (40) across all writes and
`mail_budget_per_hour` (5) for outgoing mail. Every attempt is appended to
`audit.jsonl` next to the config.

The server cannot fly, shoot, click or trade. ESI grants no control over the
game client. `waypoint` and `openwindow` work only while the EVE client is
logged in on that character.

---

## Configuration

All of this lives in `config.toml`. Environment variables with the same names
prefixed `EVE_` still override a field if you want them, but they are not
required.

| Key | Default | Meaning |
|---|---|---|
| `client_id` | — | **required**, from the dev portal |
| `contact` | — | email for the User-Agent |
| `client_secret` | empty | confidential applications only |
| `listen` | `127.0.0.1:8765` | bind address |
| `public_url` | empty | public base URL (sets the SSO callback) |
| `mcp_token` | empty | bearer token for `/mcp` when not on loopback |
| `write_mode` | `confirm` | `off` / `confirm` / `on` |
| `write_allow` | waypoint, openwindow, fittings, mail_organize | list, or `all` / `none` |
| `write_budget_per_hour` | `40` | write ceiling per hour |
| `mail_budget_per_hour` | `5` | separate ceiling for mail |
| `confirm_ttl_seconds` | `300` | how long a `confirm_token` lives |
| `corp_scopes` | `true` | request corporation read scopes and register `eve_corp_*` |
| `compat_date` | `2026-08-18` | ESI compatibility date |

Refresh tokens are access to the EVE account. Default listen is loopback.
Revoke access with `eve_auth_logout` or from
[authorized-apps](https://developers.eveonline.com/authorized-apps).

---

## Development

```bash
go build -o eve-mcp ./cmd/eve-mcp
./eve-mcp                         # foreground, imports .env on first run
python3 evals/run.py all          # lint + smoke against the running server
```

The Python tree under `eve_mcp/` is the previous implementation, kept as a
reference. The running server is this Go binary.
