# eve-mcp — Database Schema

**This document is normative.** PostgreSQL is the only durable store;
every table the server may touch is defined here. Changing the schema
means changing this file and adding a migration in the same commit.
Anything not listed here does not exist.

## Migrations

Managed by a migration tool (goose, `github.com/pressly/goose/v3`),
SQL in `sql/` at the repository root. Forward-only. The tool's own
bookkeeping table is its business, not part of this schema.

Applying them is an operator step or CI/CD, never a path inside the
running server (RULES.md §14). `Store.Open` connects; it does not
run SQL. A lock around the apply job (if two applies can race) belongs
to that job, not to pod startup. A migration must also be readable by
the pods still serving the previous image; anything that is not is
split across two deploys.

There are no migrations from earlier layouts (files, the `users`-table
era) — players re-authenticate once. Concretely: the first goose
migration creates the schema below outright, and does not transform
anything. Deploying it onto a database from the `users` era means
dropping that database first — a one-time operator step, written down
here rather than expressed as a migration nobody will ever run twice.

## Design rules

- **Identity comes from CCP.** The character id is the user id; there is
  no separate `users` table and no invented identifiers. JWT `sub` =
  `character_id`.
- **Ids are `BIGINT`**: CCP's value where CCP issues it, `GENERATED
  ALWAYS AS IDENTITY` otherwise. The only `TEXT` keys are OAuth protocol
  artifacts that *are* random strings by design (state, code, tokens,
  DCR client id).
- **Soft deletes for entities** (`deleted_at` / `revoked_at`): rows that
  represent a character, a session or a client registration are never
  hard-deleted in normal operation. **Consumables are the exception**:
  one-time secrets (`login_states`, `auth_codes`, `confirm_tokens`) are
  deleted on use — a spent bearer string kept in the table is a
  liability, not history.
- **Every consumable carries its own `expires_at`.** Validity checks and
  the sweeper read the same column; a TTL that lives half in the schema
  and half in a constant drifts.
- All timestamps `TIMESTAMPTZ`, UTC.
- **No cache tables.** The ESI response cache, the id→name cache and
  reference prices live in process memory (bounded, LRU). ETag
  revalidation keeps restarts cheap (SPEC §5.1).

## Entity map

```
characters 1 ──── 0..1 sessions[live]     one active connection
characters 1 ──── * sessions[revoked]     history (soft-deleted)
sessions   1 ──── * confirm_tokens        consent, dies with the session
characters 1 ──── * mutations             audit log + mail cap
oauth_clients, login_states, auth_codes   OAuth plumbing
```

## Tables

### `characters`

The identity. One row per EVE character that ever signed in. Created at
the SSO callback — CCP has verified the player by then and `auth_codes`
references the row — so an abandoned token exchange leaves an identity
row with no session and no grant. Re-login of a soft-deleted character
clears `deleted_at`; `sub` stays stable for the character's lifetime.

| Column | Type | Constraints | Meaning |
|---|---|---|---|
| `character_id` | BIGINT | PK | CCP's character id; JWT `sub` |
| `name` | TEXT | NOT NULL | character name at last login |
| `owner_hash` | TEXT | NOT NULL | CCP owner hash; a change means the character was transferred to another EVE account — sessions are revoked and the row is re-owned |
| `created_at` | TIMESTAMPTZ | NOT NULL default now() | first sign-in |
| `deleted_at` | TIMESTAMPTZ | NULL | soft delete (`eve_auth_logout`) |

No EVE tokens here: the grant belongs to the sign-in, i.e. the session.

### `sessions`

One MCP connection = one EVE sign-in = one row. Carries **both** halves
of the authorization: our MCP side (`sid` in the JWTs) and the EVE grant
(refresh token + scopes) produced by that same browser login.

| Column | Type | Constraints | Meaning |
|---|---|---|---|
| `id` | BIGINT | PK, IDENTITY | JWT `sid` |
| `character_id` | BIGINT | NOT NULL, FK → characters | owner |
| `refresh_token` | TEXT | NULL | EVE SSO refresh token of this sign-in — the crown jewels. Written at creation, cleared when the session is revoked (below) |
| `scopes` | TEXT[] | NOT NULL | scopes granted at this sign-in; compared to the build's required set on every resolution (SPEC §3.5) |
| `mcp_client_id` | TEXT | NOT NULL | DCR client that signed in |
| `client_name` | TEXT | NOT NULL default '' | from DCR ("Cursor", …) |
| `ip` | TEXT | NOT NULL default '' | creation IP; never updated |
| `created_at` | TIMESTAMPTZ | NOT NULL default now() | — |
| `valid_til` | TIMESTAMPTZ | NOT NULL | created_at + 30 days; after that — re-login, no sliding renewal |
| `revoked_at` | TIMESTAMPTZ | NULL | soft delete; set by a replacing sign-in, logout, owner-hash change, or the re-authentication rule (SPEC §3.5) |

Indexes:
- `sessions_one_live` — partial UNIQUE on (`character_id`)
  `WHERE revoked_at IS NULL`: at most one unrevoked session per
  character.
- `sessions_character` on (`character_id`).

A session is **live** iff `revoked_at IS NULL AND now() < valid_til`.
Token verification (access and refresh) requires a live session with a
matching `id`; everything else is `401` / `invalid_grant`.

**Revocation rule.** Anything that replaces or ends a connection sets
`revoked_at` on **every** row of that character with `revoked_at IS
NULL`, not just the live ones. The partial unique index does not know
about `valid_til`: an expired-but-unrevoked row still occupies the
slot, so a sign-in that skipped it would fail on the unique constraint
instead of replacing it. This is the failure that shows up on day 31,
not on day 1.

Revoking also **clears the grant**: the statement that sets `revoked_at`
sets `refresh_token` to NULL in the same breath, and the value it read is
what gets revoked at CCP after the commit (SPEC §3.1). A revoked row is
history — who connected, from where, when — and history has no use for
account access. The CCP call stays best effort: if it fails we have
already stopped holding the token, which is the half that shows up in a
database backup.

**Expiry is not revocation until the sweep says so.** A row past
`valid_til` is not live, so nothing authenticates against it, but it
still holds a refresh token and still occupies the unique slot. The
expiry sweep below revokes it on exactly the terms above, so a player who
signs in once and never returns leaves nothing usable behind — the
difference between a session lifetime that is stated and one that is
enforced (AUTH.md, leak audit 4).

**Sign-in serialises on the character.** The token exchange takes
`pg_advisory_xact_lock` keyed by `character_id` as its first statement
(SPEC §3.1). Two exchanges for one character arriving together would
each revoke only the predecessors their own snapshot sees and then both
insert, and the second would collide with the first's fresh row on
`sessions_one_live`.

**Locking rule.** The EVE token refresh runs `SELECT … FOR UPDATE` on
the session row — CCP may rotate the refresh token on every exchange,
and two concurrent tool calls racing it, in one pod or two, would
invalidate the grant. The
holder re-reads the row after acquiring the lock and skips the exchange
if the token was already rotated; the rotated token is written back in
the same transaction.

**CCP-side revoke is not part of the transaction.** Revoking a refresh
token at `login.eveonline.com` happens after the commit, best effort
(SPEC §3.1): an HTTP round trip inside a transaction holds row locks
for its whole duration, and a rollback after a successful revoke would
leave a live row whose grant is already dead.

### `oauth_clients`

Dynamic client registrations (RFC 7591). `client_id` is a protocol
artifact handed to the client — a generated UUIDv7 string.

| Column | Type | Constraints | Meaning |
|---|---|---|---|
| `client_id` | TEXT | PK | issued at registration (UUIDv7) |
| `client_name` | TEXT | NOT NULL default '' | as registered |
| `redirect_uris` | TEXT[] | NOT NULL | allowlisted subset |
| `created_at` | TIMESTAMPTZ | NOT NULL default now() | — |
| `deleted_at` | TIMESTAMPTZ | NULL | soft delete |

Registration is anonymous, so the table is the one place an untrusted
caller can make rows accumulate. It is rate-limited in front (SPEC §5.5)
and swept behind (below).

### `login_states` — consumable

One in-flight EVE SSO handshake: written at `/oauth/authorize`, consumed
(deleted) by `/auth/callback`. TTL 15 min.

| Column | Type | Constraints | Meaning |
|---|---|---|---|
| `state` | TEXT | PK | SSO `state` (random, is the secret) |
| `pkce_verifier` | TEXT | NOT NULL | for the EVE token exchange |
| `scopes` | TEXT[] | NOT NULL | requested scopes |
| `mcp_client_id` | TEXT | NOT NULL | requesting MCP client |
| `redirect_uri` | TEXT | NOT NULL | where the client gets its code |
| `mcp_state` | TEXT | NOT NULL default '' | client's state echo |
| `code_challenge` | TEXT | NOT NULL | client's PKCE challenge |
| `created_at` | TIMESTAMPTZ | NOT NULL default now() | — |
| `expires_at` | TIMESTAMPTZ | NOT NULL | created_at + 15 min |

### `auth_codes` — consumable

One-time MCP authorization codes. The EVE grant obtained at the
callback is parked here until the token exchange creates the session.
Deleted on redemption; TTL 2 min.

| Column | Type | Constraints | Meaning |
|---|---|---|---|
| `code` | TEXT | PK | random, is the secret |
| `character_id` | BIGINT | NOT NULL, FK → characters | who signed in |
| `refresh_token` | TEXT | NOT NULL | EVE grant, moves into the session at exchange |
| `scopes` | TEXT[] | NOT NULL | — |
| `mcp_client_id` | TEXT | NOT NULL | must match at exchange |
| `redirect_uri` | TEXT | NOT NULL | must match at exchange |
| `code_challenge` | TEXT | NOT NULL | PKCE S256 |
| `created_at` | TIMESTAMPTZ | NOT NULL default now() | — |
| `expires_at` | TIMESTAMPTZ | NOT NULL | created_at + 2 min |

The token exchange is one transaction: verify PKCE → revoke the
character's unrevoked sessions → insert the new session → delete the
code. The predecessor's CCP refresh token is revoked after the commit.

### `confirm_tokens` — consumable

Mutation consent (SPEC §4.1). Bound to the session that saw the
preview; consent dies with the session. Deleted on use; TTL 300 s.

| Column | Type | Constraints | Meaning |
|---|---|---|---|
| `token` | TEXT | PK | random, is the secret |
| `session_id` | BIGINT | NOT NULL, FK → sessions ON DELETE CASCADE | issuing session |
| `tool` | TEXT | NOT NULL | must match at redemption |
| `args_digest` | TEXT | NOT NULL | sha256 of the exact arguments |
| `created_at` | TIMESTAMPTZ | NOT NULL default now() | — |
| `expires_at` | TIMESTAMPTZ | NOT NULL | created_at + 300 s |

`ON DELETE CASCADE` and not `SET NULL` because consent without an issuing
session is not consent. It also keeps the session purge (below) from
tripping over a foreign key: an unspent token whose session was purged
ninety days later is not a row anybody wants to reason about.

### `mutations`

The audit log: one append-only row per in-game change the server
attempted on a player's behalf (SPEC §4.1, §8). It is what makes PRD's
"nothing mutates without an explicit confirmation" checkable after the
fact, and it is what the mail cap counts (SPEC §5.4).

| Column | Type | Constraints | Meaning |
|---|---|---|---|
| `id` | BIGINT | PK, IDENTITY | — |
| `character_id` | BIGINT | NOT NULL, FK → characters | who acted |
| `session_id` | BIGINT | NULL, FK → sessions ON DELETE SET NULL | which connection asked |
| `tool` | TEXT | NOT NULL | e.g. `eve_mail_send` |
| `capability` | TEXT | NOT NULL | write capability (SPEC §4.2) |
| `args_digest` | TEXT | NOT NULL | sha256 of the exact arguments — the same digest the confirm token carried |
| `summary` | TEXT | NOT NULL | one line, ≤ 200 chars, the preview's short form ("mail to 2 recipients, subject 'Fleet tonight'") |
| `outcome` | TEXT | NOT NULL | `ok` or `error` |
| `esi_status` | INT | NULL | ESI status when there was one |
| `error` | TEXT | NULL | error kind and sentence, truncated to 200 chars |
| `created_at` | TIMESTAMPTZ | NOT NULL default now() | — |

Indexes:
- `mutations_character_created` on (`character_id`, `created_at DESC`).
- `mutations_mail_cap` — partial on (`character_id`, `created_at`)
  `WHERE tool = 'eve_mail_send' AND outcome = 'ok'`: the rolling-hour
  count is a single index scan.

The cap query and the row it authorises run under one advisory lock keyed
by the character id (SPEC §5.4), in its own key namespace, separate from
the sign-in lock. Counting outside a lock lets two concurrent sends both
see the same "4 this hour" and both go out.

**What it must not store.** No mail bodies, no contact lists, no fitting
contents — the digest identifies the arguments, the summary describes
them. A log that quotes what players write to each other is a second
copy of their mail, with none of the reasons to keep it.

Rows are written by `Guard.Record` after the ESI call, successful or
not. A mutation refused before ESI (no confirm token, cap spent) is not
a mutation and is not recorded.

## What is deliberately NOT in the database

| Data | Where it lives | Why |
|---|---|---|
| MCP JWT signing key | `HMAC_KEY` env (k8s Secret), min 32 bytes | static operator-set config, not data; keeps the signing key out of DB backups; rotation = new secret + restart (all clients re-authenticate, EVE grants unaffected) |
| ESI response cache (ETag, bodies) | pod memory, 256 MiB / 2000 entries | hot, rebuildable; ETag revalidation makes a cross-pod miss a 304, not an error |
| id → name cache | pod memory, 50 000 ids | immutable data, refetch is one batch POST |
| Reference prices (`/markets/prices`) | pod memory, 1 h TTL | one small blob |
| EVE access tokens | pod memory | 20 min lifetime, re-derivable from the session's refresh token |
| Rate buckets, error budgets, error-limit state | pod memory | approximations by design (SPEC §5); the error limit is self-correcting because CCP reports the shared remainder on every response |

## Sweeps

One goroutine per pod, every 5 minutes, the run guarded by
`pg_try_advisory_lock` so exactly one pod sweeps and the rest skip
without blocking. Every sweep is a plain `DELETE`; none of them is on a
request path.

| Table | Rule |
|---|---|
| `login_states` | delete where `expires_at < now()` |
| `auth_codes` | where `expires_at < now()`: revoke the parked refresh token at CCP, then delete |
| `confirm_tokens` | delete where `expires_at < now()` |
| `sessions` — expire | where `revoked_at IS NULL AND valid_til < now()`: set `revoked_at`, clear `refresh_token`, revoke it at CCP |
| `sessions` — purge | delete where `revoked_at < now() - 90 days` |
| `mutations` | delete where `created_at < now() - 90 days` |
| `oauth_clients` | soft-delete registrations older than 30 days that never produced a session, **and delete rows soft-deleted more than 30 days ago** |

Two of these talk to CCP, and both do it the same way: the row is
revoked locally in one transaction, and the token read there is sent to
`login.eveonline.com` afterwards, best effort, logged on failure and
never retried — the column is already NULL, and re-holding a secret to
enable a retry would undo the point. Both are batched per run so one
sweep cannot turn into a thousand serial HTTP calls.

An abandoned handshake is the reason `auth_codes` is on that list at
all: a code nobody redeemed still parked a live grant, and deleting the
row without telling CCP would leave a refresh token alive at CCP that
nothing on this side can ever use or revoke.

`oauth_clients` is the only table an unauthenticated caller can grow, so
its sweep has to actually remove rows. A soft delete alone would leave
the count monotonic and turn AUTH.md's standing requirement 4 ("it
cannot accumulate") into a wish.

`characters` is never swept: it is the identity, it holds no secret, and
a returning player must keep the same `sub`.

## Data classification

| Sensitivity | Where |
|---|---|
| **Secret** (account access) | `sessions.refresh_token` of live sessions only — revoked and expired rows hold NULL — and `auth_codes.refresh_token` for its two minutes |
| Personal | `sessions` metadata (client, IP), `characters.name`, `mutations` |
| Plumbing (short-lived secrets) | `login_states`, `auth_codes`, `confirm_tokens` |

Backups of this database are backups of account access — treat them
like the refresh tokens themselves. Restoring an old backup restores old
refresh tokens: any session revoked since then comes back, tokens and
all, so a restore is followed by revoking every session — one `UPDATE`
that sets `revoked_at` and nulls `refresh_token` — and letting players
sign in again.
