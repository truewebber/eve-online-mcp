# T05 — Users, characters, SSO refresh in Postgres

- Status: `todo`
- Size: L
- Depends on: T03 (store API). Can land with or after T04.
- SPEC: §3.2 (refresh + `FOR UPDATE`), §3.3 (user model), §8

## Goal

A character row in Postgres is the source of truth for refresh tokens.
File layout `users/{id}/user.toml` + `tokens.json` goes away. Refresh
takes `SELECT … FOR UPDATE` so two replicas cannot rotate the same CCP
refresh token.

## Why this is one Composer session

SSO + user store + `session.ForUser` are one seam. OAuth handshake
(pending codes, HMAC key) stays files/memory until T06.

## Do not

- Move MCP `login_states` / `auth_codes` / `hmac.key` (T06).
- Drop `DATA_DIR` from config yet if OAuth still writes `oauth/hmac.key`
  there (T06/T07). You may keep `DATA_DIR` only for leftover OAuth files.
- Change write-policy env vars (T08).
- Implement the alt-add error string in full if `FinishEVE` still uses
  in-memory pending — but **do** persist characters with PK
  `character_id` so a second insert cannot succeed. The user-facing
  refuse is T10.

## Context

Today:

- `adapter/user.Store` — directories + TOML; `User.Dir` is the path
- `sso.TokenStore` — JSON file; `OpenStore("")` is memory (MCP broker)
- `session.ForUser(dataDir)` clones a session with that token file
- `oauth.SessionFor` uses `users.Dir(userID)`
- `oauth.ownerOf` scans every `tokens.json`

Target:

- `User` has `ID` + `CreatedAt` only (drop `Dir`)
- `sso.TokenStore` (or a new type) loads/saves via `store.Store` for
  a given `userID`
- Access tokens stay **in pod memory** (SPEC §3.4); only refresh tokens
  are durable
- `WithCharacterForUpdate` wraps CCP refresh in `adapter/sso`

## Work

1. Replace `adapter/user` file store with calls to `adapter/store`.
   Delete `internal/adapter/user/store.go` (or shrink it to a facade
   that only delegates — prefer delete).
2. Change `sso.Client` token persistence: `Get`/`All`/`Upsert`/`Delete`
   go through Postgres. In-memory access token + expiry stay as they are.
3. On refresh: `WithCharacterForUpdate` then CCP token endpoint, then
   write the possibly rotated refresh token in the same transaction.
4. `session.ForUser(userID string)` — not a filesystem path.
5. `oauth.ownerOf` uses `store.OwnerOf`.
6. `ProtectMCP` / `Create` user on first MCP login still work.
7. Drop `github.com/pelletier/go-toml` from `go.mod` if unused (`go mod tidy`).
8. Tests: unique character_id; refresh callback sees the locked row
   (can be a store test if SSO is hard to mock — at least unit-test the
   lock helper).

## Files

- Edit: `adapter/sso/sso.go`, `usecase/session/session.go`,
  `usecase/oauth/oauth.go`, `domain/user/user.go`, `cmd/eve-mcp/main.go`
- Delete: `internal/adapter/user/`
- Maybe: `go.mod` / `go.sum`

## Acceptance

- [ ] No `user.toml` / `tokens.json` reads or writes
- [ ] No `User.Dir`
- [ ] Character PK uniqueness covered by a test
- [ ] Refresh path uses `FOR UPDATE`
- [ ] MCP login still attaches a character to a user (manual or a
      focused test around `ownerOf` + upsert)
- [ ] `go test ./...` passes

## Verify

```bash
rg -n 'tokens\.json|user\.toml|pelletier/go-toml' --glob '*.go'
go test ./...
make postgres && go build -o eve-mcp ./cmd/eve-mcp
```

## Done

Set `Status: done` here and in [README.md](README.md).
