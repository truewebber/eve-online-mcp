# eve-mcp

A single Go binary that exposes EVE Online accounts to LLM clients through
CCP's official ESI API, plus guarded write access back into the game.

The instance owns one EVE application. Players never register anything:
they add one URL in Cursor / Claude, hit **Authentication required**, and
sign in with their EVE account in the browser. A connection *is* the
character picked at that login: it reads and acts as that character and
nothing else. An alt is a second server entry with its own sign-in.

52 tools: assets, wallet, skills, industry, PI, market, contracts, mail,
calendar, killmails, routes, live hub prices. Plus guarded writes:
waypoints, in-client windows, fittings, mail — sent, or handed to the
player pre-filled — calendar answers and contacts. Corporation hangars,
wallets and jobs are always registered; in-game roles are the only gate.

---

## Quick start

### 1. Register an application

https://developers.eveonline.com/applications → **Create New Application**.

| Field | Value |
|---|---|
| Connection Type | **Authentication & API Access** |
| Callback URL | `http://127.0.0.1:8765/auth/callback` locally, or `{PUBLIC_URL}/auth/callback` — exactly |
| Permissions | **every** scope the build asks for — all 51, listed in `internal/domain/write/policy.go` (`RequestedScopes`) |

Permissions is not a place to pick and choose: EVE grants only what the
application lists, and a login that comes back missing one is refused at
the callback with the missing names spelled out. Copy the whole set.

Copy the **Client ID**. No Client Secret is needed — the server uses PKCE.

### 2. Configure

Config is env only (see `.env.example`). Put a `.env` in the working
directory of the process (the installed service uses the OS user config
dir):

- macOS: `~/Library/Application Support/eve-mcp/.env`
- Linux: `~/.config/eve-mcp/.env`

```bash
CLIENT_ID=...                # required, from step 1
DATABASE_URL=postgres://...  # required, the only durable store
HMAC_KEY=...                 # required, openssl rand -hex 32
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
straight to the EVE login, the player picks a character, done. A second
character is a second entry in the client, signed in separately; signing
the same character in from somewhere else moves the connection there and
signs the old one out. A sign-in lasts 30 days.

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

`/healthz`, `/readyz` (and metrics, when added) are served on
`INTERNAL_LISTEN` (default `127.0.0.1:8766`) — point k8s probes there,
never route it publicly. Liveness is `/healthz`; readiness is `/readyz`,
which also pings Postgres, so a pod that cannot reach the database leaves
the Service instead of being restarted forever. Under Kubernetes both
listeners need `0.0.0.0:{port}`, or the kubelet cannot reach the probe,
and `PUBLIC_URL` must be set once the bind is not loopback. Any number of
replicas works — all durable state is in Postgres — but caches and rate
counters live in each pod, so above one replica hash `/mcp` onto pods by
the `Authorization` header. That keeps a character on one pod for as long
as its access token lives, an hour, which is stickiness rather than a
guarantee. Replicas buy failover and rolling updates, not ESI headroom:
CCP's error limit is per IP.

If ESI returns `420` / error-limit headers, tools answer
`kind: EsiRateLimited` with `retry_at` and `retry_after_seconds`. Wait
until then. Each character also has its own request allowance and error
budget, so one looping assistant slows itself down rather than the
household; those refusals come back as `kind: UserRateLimited`.

---

## Tools

Start with `eve_character_overview` — corporation, wallet, location, ship and
training in a single call.

| Domain | Tools |
|---|---|
| Account | `eve_auth_status` `eve_auth_logout` `eve_server_status` `eve_character_overview` |
| Character | `eve_character_skills` `eve_character_skill_queue` `eve_character_clones` `eve_character_standings` |
| Assets | `eve_assets_list` `eve_assets_find` `eve_assets_blueprints` |
| Wallet | `eve_wallet_history` |
| Industry | `eve_industry_jobs` `eve_industry_planets` `eve_industry_mining` |
| Market | `eve_market_price` `eve_market_orders` `eve_market_contracts` |
| Social | `eve_mail_list` `eve_mail_read` `eve_social_notifications` `eve_calendar_list` `eve_social_killmails` `eve_fitting_list` |
| Universe | `eve_universe_search` `eve_universe_item` `eve_universe_system` `eve_universe_route` `eve_universe_hotspots` |
| Corporation | `eve_corp_overview` `eve_corp_assets_list` `eve_corp_assets_find` `eve_corp_blueprints` `eve_corp_wallet` `eve_corp_industry_jobs` `eve_corp_mining` `eve_corp_orders` `eve_corp_contracts` `eve_corp_killmails` `eve_corp_structures` `eve_corp_members` |
| Writes | `eve_ui_set_waypoint` `eve_ui_open_window` `eve_fitting_save` `eve_fitting_delete` `eve_mail_mark` `eve_mail_delete` `eve_mail_compose` `eve_mail_send` `eve_contacts_set` `eve_contacts_delete` `eve_calendar_respond` |

List tools return a few rows in concise form by default. Full data comes from
`limit` and `response_format="detailed"`. Every response carries `data_age`:
assets are cached for an hour, market for 5 minutes, location for 5 seconds.

Long lists are walked the way ESI itself pages them: a `page` number where
the endpoint has pages, the cursor back from `next_cursor` where it has a
cursor, an `offset` over the assembled result where the tool groups or
sums across pages, and nothing at all where ESI answers in one response.
The Pagination table in [docs/TOOLS.md](docs/TOOLS.md) says which tool is
which.

---

## Writing to the game

Every mutating tool is always registered. The first call does nothing and
returns a preview plus a single-use `confirm_token`. Show `will_do` to the
user, get an explicit yes, then call again with the same arguments plus the
token.

Outgoing mail is capped at 5 per rolling hour, and its preview prices any
CSPA charge the recipients levy, so nothing is paid that the confirmation
did not name. `eve_mail_compose` is the version that sends nothing: it
fills in the client's compose window and leaves Send to the player.

There is no general write budget. Every attempted change — successful or
not — is appended to an audit log in Postgres, so "what did the assistant
actually do" is a SQL query, not a guess. That log is also the honest
answer to "did a human agree": the server can prove a change was
previewed and that its arguments match the preview, but the assistant is
what asks the user.

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
| `PUBLIC_URL` | empty | public base URL (sets the SSO callback); must be HTTPS unless loopback |
| `EXTRA_REDIRECT_URIS` | empty | extra exact OAuth callbacks, for a client beyond Cursor / Claude |
| `DATABASE_URL` | — | **required**, Postgres DSN (`make postgres`) |
| `HMAC_KEY` | — | **required**, MCP JWT signing key, min 32 bytes (`openssl rand -hex 32`) |

Refresh tokens are access to the EVE account; they live in Postgres.
Default listen is loopback. Revoke access with `eve_auth_logout` or from
[authorized-apps](https://developers.eveonline.com/authorized-apps) —
either way the connection dies and the client asks for a new sign-in.

---

## Development

```bash
make postgres                     # local Postgres (loopback :5432)
go build -o eve-mcp ./cmd/eve-mcp
./eve-mcp                         # foreground, reads ./.env or the environment
go run ./evals all                # lint + smoke; needs EVE_MCP_TOKEN
```

The server is a Go binary on the host. Postgres is Compose-only — do not
put the app in Compose, and do not run `docker compose down -v`: that
deletes the `eve-mcp-pg` volume. `make down` stops Postgres and keeps it.

Nothing reloads in place. Rebuild, then restart — after `./eve-mcp
install`, that is `launchctl kickstart -k gui/$(id -u)/eve-mcp`.

### Where the contracts live

`docs/` is normative and the code follows it, not the other way round.
Read the relevant one before changing behaviour; a change lands in the
same commit as its document.

| Document | Owns |
|---|---|
| [docs/PRD.md](docs/PRD.md) | what the product promises a player |
| [docs/SPEC.md](docs/SPEC.md) | how it is built; §12 is the remaining work |
| [docs/TOOLS.md](docs/TOOLS.md) | every tool: description, parameters, bounds |
| [docs/ESI.md](docs/ESI.md) | every ESI endpoint the server may call |
| [docs/AUTH.md](docs/AUTH.md) | every credential: where it travels, where it rests |
| [docs/DB.md](docs/DB.md) | the schema, its TTLs and sweeps |

Four invariants are worth knowing before you read any of them, because
breaking one regresses the server in ways the tests will not catch:
tools return JSON as `TextContent` with **no** typed output schema
(a typed `Out` drops undeclared keys); every mutating tool goes through
`write.Guard` for the confirm cycle, the mail cap and the audit row; all
ESI traffic goes through `adapter/esi.Client`, never a bare
`http.Client`; and numeric bounds belong in `patchBounds`, never in a
`jsonschema` tag, whose entire text the model reads as prose.
