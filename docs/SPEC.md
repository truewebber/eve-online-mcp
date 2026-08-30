# eve-mcp — Technical Specification

Companion to [PRD.md](PRD.md). The PRD says what the product does; this
document says how to build it. Where the current code differs, the delta
is listed in §12 — the spec describes the target.

Implementation is sliced into Composer-sized tasks in
[plan/README.md](plan/README.md). That board is the status of the work;
this file stays the contract.

## 1. Runtime topology

One Go binary, one container image, N identical replicas behind one
public address. All durable state lives in **PostgreSQL**; pods hold only
losable caches (access tokens, rate buckets, error-limit accounting), so
any replica can serve any request — including an OAuth callback for a
handshake another replica started.

Each pod runs two HTTP listeners:

| Listener | Env | Default | Serves | Exposure |
|---|---|---|---|---|
| Public | `LISTEN` | `127.0.0.1:8765` | `/mcp`, OAuth endpoints, human pages | Ingress / Cloudflare tunnel |
| Internal | `INTERNAL_LISTEN` | `127.0.0.1:8766` | `/healthz`, `/metrics` (future) | Cluster-only. Never routed publicly |

Scaling caveat: replicas do not add ESI throughput. All pods share one
egress IP, and CCP's error limit is per IP; more replicas buy rolling
updates and failover, not bandwidth.

## 2. Configuration

Env only. Read from the process environment, or from `./.env` in the
working directory (godotenv; the file wins if present). No config file,
nothing written back at runtime. Config lives in `cmd/eve-mcp/config.go`
(`package main`) and is never imported by inner layers; `main` maps it
into per-module `Options`.

| Env | Required | Default | Meaning |
|---|---|---|---|
| `CLIENT_ID` | **yes** | — | The instance EVE application (developers.eveonline.com) |
| `CLIENT_SECRET` | no | empty | Only for confidential applications; PKCE is used either way |
| `CONTACT` | no | empty | Operator email for the User-Agent; strongly recommended |
| `LISTEN` | no | `127.0.0.1:8765` | Public bind address |
| `INTERNAL_LISTEN` | no | `127.0.0.1:8766` | Internal bind address |
| `PUBLIC_URL` | no | empty | Public base URL; also fixes the EVE callback to `{PUBLIC_URL}/auth/callback` |
| `DATABASE_URL` | **yes** | — | PostgreSQL DSN; sole durable store (tokens, users, OAuth state, cache) |

Validation: listeners must be `host:port`; the callback URL defaults to
`http://127.0.0.1:{port}/auth/callback` when `PUBLIC_URL` is empty.
User-Agent is `eve-mcp/{version} {CONTACT}`.

**Deliberately not configurable** (constants in code, per PRD "clean
bridge, no host policy"):

| Constant | Value | Rationale |
|---|---|---|
| Compatibility date | `2026-08-18` | Changing it is a code change: every response shape must be re-verified (§9) |
| Confirm token TTL | 300 s | Consent window for one mutation |
| Mail cap | 5 / rolling hour / user | Anti-spam toward other players |
| Per-user ESI allowance | bucket 400, refill 2/s | §5.3 |
| ESI concurrency | 8 in flight per user session | Politeness |
| HTTP timeout | 30 s | — |
| Write capabilities | all registered, always | Full API, consent per call |
| Corp scopes | always requested | In-game roles are the only gate |
| Write mode | confirm, always | No `off`/`on` modes |

## 3. Identity and auth

Two OAuth layers, deliberately separate.

### 3.1 MCP OAuth (this server is the authorization server)

- `GET /.well-known/oauth-protected-resource` (+ `/mcp` suffix variants) —
  RFC 9728 PRM: resource = `{base}/mcp`, AS = `{base}`, scope `eve`.
- `GET /.well-known/oauth-authorization-server` — AS metadata:
  authorization/token/registration endpoints, `code` + PKCE S256,
  grants `authorization_code` and `refresh_token`.
- `POST /oauth/register` — dynamic client registration. Redirect URIs are
  filtered against the allowlist; empty result → `invalid_redirect_uri`.
- `GET /oauth/authorize?client_id&redirect_uri&state&code_challenge` —
  validates client + redirect, then **302 straight to EVE SSO** with the
  instance application. No intermediate form. Without OAuth params —
  human hint page.
- `POST /oauth/token` — `authorization_code` (verify PKCE, one-time code,
  2 min TTL) and `refresh_token` grants.
- Tokens: JWT HS256, key auto-generated on first boot and stored in
  Postgres (`app_secrets`), shared by all replicas.
  Access: `sub` = user id, `aud` = resource URL, `iss` = base, 1 h.
  Refresh: `typ: refresh`, 30 days.
- Handshake state (MCP pending authorizations, EVE PKCE verifiers,
  one-time codes) lives in Postgres so the callback and the token
  exchange may land on any replica.
- Unauthenticated `/mcp` → `401` + `WWW-Authenticate: Bearer
  resource_metadata="…", scope="eve"`.

Redirect URI allowlist (exact hosts, path prefixes):
`http://localhost:*/…` and loopback IPs (http only),
`https://{www.,}cursor.com/agents/mcp/oauth/callback`,
`https://claude.ai/api/mcp/auth_callback`. Nothing else.

### 3.2 EVE SSO (CCP is the authorization server)

Authorization-code + PKCE against `login.eveonline.com/v2/oauth/…` with
the instance `CLIENT_ID`. Callback is exactly
`{PUBLIC_URL|http://127.0.0.1:port}/auth/callback` and must match the CCP
application registration. Access JWTs are verified against CCP JWKS
(RS256/ES256, issuer + audience `EVE Online`); refresh handled per
character with a 60 s expiry margin. Refreshing takes a row lock on the
character (`SELECT … FOR UPDATE`): CCP may rotate the refresh token on
every exchange, and two replicas racing the same token would otherwise
invalidate the character's login.

### 3.3 User model

- A **user** = one browser sign-in group. Id: 16 hex chars, random.
  Users have no credentials of their own: proof of identity is always a
  successful EVE SSO login. A user is created only by a finished login —
  abandoned authorize flows create nothing.
- Storage: `users` and `characters` tables in Postgres (§8); a character
  row carries the refresh token and belongs to its user by foreign key.
- **Ownership invariant: a character belongs to exactly one user** —
  enforced by a unique key on `character_id`.
- On EVE callback for an MCP authorize flow: if the character already
  belongs to a user, the login resolves to **that** user (dedupe by
  `character_id`); otherwise a new user is created. The MCP code/tokens
  then carry that user id in `sub`. Consequence: whoever can log into
  the character's EVE account gets that user with all its characters —
  the EVE account is the root of trust.
- Alts: `eve_auth_login_url` from an authenticated session starts an EVE
  login whose pending state lives in that user's SSO client; the callback
  routes completion back to the same user (`SSOForState`). If the
  finished character already belongs to a **different** user, the add is
  refused with an actionable error ("log it out there first, or sign in
  with that character from your client"). Re-adding your own character
  just refreshes its token.
- Separate sign-ins with unrelated characters intentionally make
  separate users (the server cannot know they are one person). Manual
  merge path: `eve_auth_logout` the character in the old user, then add
  it as an alt in the new one.
- Logout: `eve_auth_logout` revokes at CCP and deletes the token.

### 3.4 Session resolution

`ProtectMCP` middleware: bearer → verify JWT → user must exist in
Postgres → per-user `session.Session` (cached in pod memory) injected
into the request context. Tools call `session.From(ctx)`; a missing
session is an auth error, never a fallback to another user. Sessions
share one HTTP client and the Postgres-backed HTTP cache; token rows and
rate-limit state are per user. EVE access tokens are cached in pod
memory only (20 min lifetime, re-derivable from the refresh token).

## 4. MCP surface

Transport: MCP Streamable HTTP (JSON-RPC) at `/mcp`, stateless mode,
implementation name `eve-online`. Client flow: `initialize` →
`tools/list` → `tools/call`.

Tool contract (enforced by `evals/run.py lint`):

- Names: `eve_<domain>_<action>`. Renaming is a breaking change.
- Every input field carries a `jsonschema` tag — the whole tag is the
  model-facing description (jsonschema-go does not parse `minimum=`).
  Integer bounds are patched centrally (`limit`/`items` 1–500, `division`
  1–7, `history_days` 0–365).
- List tools: small default `limit` + `response_format:
  "concise"|"detailed"`, concise default.
- Results: JSON as `TextContent`, `nil` structured output. **Never** add
  typed output schemas (they drop undeclared keys).
- Every ESI-backed result carries `data_age`.
- Errors are actionable sentences naming the next tool. Kinds:

| `kind` | Meaning | Extra fields |
|---|---|---|
| `AuthError` | character/scope problem | — |
| `CharacterNotFound` | bad/ambiguous `character` | — |
| `WriteBlocked` | confirm/budget refusal | — |
| `EsiRateLimited` | CCP error-limit / 420 / 429 | `retry_at`, `retry_after_seconds`, `error_limit_*` |
| `UserRateLimited` | per-user allowance spent (§5.3) | `retry_at`, `retry_after_seconds` |
| `EsiError` | other ESI 4xx/5xx | `status` |
| `Error` | anything else | — |

Domains: account/auth, character, assets, wallet, industry, market,
social, universe, corp (unlocked by in-game roles), writes (waypoint,
openwindow, fittings, mail_organize, calendar, mail_send, contacts).
All tools are always registered; there is no host-side capability gate.

### 4.1 Mutation flow

Every mutating tool, no exceptions:

1. `Guard.Authorize(tool, capability, args, preview, confirmToken, scopes)`
   — checks scope grant, per-user mail cap, confirm cycle:
   - no token → returns `status: confirmation_required`, `will_do`
     preview, single-use `confirm_token` (TTL 300 s);
   - token → must match tool **and** exact args digest, single use.
2. Execute the ESI write.
3. `Guard.Record(...)` — bump the mail counter.

### 4.2 Functional reference

Facts every rebuild needs; the per-tool catalog itself is §4.3.

**Write capabilities → EVE scopes** (all always enabled):

| Capability | Scope | Does |
|---|---|---|
| waypoint | `esi-ui.write_waypoint.v1` | autopilot waypoints |
| openwindow | `esi-ui.open_window.v1` | market/info/contract/mail windows |
| fittings | `esi-fittings.write_fittings.v1` | save/delete fittings |
| calendar | `esi-calendar.respond_calendar_events.v1` | respond to invites |
| mail_organize | `esi-mail.organize_mail.v1` | labels, read-flags, delete |
| mail_send | `esi-mail.send_mail.v1` | mail to other players |
| contacts | `esi-characters.write_contacts.v1` | contacts + standings |

**Read scopes** (requested at every login): assets, calendar,
characters.{agents_research, blueprints, contacts, corporation_roles,
fatigue, fw_stats, loyalty, medals, notifications, standings, titles},
clones{,.implants}, contracts, fittings.read, fleets.read,
industry.{jobs, mining}, killmails, location.{location, online, ship},
mail.read, markets.{orders, structure_markets}, planets.manage,
search.structures, skills.{queue, skills}, universe.structures,
wallet.read — the exact v1 identifiers live in
`internal/domain/write/policy.go` (`ReadScopes`, 33 entries).

**Corp read scopes** (11, same file) unlock `eve_corp_*`, gated by
in-game roles granted *everywhere* (HQ/base/other grants do not count;
Director passes every check):

| Corp tool area | Scope | Role required |
|---|---|---|
| assets, blueprints, killmails, divisions | assets/blueprints/killmails/divisions.v1 | Director |
| wallets (journal, transactions) | `esi-wallet.read_corporation_wallets.v1` | Accountant / Junior_Accountant |
| industry jobs | `esi-industry.read_corporation_jobs.v1` | Factory_Manager |
| orders | `esi-markets.read_corporation_orders.v1` | Accountant / Trader |
| structures | `esi-corporations.read_structures.v1` | Station_Manager |
| mining ledger / extractions | `esi-industry.read_corporation_mining.v1` | Accountant / Station_Manager |
| members | `esi-corporations.read_corporation_membership.v1` | any member (roles column: Director) |

**Resolution and market constants:**

- `/universe/names` resolves ids in batches of 900; `/universe/ids`
  (name → id) in batches of 500. Ids ≥ 10¹² are player structures and
  resolve via authenticated `/universe/structures/{id}` instead.
- Reference prices: `/markets/prices` (average/adjusted), cached 1 h;
  asset valuations use them, never live orders.
- Live quotes default to The Forge (region `10000002`) filtered to
  Jita 4-4 (station `60003760`) unless the caller widens the region.
- Wallet transactions page by cursor (`from_id` / `transaction_id`),
  2500 rows per page, ≤ 4 pages.
- Paginated corp endpoints cap pages per call: assets 80, most others
  40, wallet journal 10, mining observer detail 10.

### 4.3 Tool catalog — source of truth

The normative per-tool contract lives in two companion documents:

- **[TOOLS.md](TOOLS.md)** — every tool with its model-facing
  description and parameter schema. The implementation must match it;
  a tool change lands in the same commit as its TOOLS.md change.
- **[ESI.md](ESI.md)** — every EVE Online endpoint the server may call,
  with scope, page caps, the repo call site and the official CCP
  documentation link. Calling an endpoint not listed there is a bug.

`evals/run.py lint` checks a running server against the tool rules in
§4; the `addTool` blocks in `internal/usecase/eve/*.go` implement the
catalog.

## 5. Rate limiting

Three independent layers, all per concern:

### 5.1 Upstream ESI protection

- ETag + shared HTTP cache in Postgres; fresh cache hits never touch the
  network. Cache TTL honours `Expires`/`Cache-Control`, capped at 24 h.
- Error-limit accounting from `X-Esi-Error-Limit-Remain/Reset` on every
  response, kept per pod in memory (each pod re-learns the shared budget
  from the next response headers). When remain < 15 → fail fast with
  `EsiRateLimited` carrying `retry_at` (no sleeping inside tool calls).
- `420`/`429`: at most one short retry (≤ 2 s) on 429, then
  `EsiRateLimited` from `Retry-After` / reset headers (the longer wins).
- GET 5xx: up to 2 retries with jittered backoff, then serve stale cache
  if present.

### 5.2 Mail cap (per user)

5 outgoing mails per rolling hour, counted at `Guard.Record` time,
refused at `Guard.Authorize` time with `WriteBlocked`. Constant.

### 5.3 Per-user ESI allowance (per user) — NEW

Purpose (PRD): one looping assistant must not exhaust the shared CCP
error budget / IP for the whole friend group. Not meant to be felt in
normal use.

- Token bucket per user id, counting **network requests to ESI only**
  (cache hits and 304 revalidations that serve from cache are free).
- Capacity 400, refill 2 requests/second (≈ 7200/h sustained). A worst
  case tool call (80-page corp assets) costs ~85 tokens; a heavy
  conversation stays comfortably inside.
- On empty bucket: the tool returns `kind: UserRateLimited` with
  `retry_after_seconds` computed from the deficit; the server instructions
  tell the model to wait, not loop.
- Lives in the per-user ESI client next to the concurrency semaphore;
  state is per pod in memory. With N replicas the effective allowance is
  up to N× — accepted at this scale rather than paying for a shared
  counter on every request.

There is no general write budget: self-affecting mutations are limited
only by confirmation and the request allowance (PRD §5).

## 6. HTTP API (public listener)

Source of truth: `api/http.yaml` (OpenAPI 3.0). Generated server:
`make gen` → oapi-codegen (std-http, embedded spec) →
`internal/service/http/api.gen.go`. `/mcp` is deliberately **not** in the
YAML — it is mounted manually next to the generated mux so oapi-codegen
cannot steal the route.

| Route | Purpose |
|---|---|
| `GET /` | Human status page |
| `GET /auth/login` | Redirect to `/oauth/authorize` |
| `GET /auth/callback` | EVE SSO callback (MCP flow → 302 back to client; alt flow → success page) |
| `GET /.well-known/oauth-protected-resource` | PRM |
| `GET /.well-known/oauth-authorization-server` | AS metadata |
| `POST /oauth/register` | DCR |
| `GET /oauth/authorize` | Validate + 302 to EVE SSO |
| `POST /oauth/token` | Code / refresh exchange |
| `POST|GET /mcp` | MCP Streamable HTTP (outside OpenAPI) |

Internal listener: `GET /healthz` → `{"status":"ok"}`; `GET /metrics`
(Prometheus, future — see §11).

## 7. Go layout

```
api/                    http.yaml + http.cfg.yaml (oapi-codegen)
cmd/eve-mcp/            package main: main.go (composition root),
                        config.go (env, no prefix), service.go (launchd/systemd)
internal/
  adapter/              external systems; each package owns its Options
    esi/                ESI HTTP: cache, error limit, pagination, per-user bucket
    sso/                EVE SSO: PKCE, token exchange, JWKS verify
    store/              PostgreSQL (pgx): users, characters, oauth state,
                        confirm tokens, http cache, names, secrets; embedded
                        migrations applied at startup
    names/              id→name resolution, reference prices
  domain/               pure model, no upward imports
    character/          Token, Corporation, roles
    user/               User, NewID
    write/              capability catalog, Guard (confirm cycle, mail cap)
    universe/           reference constants (Jita, The Forge)
    j/                  map[string]any helpers
  usecase/              business logic
    session/            per-user runtime (Session, ForUser, resolution)
    oauth/              MCP authorization server + user attach/dedupe
    eve/                all MCP tools + instructions
  service/              transport; depends on usecase only
    http/               generated OpenAPI + handlers + both listeners
    mcp/                MCP server registration facade
evals/                  lint + smoke against a running server
eve_mcp/                retired Python implementation (reference only)
```

Import direction: `service → usecase → adapter | domain`; domain imports
nothing above stdlib. Process config maps to `Options` in `main` only.

Dependencies: `modelcontextprotocol/go-sdk`, `golang-jwt/jwt/v5`,
`jackc/pgx/v5`, `caarlos0/env`, `joho/godotenv`, `oapi-codegen`
(tool + runtime).

## 8. Data model (PostgreSQL)

Embedded SQL migrations, applied at startup. Sketch:

| Table | Columns (key ones) | Notes |
|---|---|---|
| `users` | `id` pk, `created_at` | one browser sign-in group |
| `characters` | `character_id` pk, `user_id` fk, `name`, `owner_hash`, `refresh_token`, `scopes`, `added_at` | pk enforces the ownership invariant; refresh takes `FOR UPDATE` |
| `oauth_clients` | `client_id` pk, `redirect_uris`, `created_at` | DCR registrations |
| `login_states` | `state` pk, `pkce_verifier`, `scopes`, `kind` (mcp/alt), `user_id` null, `mcp_client_id`, `redirect_uri`, `mcp_state`, `code_challenge`, `created_at` | one EVE handshake; TTL 15 min, any replica can finish it |
| `auth_codes` | `code` pk, `user_id`, `mcp_client_id`, `redirect_uri`, `code_challenge`, `expires_at` | one-time, 2 min |
| `confirm_tokens` | `token` pk, `user_id`, `tool`, `args_digest`, `created_at` | mutation consent, TTL 300 s |
| `mail_log` | `user_id`, `sent_at` | rolling-hour mail cap |
| `http_cache` | `key` pk, `etag`, `expires_at`, `stored_at`, `pages`, `body` | shared ESI cache |
| `names`, `blobs` | id/name/category; key/value | resolution + reference data |
| `app_secrets` | `name` pk, `value` | MCP JWT HMAC key, generated on first boot |

Expired rows (`login_states`, `auth_codes`, `confirm_tokens`, stale
`http_cache`) are purged opportunistically on access plus a periodic
sweep. No migrations from the file-based layouts — old `DATA_DIR`
content is ignored; players re-authenticate once.

## 9. ESI usage rules

- Every request: identifying `User-Agent` (with `CONTACT`, per CCP's
  developer guidelines — no contact means no warning before a ban) and
  the pinned `X-Compatibility-Date`. Moving the pinned date requires
  re-checking every response shape (baseline matches 2020-01-01; newer
  dates add routes).
- `/route/` is POST with `preference` in the body.
- ESI search is prefix-only; `eve_universe_search` shortens the prefix
  and retries.
- `/universe/names` is one shared id space; group ids resolve via
  `Resolver.GroupName`, never through `/universe/names`.
- Respect cache timers: never re-fetch before expiry (the cache layer
  guarantees this).

## 10. Deployment

- **Kubernetes (primary):** Deployment, `replicas >= 1`, no volumes; env
  (`DATABASE_URL`, `CLIENT_ID`, …) from a Secret; liveness/readiness →
  `GET /healthz` on the internal port; public port exposed via the
  existing Cloudflare tunnel; internal port has no Ingress exposure.
  Postgres is a cluster service, provisioned separately.
- **Local dev:** any reachable Postgres (e.g. `docker run postgres`),
  `DATABASE_URL` in `./.env`, `./eve-mcp` in the foreground. The
  launchd/systemd `install` path remains for a laptop instance and also
  requires a running Postgres.

## 11. Observability

- Now: `/healthz`, structured process logs to stdout/launchd log file.
- Next (internal listener): Prometheus `/metrics` — ESI requests/errors
  by status, error-limit remain gauge, cache hit ratio, per-tool call
  count + latency, per-user bucket rejections, mail-cap rejections,
  active users.

## 12. Deltas vs current code (implementation checklist)

0. **PostgreSQL storage** (§8): **done (T03–T07).** `adapter/store` is the
   only durable store; `DATABASE_URL` is required at boot.
1. **Host write policy** (§2): **done (T08).** Write/corp tools always
   registered; login always requests the full scope set; confirm 300 s
   and mail cap 5/h are constants, not env.
2. **Write budget and audit log:** **done (T08).** Guard is confirm +
   mail cap only.
3. **Per-user ESI token bucket** (§5.3): **done (T09).** Capacity 400,
   refill 2/s on the per-user ESI client; cache hits and 304-from-cache
   are free; `kind: UserRateLimited` with `retry_at`.
3a. Enforce the ownership invariant in the alt-add flow (§3.3): the EVE
   callback for a tool-started login must check `ownerOf(character_id)`
   and refuse storing a character that belongs to a different user
   (today it bypasses the dedupe and can duplicate ownership).
4. Update `.env.example`, README and `api/http.yaml` description to the
   reduced env set.
5. Metrics endpoint (§11) — separate change, after this lands.
