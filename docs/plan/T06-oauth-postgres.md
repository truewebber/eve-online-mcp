# T06 — OAuth handshake state in Postgres

- Status: `todo`
- Size: L
- Depends on: T05
- SPEC: §3.1 (handshake on any replica), §8 (`login_states`,
  `auth_codes`, `oauth_clients`, `app_secrets`)

## Goal

MCP OAuth no longer keeps pending EVE logins, one-time codes, DCR
clients, or the JWT HMAC key on local disk / in process maps. Any
replica can finish a callback started on another (SPEC §1, §3.1).

## Why this is one Composer session

`internal/usecase/oauth/oauth.go` is the whole seam. Character storage
is already Postgres (T05). Confirm tokens stay in-memory until T07.

## Do not

- Change tool registration or ESI.
- Drop `DATA_DIR` if Guard still writes `audit.jsonl` (T07/T08). After
  this task nothing under `oauth/` on disk should be required; if
  `DATA_DIR` is unused, you may stop creating `oauth/` directories.
- Loosen the redirect-URI allowlist (`redirect_test.go` stays).
- Put CCP `CLIENT_SECRET` in `app_secrets`.

## Context

Today in `oauth.Server`:

- `hmacKey` from `DATA_DIR/oauth/hmac.key`
- `clients` map + `clients.json`
- `pending` map (`evePending`) for MCP authorize
- `codes` map (`authCode`, 2 min)
- Alt logins: `SSOForState` walks in-memory sessions’ `HasPending`

Target:

- HMAC: `GetOrCreateSecret("mcp_jwt_hmac")` (32+ random bytes)
- DCR: `oauth_clients` table
- MCP pending: `login_states` with `kind=mcp` and the MCP PKCE fields
- Alt pending: `login_states` with `kind=alt` and `user_id` set when
  `eve_auth_login_url` starts (so callback does not depend on which
  pod holds the session)
- Auth codes: `TakeAuthCode` one-time

`SSOForState` / in-memory PKCE verifiers on `sso.Client` must not be
the only copy: store `pkce_verifier` in `login_states` and complete
the EVE token exchange using that row.

TTL: 15 min login, 2 min codes — reject on read; `PurgeExpired` on
access or a small ticker.

Keep JWT TTLs: access 1 h, refresh 30 d, HS256, `sub` = user id.

## Work

1. Persist and load the four concerns above via `adapter/store`.
2. Remove `loadOrCreateKey` file I/O, `saveClients` / `clients.json`.
3. `eve_auth_login_url`: write `kind=alt` login state with the current
   user id before returning the URL.
4. `FinishEVE`: MCP path unchanged in product behaviour (dedupe via
   `OwnerOf`, else `CreateUser`); alt path is T10 for the refuse
   message, but the state machine must already distinguish `mcp` vs
   `alt` from the row.
5. Expand `internal/usecase/oauth/redirect_test.go` if you touch
   allowlist helpers; add tests for PKCE code exchange against store
   (table-driven, Compose `DATABASE_URL`).

## Files

- Edit: `usecase/oauth/oauth.go`, `usecase/oauth/redirect_test.go`,
  `adapter/sso/sso.go` (pending login may move out),
  `usecase/eve/account.go` (login_url must record alt state),
  `cmd/eve-mcp/main.go` if construction changes
- Delete: any `hmac.key` helpers that only exist for files

## Acceptance

- [ ] No `oauth/hmac.key` or `oauth/clients.json` I/O
- [ ] `login_states` + `auth_codes` used on authorize / callback / token
- [ ] Redirect allowlist tests still pass
- [ ] Alt login state includes `user_id` so a second process can finish it
- [ ] `go test ./internal/usecase/oauth ./internal/adapter/store` pass

## Verify

```bash
rg -n 'hmac\.key|clients\.json' --glob '*.go'
go test ./...
```

Manual: MCP authorize still 302s to EVE SSO; callback without a valid
state is a clear HTML error.

## Done

Set `Status: done` here and in [README.md](README.md).
