# eve-mcp

A containerised MCP server that exposes an EVE Online account to an LLM through
CCP's official ESI API, plus guarded write access back into the game.

35 tools: assets, wallet, skills, industry, PI, market, contracts, mail,
killmails, routes, live hub prices. Plus guarded writes: waypoints, in-client
windows, fittings, mail, contacts.

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

### 2. Configure and start

```bash
cp .env.example .env
$EDITOR .env          # EVE_CLIENT_ID and EVE_CONTACT are required
docker compose up -d --build
curl -s localhost:8765/health
```

### 3. Authorize a character

Open http://localhost:8765/ → **Authorize a character**. Log in to EVE, approve
the scopes, come back. Tokens live in the `eve-data` docker volume, so this is a
one-time step per character. Several characters are supported — just repeat the
login.

### 4. Connect a client

**Claude Code**

```bash
claude mcp add --transport http eve http://localhost:8765/mcp
```

**Cursor**

`~/.cursor/mcp.json` (global) or `.cursor/mcp.json` (per project):

```json
{
  "mcpServers": {
    "eve": {
      "url": "http://localhost:8765/mcp"
    }
  }
}
```

**Other clients:** endpoint `http://localhost:8765/mcp`, streamable HTTP transport.

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
| Writes | `eve_ui_set_waypoint` `eve_ui_open_window` `eve_fitting_save` `eve_fitting_delete` `eve_mail_mark` `eve_mail_delete` `eve_mail_send` `eve_contacts_set` `eve_contacts_delete` `eve_calendar_respond` |

Example questions:

- "What do I own and where" → `eve_assets_list`
- "Where did I leave my Orca" → `eve_assets_find`, searches inside containers too
- "Where did my ISK go this month" → `eve_wallet_history`
- "What is Tritanium going for in Jita" → `eve_market_price`, live orders
- "Safe route to Amarr" → `eve_universe_route`
- "Anything that needs attention" → `eve_social_notifications`

List tools return a few rows in concise form by default. Full data comes from
`limit` and `response_format="detailed"`. Every response carries `data_age`:
assets are cached for an hour, market for 5 minutes, location for 5 seconds.

---

## Writing to the game

### What is allowed

`EVE_WRITE_ALLOW` lists the permitted groups. Anything else is neither
registered as a tool nor requested as a scope at login.

| Capability | What it does | Default |
|---|---|---|
| `waypoint` | autopilot waypoints | ✅ |
| `openwindow` | market / info / contract windows in the client | ✅ |
| `fittings` | save and delete fittings | ✅ |
| `mail_organize` | mark read, delete mail | ✅ |
| `calendar` | respond to calendar invitations | ❌ |
| `mail_send` | send mail to other players | ❌ |
| `contacts` | edit contacts and standings | ❌ |

To enable a disabled one: add it to `EVE_WRITE_ALLOW`, restart the container and
re-authorize the character — new scopes are required.

### Confirmation

With `EVE_WRITE_MODE=confirm` (the default) the first call does nothing and
returns a preview plus a single-use `confirm_token`:

```json
{
  "status": "confirmation_required",
  "will_do": {
    "action": "Set autopilot waypoint",
    "character": "Your Character",
    "destination": "Amarr (solar system)",
    "clears_existing_route": true
  },
  "confirm_token": "QPOlrI1XOAnx",
  "expires_in_seconds": 300
}
```

The token is single-use, lives 5 minutes, and is bound to the tool, the
character and a hash of the arguments — changing any of them voids it.

`EVE_WRITE_MODE=off` removes writes entirely; `on` executes immediately.

### Budgets and audit log

Rolling one-hour window: `EVE_WRITE_BUDGET_PER_HOUR` (40) across all writes and
`EVE_MAIL_BUDGET_PER_HOUR` (5) for outgoing mail specifically. Every attempt —
preview and execution alike — is appended to `/data/audit.jsonl`.

```bash
docker compose exec eve-mcp cat /data/audit.jsonl
```

Current policy: the `eve_auth_status` tool.

### What the server cannot do

Fly, shoot, click or trade for you. ESI grants no control over the game client.
`waypoint` and `openwindow` work only while the EVE client is logged in on that
character, and merely place a marker or open a window.

---

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `EVE_CLIENT_ID` | — | **required**, from the dev portal |
| `EVE_CONTACT` | — | email for the User-Agent, needed |
| `EVE_CLIENT_SECRET` | empty | confidential applications only |
| `EVE_WRITE_MODE` | `confirm` | `off` / `confirm` / `on` |
| `EVE_WRITE_ALLOW` | `waypoint,openwindow,fittings,mail_organize` | list, or `all` / `none` |
| `EVE_WRITE_BUDGET_PER_HOUR` | `40` | write ceiling per hour |
| `EVE_MAIL_BUDGET_PER_HOUR` | `5` | separate ceiling for mail |
| `EVE_CONFIRM_TTL` | `300` | how long a `confirm_token` lives |
| `EVE_CORP_SCOPES` | `false` | also request corporation read scopes |
| `EVE_COMPAT_DATE` | `2026-08-18` | ESI compatibility date |
| `EVE_LOG_LEVEL` | `INFO` | |

The `eve-data` volume holds refresh tokens — that is access to the EVE account.
The port is published as `127.0.0.1:8765:8765` and is not reachable from outside
the machine.

Revoke access with the `eve_auth_logout` tool or from
[authorized-apps](https://developers.eveonline.com/authorized-apps).

---

## Development

```bash
docker compose up -d --build         # rebuild and start
python3 evals/run.py all             # tool quality gates
docker compose logs -f               # logs
docker compose exec eve-mcp sh       # shell inside the container
docker compose down                  # stop (tokens are kept)
```

`docker compose down -v` deletes the volume along with the tokens — every
character would have to be authorized again.
