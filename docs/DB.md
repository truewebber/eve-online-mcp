# eve-mcp — Database Schema

**This document is normative.** PostgreSQL is the only durable store;
every table the server may touch is defined here. Changing the schema
means changing this file and adding a migration in the same commit.
Anything not listed here does not exist.

## Migrations

Managed by a migration tool (goose, `github.com/pressly/goose/v3`),
embedded SQL in `internal/adapter/store/sql/`, applied at startup.
Forward-only. The tool's own bookkeeping table is its business, not
part of this schema.

There are no migrations from earlier layouts (files, the `users`-table
era) — players re-authenticate once.

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
- All timestamps `TIMESTAMPTZ`, UTC.
- **No cache tables.** The ESI response cache, the id→name cache and
  reference prices live in pod memory (bounded, LRU). ETag revalidation
  keeps restarts cheap; replicas do not share caches and that is
  accepted (SPEC §5.1).

## Entity map

```
characters 1 ──── 0..1 sessions[live]     one active connection
characters 1 ──── * sessions[revoked]     history (soft-deleted)
sessions   1 ──── * confirm_tokens        consent, dies with the session
characters 1 ──── * mail_log              rolling mail cap
oauth_clients, login_states, auth_codes   OAuth plumbing
```

## Tables

### `characters`

The identity. One row per EVE character that ever signed in. Created on
first finished login; re-login of a soft-deleted character clears
`deleted_at` — `sub` stays stable for the character's lifetime.

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
(refresh token + scopes) produced by that same browser login. Revoking
the session revokes its EVE refresh token at CCP.

| Column | Type | Constraints | Meaning |
|---|---|---|---|
| `id` | BIGINT | PK, IDENTITY | JWT `sid` |
| `character_id` | BIGINT | NOT NULL, FK → characters | owner |
| `refresh_token` | TEXT | NOT NULL | EVE SSO refresh token of this sign-in — the crown jewels |
| `scopes` | TEXT[] | NOT NULL | scopes granted at this sign-in |
| `mcp_client_id` | TEXT | NOT NULL | DCR client that signed in |
| `client_name` | TEXT | NOT NULL default '' | from DCR ("Cursor", …) |
| `ip` | TEXT | NOT NULL default '' | creation IP (`CF-Connecting-IP`); never updated |
| `created_at` | TIMESTAMPTZ | NOT NULL default now() | — |
| `valid_til` | TIMESTAMPTZ | NOT NULL | created_at + 30 days; after that — re-login, no sliding renewal |
| `revoked_at` | TIMESTAMPTZ | NULL | soft delete; set by a replacing sign-in, logout, or owner-hash change |

Indexes:
- `sessions_one_live` — partial UNIQUE on (`character_id`)
  `WHERE revoked_at IS NULL`: at most one live session per character.
- `sessions_character` on (`character_id`).

A session is **live** iff `revoked_at IS NULL AND now() < valid_til`.
Token verification (access and refresh) requires a live session with a
matching `id`; everything else is `401` / `invalid_grant`.

**Locking rule:** the EVE token refresh runs `SELECT … FOR UPDATE` on
the session row — CCP may rotate the refresh token on every exchange,
and two replicas racing it would invalidate the grant. The rotated
token is written back in the same transaction.

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

### `login_states` — consumable

One in-flight EVE SSO handshake: written at `/oauth/authorize`, consumed
(deleted) by `/auth/callback` on any replica. TTL 15 min; expired rows
swept.

| Column | Type | Constraints | Meaning |
|---|---|---|---|
| `state` | TEXT | PK | SSO `state` (random, is the secret) |
| `pkce_verifier` | TEXT | NOT NULL | for the EVE token exchange |
| `scopes` | TEXT[] | NOT NULL | requested scopes |
| `mcp_client_id` | TEXT | NOT NULL | requesting MCP client |
| `redirect_uri` | TEXT | NOT NULL | where the client gets its code |
| `mcp_state` | TEXT | NOT NULL default '' | client's state echo |
| `code_challenge` | TEXT | NOT NULL | client's PKCE challenge |
| `created_at` | TIMESTAMPTZ | NOT NULL default now() | TTL anchor |

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
| `expires_at` | TIMESTAMPTZ | NOT NULL | — |

The token exchange is one transaction: verify PKCE → revoke the
character's previous live session (and its CCP refresh token, best
effort) → insert the new session → delete the code.

### `confirm_tokens` — consumable

Mutation consent (SPEC §4.1). Bound to the session that saw the
preview; consent dies with the session. Deleted on use; TTL 300 s.

| Column | Type | Constraints | Meaning |
|---|---|---|---|
| `token` | TEXT | PK | random, is the secret |
| `session_id` | BIGINT | NOT NULL, FK → sessions | issuing session |
| `tool` | TEXT | NOT NULL | must match at redemption |
| `args_digest` | TEXT | NOT NULL | sha256 of the exact arguments |
| `created_at` | TIMESTAMPTZ | NOT NULL default now() | TTL anchor |

### `mail_log`

Rolling-hour outgoing-mail cap (5/h per character, SPEC §5.2). Append
only; rows older than one hour are irrelevant and swept.

| Column | Type | Constraints | Meaning |
|---|---|---|---|
| `id` | BIGINT | PK, IDENTITY | — |
| `character_id` | BIGINT | NOT NULL, FK → characters | — |
| `sent_at` | TIMESTAMPTZ | NOT NULL default now() | — |

Index: `mail_log_character_sent` on (`character_id`, `sent_at`).

## What is deliberately NOT in the database

| Data | Where it lives | Why |
|---|---|---|
| MCP JWT signing key | `HMAC_KEY` env (k8s Secret), min 32 bytes | static operator-set config, not data; keeps the signing key out of DB backups; rotation = new secret + rolling restart (all clients re-authenticate, EVE grants unaffected) |
| ESI response cache (ETag, bodies) | pod memory, bounded LRU | hot, rebuildable; ETag revalidation makes cold starts cheap |
| id → name cache | pod memory | immutable data, refetch is one batch POST |
| Reference prices (`/markets/prices`) | pod memory, 1 h TTL | one small blob |
| EVE access tokens | pod memory | 20 min lifetime, re-derivable from the session's refresh token |
| Rate buckets, error-limit state | pod memory | approximations by design (SPEC §5) |

## Sweeps

| Table | Rule |
|---|---|
| `login_states` | delete where `created_at < now() - 15 min` |
| `auth_codes` | delete where `expires_at < now()` |
| `confirm_tokens` | delete where `created_at < now() - 300 s` |
| `mail_log` | delete where `sent_at < now() - 1 h` |
| `sessions` | purge soft-deleted where `revoked_at < now() - 90 days` |

## Data classification

| Sensitivity | Where |
|---|---|
| **Secret** (account access) | `sessions.refresh_token`, `auth_codes.refresh_token` |
| Personal | `sessions` metadata (client, IP), `mail_log` |
| Plumbing (short-lived secrets) | `login_states`, `auth_codes`, `confirm_tokens` |

Backups of this database are backups of account access — treat them
like the refresh tokens themselves.
