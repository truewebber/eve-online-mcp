# eve-mcp — Technical Specification

Companion to [PRD.md](PRD.md). The PRD says what the product does; this
document says how to build it. Where the current code differs, the delta
is listed in §12 — the spec describes the target.

§12 is the remaining work. The board in [plan/README.md](plan/README.md)
slices finished work into Composer-sized tasks and predates §12; it is
being rebuilt from it. This file stays the contract either way.

## 1. Runtime topology

One Go binary, one container image, **N identical replicas behind one
public address**. All durable state lives in **PostgreSQL**; a pod holds
only losable caches (ESI responses, id→name, reference prices, EVE
access tokens, rate buckets, error accounting), so any pod can serve any
request — including an OAuth callback for a handshake another pod
started, or a tool call from a session another pod created.

**Correct at any replica count**, because it is enforced in Postgres and
not in memory: one live session per character (partial unique index plus
the exchange transaction, §3.1), confirm tokens, the EVE token refresh
(`FOR UPDATE` plus post-lock re-read, §3.2), the mail cap (counted and
recorded under one lock, §5.4), the audit log, migrations and sweeps
(advisory locks, DB.md).

**Per pod, and therefore approximate when N > 1:** the ESI response
cache, the per-character request allowance and the per-character error
budget (§5). None of them is a correctness boundary; a character served
by three pods gets up to three times its allowance and warms three
copies of the cache.

The fix, when N > 1 and that matters, is routing rather than shared
state: hash the request onto a pod by the `Authorization` header (nginx
ingress `upstream-hash-by: "$http_authorization"`). A bearer is stable
for the life of a session, so a character keeps landing on the same pod
and all three structures become exact again — with a warmer cache as a
side effect. Pod churn reshuffles the mapping, which costs a cold cache
and a reset counter, nothing more.

Scaling caveat: replicas do not add ESI headroom when they share an
egress IP. CCP's error limit is 100 errors per 60 s **per IP**, and
every response carries the shared remainder in
`X-Esi-Error-Limit-Remain`, so each pod reads the true global state off
its own traffic (§5.1) — that protection needs no coordination. What
more replicas buy is rolling updates, failover and CPU, not API
bandwidth. Beyond a handful of pods, add the affinity above or the cache
duplication stops paying for itself.

Each pod runs two HTTP listeners:

| Listener | Env | Default | Serves | Exposure |
|---|---|---|---|---|
| Public | `LISTEN` | `127.0.0.1:8765` | `/mcp`, OAuth endpoints, human pages | Ingress / Cloudflare tunnel |
| Internal | `INTERNAL_LISTEN` | `127.0.0.1:8766` | `/healthz`, `/metrics` (future) | Cluster-only. Never routed publicly |

Defaults bind loopback because the common local case is a laptop. Under
Kubernetes both must be set to `0.0.0.0:<port>` or the kubelet cannot
reach `/healthz` and the Service cannot reach `/mcp` (§10).

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
| `EXTRA_REDIRECT_URIS` | no | empty | Comma-separated exact redirect URIs added to the built-in allowlist (§3.1), for an MCP client we do not ship support for |
| `DATABASE_URL` | **yes** | — | PostgreSQL DSN; sole durable store (see DB.md) |
| `HMAC_KEY` | **yes** | — | MCP JWT signing key, min 32 bytes (`openssl rand -hex 32`). Rotation = new secret + restart → all clients re-authenticate |

Validation at boot, fatal on failure:

- listeners are `host:port`;
- `PUBLIC_URL`, when set, is an absolute URL and its scheme is `https`
  unless the host is loopback — authorization codes travel in browser
  URLs (AUTH.md, standing requirement 1);
- every entry of `EXTRA_REDIRECT_URIS` is an absolute URL with no
  wildcard and no fragment;
- `HMAC_KEY` decodes to at least 32 bytes.

The callback URL defaults to `http://127.0.0.1:{port}/auth/callback`
when `PUBLIC_URL` is empty. User-Agent is `eve-mcp/{version} {CONTACT}`.

**Deliberately not configurable** (constants in code, per PRD "clean
bridge, no host policy"):

| Constant | Value | Rationale |
|---|---|---|
| Compatibility date | `2026-08-18` | Changing it is a code change: every response shape must be re-verified (§9) |
| Session lifetime | 30 days | Re-login in the browser; no sliding renewal (§3.1) |
| Confirm token TTL | 300 s | Consent window for one mutation |
| Mail cap | 5 / rolling hour / character | Anti-spam toward other players (§5.4) |
| Per-character ESI allowance | bucket 400, refill 2/s | §5.2 |
| Per-character ESI error budget | min(20, ⅕ of the shared remainder) / 60 s window | §5.3 |
| ESI concurrency | 8 in flight per character | Politeness |
| HTTP timeout | 30 s | — |
| ESI response cache | 256 MiB, 2000 entries per pod, bodies > 8 MiB not cached | §5.1 |
| id→name cache | 50 000 ids per pod | §5.1 |
| Reference price cache | 1 h | §5.1 |
| Public OAuth endpoint limit | 60 requests / min / IP / pod | §5.5 |
| Audit retention | 90 days | §8, DB.md |
| Write capabilities | all registered, always | Full API, consent per call |
| Corp scopes | always requested | In-game roles are the only gate |
| Write mode | confirm, always | No `off`/`on` modes |

## 3. Identity and auth

Two OAuth layers, deliberately separate. The full channel-by-channel
map — every secret, where it travels, where it rests, and the leak
audit — is **[AUTH.md](AUTH.md)**; changing an auth flow means changing
that file in the same commit.

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
- Tokens: JWT HS256, signed with the `HMAC_KEY` env secret (§2), never
  stored in the database. Access: `sub` = **character id**, `sid` =
  session id, `aud` = resource URL, `iss` = base, 1 h. Refresh:
  `typ: refresh`, same `sub` + `sid`, `exp` = the session's `valid_til`.
- **Single active session per character.** The token exchange creates a
  `sessions` row (which also carries the EVE grant of that sign-in) and
  revokes the character's previous one. Both grants verify that the
  token's `sid` is a live session: `revoked_at IS NULL AND now() <
  valid_til` — the per-request Postgres lookup already exists, this is
  the same query. A kicked or expired client gets `401` /
  `invalid_grant` and shows Authentication required. Moving a character
  from Cursor to Claude is just signing in from Claude.
- **Revocation is by `revoked_at IS NULL` alone, never by liveness.** A
  session whose `valid_til` has passed is not live, but it still
  occupies the partial unique index (DB.md); a sign-in that only
  revoked *live* predecessors would collide with it on the 31st day.
- **The CCP revoke happens after the commit.** The database work of the
  exchange — consume the code, revoke the predecessor row, insert the
  new session — is one transaction. Telling CCP to drop the
  predecessor's refresh token is a network call and runs after that
  transaction commits, best effort, failures logged and dropped. A
  revoke inside the transaction would hold row locks for the length of
  an HTTP round trip and, on rollback, would leave a live session row
  whose grant is already dead.
- Sessions live 30 days (`valid_til`, no sliding renewal) — after that
  the player re-logs via the browser.
- Session metadata, captured once at creation: DCR `client_id` +
  `client_name`, IP, `created_at`. Nothing is updated per request. The
  IP comes from `CF-Connecting-IP` only when the public listener is
  unreachable except through the tunnel (§10); otherwise the header is
  attacker-controlled and the socket address is used.
- Handshake state (MCP pending authorizations, EVE PKCE verifiers,
  one-time codes) lives in Postgres, so the callback and the token
  exchange may land on any pod and a restart mid-sign-in is recoverable.
- Unauthenticated `/mcp` → `401` + `WWW-Authenticate: Bearer
  resource_metadata="…", scope="eve"`.

Redirect URI allowlist (exact hosts, path prefixes):
`http://localhost:*/…` and loopback IPs (http only),
`https://{www.,}cursor.com/agents/mcp/oauth/callback`,
`https://claude.ai/api/mcp/auth_callback`, plus anything in
`EXTRA_REDIRECT_URIS` (§2). Nothing else. A client whose callback is not
on that list cannot connect at all, and only the host can widen it —
that is the one piece of per-client setup the product keeps (PRD §5).

### 3.2 EVE SSO (CCP is the authorization server)

Authorization-code + PKCE against `login.eveonline.com/v2/oauth/…` with
the instance `CLIENT_ID`. Callback is exactly
`{PUBLIC_URL|http://127.0.0.1:port}/auth/callback` and must match the CCP
application registration. Access JWTs are verified against CCP JWKS
(RS256/ES256, issuer + audience `EVE Online`); refresh handled per
character with a 60 s expiry margin. Refreshing takes a row lock on the
session (`SELECT … FOR UPDATE`): CCP may rotate the refresh token on
every exchange, and two concurrent tool calls racing the same token — in
one pod or two — would otherwise invalidate the character's login. After
acquiring the lock the holder re-reads the row and skips the refresh if
someone already rotated it.

Multiple refresh-token streams for one character are expected to
coexist at CCP (a new sign-in does not invalidate the previous stream),
which is why the predecessor's token is revoked explicitly. If that
assumption ever proves wrong, the symptom is a sign-in that kills
itself, and the fix is to stop revoking the predecessor at CCP.

### 3.3 Identity model — the character is the user

- There is no `users` table and no invented user id. **The identity is
  CCP's `character_id`** (PRD: "one connection, one character"); JWT
  `sub` carries it directly. Re-login by the same character is the same
  identity by construction — no dedupe logic exists.
- Proof of identity is always a successful EVE SSO login. A `characters`
  row appears at the callback, when CCP has already verified the player
  and the grant is parked in `auth_codes` (which references it). An
  abandoned token exchange therefore leaves an identity row with no
  session and no grant — harmless, and it holds no secret.
- The row is soft-deleted by logout; a later login revives it.
- **The EVE grant belongs to the sign-in, not the character.** Each
  browser login yields a fresh refresh token + scope set; they live on
  the `sessions` row of that sign-in and are revoked with it. The
  character row holds only identity (name, `owner_hash`).
- `owner_hash` change on login = the character was sold to another EVE
  account: previous sessions are revoked, the row is re-owned. Whoever
  can log into the character's EVE account is the owner — the EVE
  account is the root of trust.
- **There is no alt flow.** No `eve_auth_login_url`, no tool-started
  EVE logins, no `character` parameter anywhere: a session always reads
  and acts as its one character. Another character = another MCP server
  entry in the client, its own sign-in, its own identity.
- Logout: `eve_auth_logout` (no arguments) revokes the session and
  soft-deletes the character row, then revokes the refresh token at CCP
  after the commit (§3.1). The connection is dead until a fresh sign-in.

### 3.4 Session resolution

`ProtectMCP` middleware: bearer → verify JWT → `sid` must be a live
session of `sub` in Postgres → per-character runtime (cached in pod
memory) injected into the request context. Tools call
`session.From(ctx)`; a missing session is an auth error, never a
fallback to another character. Runtime state is keyed by `sub` only —
the MCP client that presented the bearer never partitions anything.
Runtimes share one HTTP client and the ESI cache; the EVE grant, the
allowance and the error budget are per character. EVE access tokens are
cached in process memory only (20 min lifetime, re-derivable from the
session's refresh token under `FOR UPDATE`).

### 3.5 Re-authentication — the only way back in

There is exactly one path to a new grant: the MCP client sees `401` on
`/mcp` and runs the OAuth flow, which ends at EVE SSO. No tool can mint
a login URL (§3.3), so **any state where the session's EVE grant is
unusable must be turned into a dead session**, or the connection hangs
in a state the model cannot describe its way out of.

The server revokes the session — after which the next call gets `401` —
whenever:

- refreshing the EVE token returns `invalid_grant` (the player revoked
  the application on CCP's "authorized apps" page, or CCP dropped the
  stream);
- the login's `owner_hash` differs from the stored one (§3.3);
- the session's stored `scopes` no longer cover the set the build
  requires (`ReadScopes` ∪ `CorpReadScopes` ∪ write scopes, §4.2).

The scope rule means that adding a scope to the code signs everybody
out once, at their next call. That is intended: the alternative is
tools failing one by one with an error the player cannot act on.
Transient ESI failures (5xx, timeouts, `420`) never revoke anything —
only an authorization verdict does.

## 4. MCP surface

Transport: MCP Streamable HTTP (JSON-RPC) at `/mcp`, stateless mode,
implementation name `eve-online`. Client flow: `initialize` →
`tools/list` → `tools/call`.

Tool contract (enforced by `go run ./evals lint`):

- Names: `eve_<domain>_<action>`. Renaming is a breaking change.
- Every input field carries a `jsonschema` tag — the whole tag is the
  model-facing description (jsonschema-go does not parse `minimum=`).
  **Descriptions never contain bound syntax**: numeric bounds are
  patched onto the schema centrally in `patchBounds` (`limit` 1–500,
  `items` 1–200, `division` 1–7, `history_days` 0–365, `approved_cost`
  ≥ 0, `mail_id`/`event_id`/`fitting_id` ≥ 1, `min_value` ≥ 0,
  `standing` −10–10 — the Bounds column of TOOLS.md is the list). A
  `,minimum=` left in tag text is a lint failure: the model reads it as
  prose.
- List tools: small default `limit` + `response_format:
  "concise"|"detailed"`, concise default.
- Results: JSON as `TextContent`, `nil` structured output. **Never** add
  typed output schemas (they drop undeclared keys).
- No tool takes a `character` parameter: the session is bound to exactly
  one character (§3.3) and always acts as it.
- Every ESI-backed result carries `data_age`, a human-readable age of
  the underlying response (`"12s old"`, `"7m old"`, `"1.4h old"`). A
  result fused from several endpoints reports the oldest of them.
- Errors are actionable sentences naming the next tool. Kinds:

| `kind` | Meaning | Extra fields |
|---|---|---|
| `AuthError` | session or scope problem; the client must re-authenticate (§3.5) | — |
| `WriteBlocked` | confirm cycle or mail cap refusal | — |
| `EsiRateLimited` | CCP error-limit / 420 / 429 | `retry_at`, `retry_after_seconds`, `error_limit_*` |
| `UserRateLimited` | this character's allowance or error budget spent (§5.2, §5.3) | `retry_at`, `retry_after_seconds` |
| `EsiError` | other ESI 4xx/5xx | `status` |
| `Error` | anything else, including a name that resolved to nothing | — |

A name the server cannot resolve (item, system, contact) is an `Error`
whose sentence points at `eve_universe_search`; there is no
`CharacterNotFound` kind, because nothing takes a character any more.

Domains: account/auth, character, assets, wallet, industry, market,
social, universe, corp (unlocked by in-game roles), writes (waypoint,
openwindow, fittings, mail_organize, calendar, mail_send, contacts).
All tools are always registered; there is no host-side capability gate.

### 4.1 Mutation flow

Every mutating tool, no exceptions:

1. `Guard.Authorize(tool, capability, args, preview, confirmToken)` —
   checks the session's scope grant, the per-character mail cap and the
   confirm cycle:
   - no token → returns `status: confirmation_required`, `will_do`
     preview, single-use `confirm_token` (TTL 300 s), bound to the
     **session** that asked;
   - token → must match tool, exact args digest **and** the caller's
     `sid`, single use. Consent dies with the session: a sign-in that
     replaces the session voids every pending confirmation, and the new
     conversation starts from its own preview.
2. Execute the ESI write.
3. `Guard.Record(...)` — append the audit row (§8) with the outcome.
   The mail cap counts successful `eve_mail_send` rows in that log.

A mutation that never reached ESI (refused at step 1) is not recorded.
A mutation that reached ESI and failed is recorded with its error: the
question "did the assistant send that mail" must be answerable.

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

Seven of those have no endpoint in [ESI.md](ESI.md) today:
`read_agents_research`, `read_fatigue`, `read_fw_stats`, `read_medals`,
`read_titles`, `fleets.read_fleet`, `markets.structure_markets`. They
are requested deliberately, so that the tools that will use them ship
without signing the whole friend group out (§3.5 revokes on scope
drift). Removing one is as breaking as adding one.

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
| contracts | `esi-contracts.read_corporation_contracts.v1` | any member |
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
  description and parameter schema. It is written by hand and the
  implementation follows it: a tool change lands in the same commit as
  its TOOLS.md change, and `go run ./evals lint` compares the running
  server's `tools/list` against it (names, required/optional fields,
  types, descriptions, bounds).
- **[ESI.md](ESI.md)** — every EVE Online endpoint the server may call,
  with scope, page caps, the repo call site and the official CCP
  documentation link. Calling an endpoint not listed there is a bug.

The `addTool` blocks in `internal/usecase/eve/*.go` implement the
catalog; where they disagree with TOOLS.md, the code is wrong.

## 5. Rate limiting

Four layers below a tool call, plus one in front of the public
listener. Every counter here lives in pod memory: exact with one pod,
approximate across N unless requests are pinned by bearer (§1). Nothing
that must be exact is in this section — that is all in Postgres.

### 5.1 Upstream ESI protection

- ETag + **in-memory** HTTP cache, bounded LRU: 256 MiB and 2000
  entries per pod, whichever binds first; a body over 8 MiB is served
  but not stored (one corp-assets sweep must not evict everything else).
  No cache tables in Postgres — DB.md "what is deliberately not in the
  database". Fresh hits never touch the network; TTL honours
  `Expires`/`Cache-Control`, capped at 24 h. Restarts and cross-pod
  misses are cheap: revalidation with the stored ETag costs a 304, not
  an error, and 304s cost nothing against CCP's error limit. Memory
  budget is per pod, so N pods hold up to N copies of the hot set.
- Id→name (bounded at 50 000 ids, immutable data) and reference prices
  (one blob, 1 h) are in-memory the same way.
- Error-limit accounting from `X-Esi-Error-Limit-Remain/Reset` on every
  response. When remain < 15 → fail fast with `EsiRateLimited` carrying
  `retry_at` (no sleeping inside tool calls). This is the last line of
  defence and it is shared by everyone, which is why §5.3 exists to
  keep one character from getting us there.
- `420`/`429`: at most one short retry (≤ 2 s) on 429, then
  `EsiRateLimited` from `Retry-After` / reset headers (the longer wins).
- GET 5xx: up to 2 retries with jittered backoff, then serve stale cache
  if present.

### 5.2 Per-character ESI allowance

Purpose (PRD): one looping assistant must not monopolise the shared
pipe. Not meant to be felt in normal use.

- Token bucket per character, counting **network requests to ESI only**
  (cache hits and 304 revalidations that serve from cache are free).
- Capacity 400, refill 2 requests/second (≈ 7200/h sustained). A worst
  case tool call (80-page corp assets) costs ~85 tokens; a heavy
  conversation stays comfortably inside.
- On empty bucket: `kind: UserRateLimited` with `retry_after_seconds`
  computed from the deficit; the server instructions tell the model to
  wait, not loop.
- Lives in the per-character ESI client next to the concurrency
  semaphore, i.e. in pod memory. Without bearer affinity (§1) a
  character served by N pods holds N buckets. Accepted: the allowance is
  deliberately generous, and the layer that actually protects CCP is the
  error budget below, which self-scales.

### 5.3 Per-character ESI error budget

The allowance above counts successful traffic, and CCP's scarce
resource is the opposite: an error limit of 100 responses per 60 s
window **for the whole IP**. Without attribution, one player whose
assistant hammers an endpoint their roles do not allow spends everyone's
budget and §5.1 shuts the instance down for the household — exactly
what PRD §5 promises will not happen.

- Every ESI response with status ≥ 400 is charged to the character
  whose tool call produced it (`420` and `429` included: they mean the
  budget is already gone).
- Budget: **the lesser of 20 errors and one fifth of the shared
  remainder** last reported by `X-Esi-Error-Limit-Remain`, per rolling
  60 s window per character. On a healthy window that is 20 — a fifth of
  CCP's 100. As the shared budget drains it tightens everywhere at once,
  which is what makes the rule survive N pods without a shared counter:
  each pod reads the same global remainder off its own responses, so
  three pods serving one greedy character all clamp together instead of
  each granting a private 20.
- Over budget → `kind: UserRateLimited` with `retry_at` at the end of
  the window, and no request leaves the pod. Other characters are
  unaffected.
- The global fail-fast in §5.1 stays as the backstop for the case the
  attribution misses (e.g. errors caused by CCP itself).

### 5.4 Mail cap (per character)

5 outgoing mails per rolling hour, counted from the audit log (§8) at
`Guard.Record` time, refused at `Guard.Authorize` time with
`WriteBlocked`. Only successful sends count. Constant.

Unlike everything else in §5 this one is exact, because it is a database
count and not a memory counter — but only if the count and the insert
happen under one lock on the character (`SELECT … FOR UPDATE` on the
`characters` row, or an advisory lock keyed by character id). Two
concurrent sends that both read "4 this hour" would otherwise both go
out, and that is reachable with two goroutines in one pod, never mind
two pods.

There is no general write budget: self-affecting mutations are limited
only by confirmation and the request allowance (PRD §5).

### 5.5 Public endpoint protection

`/oauth/register`, `/oauth/authorize`, `/oauth/token` and
`/auth/callback` are unauthenticated by construction and two of them
write rows. Each caller IP gets 60 requests per minute across those
routes; over that, `429` with `Retry-After`. Combined with the sweeps in
DB.md (`login_states` 15 min, `auth_codes` 2 min, registrations that
never produced a session 30 days) this bounds what an anonymous caller
can accumulate in the database.

The caller IP is resolved exactly as in §3.1 — `CF-Connecting-IP` when
the listener is only reachable through the tunnel, the socket address
otherwise. Behind a tunnel every request shares one socket address, so
using it would turn a per-IP limit into a per-household one and the
first friend to open their laptop would lock out the rest.

Like the rest of §5 this counter is per pod, so the effective ceiling is
the limit times the number of pods an attacker's requests spray across.
That is fine for what it defends: the point is bounding row growth, and
the sweeps behind it are what actually keep the tables finite.

## 6. HTTP API (public listener)

Source of truth: `api/http.yaml` (OpenAPI 3.0). Generated server:
`make gen` → oapi-codegen (std-http, embedded spec) →
`internal/service/http/api.gen.go`. `/mcp` is deliberately **not** in the
YAML — it is mounted manually next to the generated mux so oapi-codegen
cannot steal the route.

| Route | Purpose |
|---|---|
| `GET /` | Human status page: version, whether ESI is reachable, how to connect. No character data, no counts — it is world-readable |
| `GET /auth/login` | Redirect to `/oauth/authorize` |
| `GET /auth/callback` | EVE SSO callback → 302 back to the MCP client |
| `GET /.well-known/oauth-protected-resource` | PRM |
| `GET /.well-known/oauth-authorization-server` | AS metadata |
| `POST /oauth/register` | DCR |
| `GET /oauth/authorize` | Validate + 302 to EVE SSO |
| `POST /oauth/token` | Code / refresh exchange |
| `POST\|GET /mcp` | MCP Streamable HTTP (outside OpenAPI) |

Internal listener: `GET /healthz` → `{"status":"ok"}`; `GET /metrics`
(Prometheus, future — see §11).

## 7. Go layout

```
api/                    http.yaml + http.cfg.yaml (oapi-codegen)
cmd/eve-mcp/            package main: main.go (composition root),
                        config.go (env, no prefix), service.go (launchd/systemd)
internal/
  adapter/              external systems; each package owns its Options
    esi/                ESI HTTP: in-memory cache, error limit + budget,
                        pagination, per-character bucket
    sso/                EVE SSO: PKCE, token exchange, JWKS verify
    store/              PostgreSQL (pgx + goose migrations): characters,
                        sessions, oauth state, confirm tokens, audit log
                        — see DB.md
    names/              id→name resolution, reference prices (memory-cached)
  domain/               pure model, no upward imports
    character/          Corporation, roles
    write/              capability catalog, Guard (confirm cycle, mail cap)
    universe/           reference constants (Jita, The Forge)
    j/                  map[string]any helpers
  usecase/              business logic
    session/            per-character runtime (Session, resolution)
    oauth/              MCP authorization server + sessions
    eve/                all MCP tools + instructions
  service/              transport; depends on usecase only
    http/               generated OpenAPI + handlers + both listeners
    mcp/                MCP server registration facade
evals/                  Go: lint + smoke against a running server
```

Import direction: `service → usecase → adapter | domain`; domain imports
nothing above stdlib. Process config maps to `Options` in `main` only.

Dependencies: `modelcontextprotocol/go-sdk`, `golang-jwt/jwt/v5`,
`jackc/pgx/v5`, `pressly/goose` (migrations), `caarlos0/env`,
`joho/godotenv`, `oapi-codegen` (tool + runtime).

## 8. Data model (PostgreSQL)

The full normative schema — every column, constraint, index, TTL and
sweep rule — is **[DB.md](DB.md)**; a schema change lands in the same
commit as its DB.md change and migration (goose, embedded SQL, applied
at startup under a Postgres advisory lock).

Shape in one breath: `characters` (identity = CCP id, soft-deleted) →
`sessions` (one live per character; carries the sign-in's EVE grant,
`valid_til` 30 d, `FOR UPDATE` around refresh) → consumables
(`login_states`, `auth_codes`, `confirm_tokens` — deleted on use) +
`oauth_clients`, `mutations`. **No cache tables and no secrets tables**:
ESI responses, names and reference prices live in pod memory (§5.1);
the JWT key is the `HMAC_KEY` env. No migrations from earlier layouts —
players re-authenticate once.

`mutations` is the audit log: one append-only row per in-game change the
server attempted, with the character, the session, the tool, the
arguments digest, a short summary and the outcome. It answers "what did
the assistant actually do" (PRD §8 success criterion) and it is where
the mail cap counts from (§5.4). It stores no message bodies.

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
- Every response, success or failure, updates the error-limit state and
  charges the calling character's error budget when it is ≥ 400 (§5.3).

## 10. Deployment

- **Kubernetes (primary):** Deployment, `replicas >= 1`, rolling
  updates, no volumes; env (`DATABASE_URL`, `CLIENT_ID`, `HMAC_KEY`, …)
  from a Secret; `LISTEN` and `INTERNAL_LISTEN` set to `0.0.0.0:{port}`
  so the kubelet and the Service can reach them; liveness/readiness →
  `GET /healthz` on the internal port; internal port has no Ingress
  exposure. Postgres is a cluster service, provisioned separately.
- **Above one replica:** hash `/mcp` onto pods by the `Authorization`
  header (nginx ingress `upstream-hash-by: "$http_authorization"`) so a
  character keeps its cache and its counters (§1). Without it the
  service still works, with each character's allowance multiplied by the
  number of pods it lands on. Size the pool: every pod opens its own
  pgx pool, so `pool_max_conns × replicas` must stay inside Postgres's
  `max_connections`, and past a few dozen pods that means PgBouncer.
- **Reaching the public listener:** the Cloudflare tunnel is the only
  intended path in. If the tunnel runs as a sidecar in the same pod,
  keep `LISTEN` on loopback and treat `CF-Connecting-IP` as trusted
  (§3.1); if it terminates elsewhere, the Service must not be reachable
  from anywhere but the tunnel, and until that is true the header is
  ignored in favour of the socket address.
- **Local dev:** any reachable Postgres (e.g. `docker run postgres`),
  `DATABASE_URL` in `./.env`, `./eve-mcp` in the foreground. The
  launchd/systemd `install` path remains for a laptop instance and also
  requires a running Postgres.
- **Rollout:** migrations are forward-only and run at startup under an
  advisory lock — with rolling updates several pods start at once, so
  the lock is load-bearing, not paranoia. A migration must therefore be
  compatible with the pods still serving the previous image; anything
  that is not gets split across two deploys.

## 11. Observability

- Now: `/healthz`, structured process logs to stdout/launchd log file,
  and the `mutations` audit log (§8), which is queryable with SQL and is
  the only durable record of in-game changes.
- Next (internal listener): Prometheus `/metrics` — ESI requests/errors
  by status, error-limit remain gauge, cache hit ratio and bytes held,
  per-tool call count + latency, per-character allowance and error-budget
  rejections, mail-cap rejections, mutations by tool and outcome, active
  sessions, sweep run age. Everything except the database-derived series
  is per pod and must be summed across them; a cache hit ratio averaged
  over pods without weighting is a lie when replicas roll.

## 12. Deltas vs current code (implementation checklist)

Done and holding: PostgreSQL as the durable store (T03–T07); write and
corp tools always registered, confirm 300 s and mail cap 5/h as
constants, no write budget (T08); per-character ESI token bucket 400 /
2 per s with `UserRateLimited` (T09). The alt-add ownership refuse (T10)
is superseded by item 1 below and goes away with it.

Remaining, in dependency order:

1. **The character is the user** (§3.3): drop the `users` table and
   `domain/user`; JWT `sub` = `character_id`; drop `eve_auth_login_url`
   and every tool-started EVE login (`login_states.kind`, `SSOForState`,
   the T10 refuse path); remove the `character` parameter from all
   tools and character resolution from `usecase/session`;
   `eve_auth_status` reports the one character; `eve_auth_logout` takes
   no arguments; drop the `CharacterNotFound` error kind (§4).
2. **Sessions own the EVE grant** (§3.1, §8, DB.md): `sessions` table
   (BIGINT identity ids, `refresh_token` + `scopes` on the session,
   `valid_til` 30 d, creation-only metadata, partial unique "one live
   per character"); `sid` claim in access + refresh tokens; the token
   exchange revokes every predecessor with `revoked_at IS NULL` inside
   the transaction and calls CCP's revoke after the commit; confirm
   tokens carry `session_id` (§4.1). `FOR UPDATE` moves from
   `characters` to `sessions`, with the post-lock re-read (§3.2).
3. **Schema per DB.md**: migrate the migrator to goose with an advisory
   lock; soft deletes on entities; drop `users`, `http_cache`, `names`,
   `blobs` and `app_secrets`; add `sessions` and `mutations`; the JWT
   key moves to the required `HMAC_KEY` env (§2).
4. **In-memory caches** (§5.1): ESI responses (256 MiB / 2000 entries /
   8 MiB body cap), id→name (50 000), reference prices (1 h) become
   bounded structures in `adapter/esi` and `adapter/names`.
5. **Re-authentication rule** (§3.5): revoke the session on
   `invalid_grant`, on `owner_hash` change and on scope drift, so the
   client is forced through `401` → OAuth. This is what makes the
   product recoverable: items 1–4 without it leave connections whose
   only cure is deleting the server entry in the client and adding it
   again.
6. **Audit log** (§8, §4.1, §5.4): `mutations` table, `Guard.Record`
   writes outcome rows, the mail cap counts from it, `mail_log` goes
   away, `eve_auth_status` reports remaining sends from it.
7. **Per-character error budget** (§5.3): attribute every ≥ 400 ESI
   response to its character, 20 per 60 s, `UserRateLimited`.
8. **Config and edges** (§2, §5.5, §10): `EXTRA_REDIRECT_URIS`,
   `PUBLIC_URL` HTTPS validation, `HMAC_KEY` length check, the per-IP
   limit on public OAuth routes, the `CF-Connecting-IP` trust rule, the
   sweep runner under its advisory lock (DB.md), the `0.0.0.0` binds and
   — above one replica — the bearer-hash affinity annotation (§1).
9. **Catalog conformance** (§4.3): implement TOOLS.md as written — 50
   tools, no `character`, no bound syntax in descriptions — and extend
   `evals lint` to diff the live `tools/list` against TOOLS.md.
10. Update `.env.example` and the `api/http.yaml` description to the env
    set and topology above (`README.md` is already in step).
11. Metrics endpoint (§11) — separate change, after everything else
    lands.
