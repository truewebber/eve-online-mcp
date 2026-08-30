# T03 — `adapter/store` (pgx + migrations)

- Status: `done`
- Size: L
- Depends on: T02
- SPEC: §8 (tables), §12.0 (replace SQLite + files). **Do not wire
  this package into `main` yet.**

## Goal

A tested PostgreSQL adapter with embedded migrations and CRUD for every
table in SPEC §8. The running server still uses files/SQLite. Later
tasks only call this API.

## Why this is one Composer session

Greenfield package, schema is already written in SPEC §8. No OAuth or
tool changes. Hard part is a boring, complete API plus tests against
Compose Postgres.

## Do not

- Change `cmd/eve-mcp`, `usecase/`, or `adapter/cache` / `adapter/user`.
- Import `store` from other packages in this task.
- Add `golang-migrate` unless you have a strong reason; SPEC wants
  embedded SQL applied at `Open`.
- Migrate existing `DATA_DIR` contents.
- Use the `database/sql` + `pgx` stdlib driver if `jackc/pgx/v5` pool
  is enough — SPEC names `jackc/pgx/v5`.

## Schema (authoritative)

One initial migration is enough (no production PG data yet). Include:

| Table | Keys / notes |
|---|---|
| `users` | `id` text PK, `created_at` timestamptz |
| `characters` | `character_id` bigint PK, `user_id` FK → `users`, `name`, `owner_hash`, `refresh_token`, `scopes` (text[] or jsonb), `added_at`. PK **is** the ownership invariant. |
| `oauth_clients` | `client_id` PK, `redirect_uris`, `created_at` |
| `login_states` | `state` PK; PKCE verifier; `scopes`; `kind` in (`mcp`,`alt`); `user_id` nullable; MCP client/redirect/state/challenge; `created_at`. TTL 15 min at read time. |
| `auth_codes` | `code` PK; `user_id`; MCP client/redirect/challenge; `expires_at` (2 min) |
| `confirm_tokens` | `token` PK; `user_id`; `tool`; `args_digest`; `created_at` (TTL 300 s) |
| `mail_log` | `user_id`, `sent_at` — index `(user_id, sent_at)` |
| `http_cache` | `key` PK; `etag`; `expires_at`; `stored_at`; `pages`; `body` |
| `names` | `id` bigint PK; `name`; `category` |
| `blobs` | `key` PK; `stored_at`; `value` |
| `app_secrets` | `name` PK; `value` bytea |

`Open` applies migrations, then returns a pool. Opportunistic purge of
expired `login_states`, `auth_codes`, `confirm_tokens`, stale
`http_cache` can be a `PurgeExpired` method (called later from `main` /
a ticker). Implement it here and test it.

## API shape (keep names stable for T04–T07)

Package `eve-mcp/internal/adapter/store`. Suggested methods:

```
Open(ctx, databaseURL) (*Store, error)
Close()

CreateUser / GetUser / UserExists

UpsertCharacter(ctx, userID, row) error
GetCharacter / ListCharacters(userID) / DeleteCharacter
OwnerOf(characterID) (userID string, ok bool, err error)
WithCharacterForUpdate(ctx, characterID, fn) error
  // SELECT … FOR UPDATE, pass current refresh token into fn,
  // write the returned refresh token (CCP may rotate).

PutClient / GetClient
PutLoginState / GetLoginState / DeleteLoginState
PutAuthCode / TakeAuthCode          // one-time
GetOrCreateSecret(name) ([]byte, error)  // generate on first boot

PutConfirmToken / TakeConfirmToken  // one-time, honour TTL
CountMailSince(userID, since) / InsertMail

CacheGet / CachePut / CacheTouch / CachePurgeExpired
NameGet / NamePut
BlobGet / BlobPut
```

Use `context.Context` on every DB call. Domain types (`domain/user.User`,
SSO character fields) may be used; do not import `usecase`.

`domain/user.User.Dir` is a file-layout leftover — do not require it in
new rows. Leave the field on the struct for T05 to delete.

## Tests

`go test ./internal/adapter/store`:

- Skip with a clear message if `DATABASE_URL` is unset.
- Against Compose: create user, upsert character, `OwnerOf`, unique
  `character_id` conflict, `WithCharacterForUpdate` serializes two
  updaters, one-time auth code, confirm TTL, cache get/put/freshness,
  `GetOrCreateSecret` stable across Open.

Makefile: `test-store` that runs `make postgres` then
`DATABASE_URL=… go test ./internal/adapter/store -count=1`.

## Work

1. `go get github.com/jackc/pgx/v5` (and `pgxpool`).
2. Implement the package + `sql/*.sql` embedded FS.
3. Tests + Makefile target.
4. Do not remove `modernc.org/sqlite` yet (T04/T07).

## Files

- Create: `internal/adapter/store/**`
- Edit: `go.mod`, `go.sum`, `Makefile`

## Acceptance

- [x] `make test-store` passes on a clean Compose Postgres
- [x] Unique `character_id` is enforced by the database
- [x] No package outside `adapter/store` imports it yet
- [x] `go build ./cmd/eve-mcp` still works without `DATABASE_URL`

## Verify

```bash
make postgres
make test-store
go test ./...
go build ./cmd/eve-mcp
```

## Done

Set `Status: done` here and in [README.md](README.md).
