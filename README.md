# eve-mcp

A single Go binary that exposes EVE Online accounts to LLM clients through
CCP's official ESI API, plus guarded write access back into the game.

The instance owns one EVE application. Players never register anything:
they add one URL in Cursor / Claude, hit **Authentication required**, and
sign in with their EVE account in the browser. Each player sees only their
own characters.

35 tools: assets, wallet, skills, industry, PI, market, contracts, mail,
killmails, routes, live hub prices. Plus guarded writes: waypoints, in-client
windows, fittings, mail, contacts. Corporation hangars, wallets and jobs
appear when `CORP_SCOPES` is on.

---

## Quick start

### 1. Register an application

https://developers.eveonline.com/applications → **Create New Application**.

| Field | Value |
|---|---|
| Connection Type | **Authentication & API Access** |
| Callback URL | `http://127.0.0.1:8765/auth/callback` locally, or `{PUBLIC_URL}/auth/callback` — exactly |
| Permissions | the `read` scopes you want, plus `esi-ui.write_waypoint.v1` and `esi-ui.open_window.v1` for waypoints |

Copy the **Client ID**. No Client Secret is needed — the server uses PKCE.

### 2. Configure

Config is env only (see `.env.example`). Put a `.env` in the working
directory of the process (the installed service uses the OS user config
dir):

- macOS: `~/Library/Application Support/eve-mcp/.env`
- Linux: `~/.config/eve-mcp/.env`

```bash
CLIENT_ID=...                # required, from step 1
CONTACT=you@example.com      # identifies you to CCP
```

### 3. Build and install

```bash
go build -o eve-mcp ./cmd/eve-mcp
./eve-mcp install
```

That copies the binary to `~/.local/bin/eve-mcp` and starts a user service
(launchd on macOS, systemd --user on Linux). It keeps running across reboots.

### 4. Connect a client

The only thing a client needs is the URL. The first connection returns `401`
and Cursor / Claude show **Authentication required** — the browser goes
straight to the EVE login, the player picks a character, done. More
characters: `eve_auth_login_url` from the chat, or sign in again.

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

On a public host, use `https://your.example/mcp` and set `PUBLIC_URL` so the
OAuth metadata and the EVE callback match.

---

## On the network

Same binary, same client config — only the URL changes:

```bash
PUBLIC_URL=https://eve.example.com eve-mcp
```

The EVE application callback must then be exactly
`https://eve.example.com/auth/callback`. Friends just add the URL in their
client and sign in with EVE — MCP OAuth binds each of them to their own
characters.

`/healthz` (and metrics, when added) are served on `INTERNAL_LISTEN`
(default `127.0.0.1:8766`) — point k8s probes there, never route it
publicly.

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
| Corporation | `eve_corp_overview` `eve_corp_assets_list` `eve_corp_assets_find` `eve_corp_blueprints` `eve_corp_wallet` `eve_corp_industry_jobs` `eve_corp_mining` `eve_corp_orders` `eve_corp_contracts` `eve_corp_killmails` `eve_corp_structures` `eve_corp_members` — registered only when `CORP_SCOPES` is on |
| Writes | `eve_ui_set_waypoint` `eve_ui_open_window` `eve_fitting_save` `eve_fitting_delete` `eve_mail_mark` `eve_mail_delete` `eve_mail_send` `eve_contacts_set` `eve_contacts_delete` `eve_calendar_respond` |

List tools return a few rows in concise form by default. Full data comes from
`limit` and `response_format="detailed"`. Every response carries `data_age`:
assets are cached for an hour, market for 5 minutes, location for 5 seconds.

---

## Writing to the game

`WRITE_ALLOW` lists the permitted groups. Anything else is neither
registered as a tool nor requested as a scope at login.

| Capability | What it does | Default |
|---|---|---|
| `waypoint` | autopilot waypoints | yes |
| `openwindow` | market / info / contract windows in the client | yes |
| `fittings` | save and delete fittings | yes |
| `mail_organize` | mark read, delete mail | yes |
| `calendar` | respond to calendar invitations | no |
| `mail_send` | send mail to other players | no |
| `contacts` | edit contacts and standings | no |

To enable a disabled one: add it to `WRITE_ALLOW`, restart, and re-authorize
the character — new scopes are required.

With `WRITE_MODE=confirm` (the default) the first call does nothing and
returns a preview plus a single-use `confirm_token`. Show `will_do` to the
user, get an explicit yes, then call again with the same arguments plus the
token.

`WRITE_MODE=off` removes writes entirely; `on` executes immediately.

Rolling one-hour window: `WRITE_BUDGET_PER_HOUR` (40) across all writes and
`MAIL_BUDGET_PER_HOUR` (5) for outgoing mail.

The server cannot fly, shoot, click or trade. ESI grants no control over the
game client. `waypoint` and `openwindow` work only while the EVE client is
logged in on that character.

---

## Configuration

Env only — process environment, or a `.env` in the working directory
(the OS user config dir for an installed service). See `.env.example`.

| Env | Default | Meaning |
|---|---|---|
| `CLIENT_ID` | — | **required**, the instance EVE application |
| `CONTACT` | — | email for the User-Agent |
| `CLIENT_SECRET` | empty | confidential applications only |
| `LISTEN` | `127.0.0.1:8765` | public bind address (MCP + OAuth) |
| `INTERNAL_LISTEN` | `127.0.0.1:8766` | healthz / metrics; never route publicly |
| `PUBLIC_URL` | empty | public base URL (sets the SSO callback) |
| `DATABASE_URL` | — | **required**, Postgres DSN (`make postgres`) |
| `WRITE_MODE` | `confirm` | `off` / `confirm` / `on` |
| `WRITE_ALLOW` | waypoint, openwindow, fittings, mail_organize | list, or `all` / `none` |
| `WRITE_BUDGET_PER_HOUR` | `40` | write ceiling per hour |
| `MAIL_BUDGET_PER_HOUR` | `5` | separate ceiling for mail |
| `CONFIRM_TTL` | `300` | how long a `confirm_token` lives, seconds |
| `CORP_SCOPES` | `true` | request corporation read scopes and register `eve_corp_*` |
| `COMPAT_DATE` | `2026-08-18` | ESI compatibility date |

Refresh tokens are access to the EVE account; they live in Postgres.
Default listen is loopback. Revoke access
with `eve_auth_logout` or from
[authorized-apps](https://developers.eveonline.com/authorized-apps).

---

## Development

```bash
make postgres                     # local Postgres (loopback :5432)
go build -o eve-mcp ./cmd/eve-mcp
./eve-mcp                         # foreground, reads ./.env or the environment
go run ./evals all                # lint + smoke; needs EVE_MCP_TOKEN
```
