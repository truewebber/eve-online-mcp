# T15 — Sessions own the EVE grant; the runtime is keyed by `sid`

- Status: `todo`
- Size: L
- Depends on: T14
- SPEC: §3.1, §3.2, §3.4, §8, §12.2, §12.3; DB.md `sessions`

## Goal

One MCP connection = one EVE sign-in = one `sessions` row, carrying both
halves of the authorization: our `sid` and that login's EVE grant
(refresh token + granted scopes). A character has at most one live
session; signing in again moves the connection and signs the old one out.

And the half that is easy to get wrong: the per-character runtime must
keep grant state under `sid`, not under `sub`. Otherwise a fresh sign-in
is served with the predecessor's refresh token, takes `invalid_grant`,
and revokes itself — on every pod in turn (SPEC §3.4).

## Why this is one Composer session

The table, the claim, the exchange transaction and the runtime keying are
one invariant expressed in four places. Landing any subset gives a build
that authenticates but cannot say which grant it is using.

## Do not

- Revoke only *live* predecessors. Revocation is by `revoked_at IS NULL`
  alone: an expired-but-unrevoked row still occupies the partial unique
  index, so a sign-in that skipped it collides on day 31 (SPEC §3.1).
- Call CCP's revoke inside the transaction. It runs after the commit,
  best effort, failures logged and dropped.
- Skip `pg_advisory_xact_lock` on the character at the top of the
  exchange. Two sign-ins arriving together each revoke what their own
  snapshot sees and then both insert — the second fails the unique index,
  which is a `500` on a login reachable by double-clicking.
- Cache the access token or the refresh token under `sub`.
- Revoke "the character's live session" on an authorization failure.
  The verdict is charged to the `sid` that produced the request, read
  from the verified JWT.
- Keep the EVE grant on `characters`. That row holds identity only.

## Context

After T14 the grant still sits on the character row and `FOR UPDATE`
locks that row during refresh. This task moves both.

`sessions` per DB.md: `id` (identity, the `sid`), `character_id`,
nullable `refresh_token`, `scopes`, `mcp_client_id`, `client_name`, `ip`,
`created_at`, `valid_til` (created_at + 30 d), `revoked_at`. Partial
unique on `character_id WHERE revoked_at IS NULL`. Revoking clears
`refresh_token` in the same statement, and the value read there is what
gets revoked at CCP after the commit.

`confirm_tokens` gains `session_id` with `ON DELETE CASCADE`: consent
dies with the session, so a replacing sign-in voids every pending
confirmation.

Session metadata is captured once at creation and never updated. `ip`
comes from `CF-Connecting-IP` only when the public listener is
unreachable except through the tunnel; otherwise the socket address
(SPEC §3.1, §10) — T20 owns the trust rule, so take the socket address
here unless it already exists.

## Work

1. Migration: create `sessions` with its indexes; add
   `confirm_tokens.session_id` (FK, cascade); drop the grant columns from
   `characters`.
2. Store: create a session, revoke every unrevoked session of a
   character (clearing tokens and returning what was cleared), read a
   live session by id, `SELECT … FOR UPDATE` on a session with a
   post-lock re-read for the refresh.
3. Token exchange (`usecase/oauth`), one transaction:
   `pg_advisory_xact_lock(character_id)` → delete the code → revoke
   predecessors → insert the session → commit → issue JWTs with `sub`
   and `sid`. After the commit, revoke the predecessor's refresh token
   at CCP.
4. Both grants verify `sid` against a live session
   (`revoked_at IS NULL AND now() < valid_til`); refresh JWT `exp` is the
   session's `valid_til`. A dead session is `401` on `/mcp` and
   `invalid_grant` on refresh.
5. Runtime split (SPEC §3.4): allowance, error budget and the shared
   response cache under `sub`; refresh token, scopes and cached access
   token under `sid`. The runtime records its `sid` and a request with a
   different one rebuilds the grant half.
6. `eve_auth_logout` revokes the session, soft-deletes the character,
   then revokes at CCP after the commit. `eve_auth_status` reports
   `session_expires_at`.
7. Tests: two concurrent exchanges for one character both succeed, the
   second replacing the first, with no unique violation; a token whose
   `sid` was revoked gets `401`; an expired-but-unrevoked row does not
   block a new sign-in; a runtime built from session A rebuilds when a
   request carries session B; two goroutines refreshing one session
   perform one CCP exchange (the post-lock re-read).

## Files

- Edit: `internal/adapter/store/*.go`, `internal/adapter/sso/*.go`,
  `internal/usecase/oauth/oauth.go`, `internal/usecase/session/*.go`,
  `internal/service/http/handler.go`, `internal/usecase/eve/account.go`
- Add: one migration

## Acceptance

- [ ] `sessions` exists with the partial unique index; `characters`
      holds no token
- [ ] The exchange takes the advisory lock and revokes with
      `revoked_at IS NULL`, tokens cleared
- [ ] CCP revoke happens after the commit, never inside
- [ ] `sid` is in both tokens and checked on every call
- [ ] Grant state is keyed by `sid`; a request with a new `sid` rebuilds
      it, covered by a test
- [ ] An authorization failure revokes the requesting `sid` only
- [ ] Concurrent sign-in test passes; concurrent refresh test passes
- [ ] `go test ./...` passes; signing in from a second client kicks the
      first, verified by hand once

## Verify

```bash
go test ./internal/adapter/store ./internal/usecase/oauth ./internal/usecase/session -count=1
rg -n 'refresh_token' --glob '*.go'      # only sessions, never characters
```

## Done

Set `Status: done` here and in [README.md](README.md).
