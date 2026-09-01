# T17 — Sessions own the EVE grant; the runtime is keyed by `sid`

- Status: `done`
- Size: L
- Depends on: T16
- RULES: §2 (constraints are not control flow), §11 (`domain/session` +
  `pgx`), §12 (declared SQL), §1 (no clock), §10 (one result),
  §13 (generated mocks), §5 (tests)
- SPEC: §3.1, §3.2, §3.4, §8, §12.2, §12.3; DB.md `sessions`
- Replaces: old T15

## Goal

One MCP connection = one EVE sign-in = one `sessions` row, carrying both
halves of the authorization: our `sid` and that login's EVE grant
(refresh token + granted scopes). A character has at most one live
session; signing in again moves the connection and signs the old one
out.

And the half that is easy to get wrong: the per-character runtime must
keep grant state under `sid`, not under `sub`. Otherwise a fresh sign-in
is served with the predecessor's refresh token, takes `invalid_grant`,
and revokes itself — on every pod in turn (SPEC §3.4).

Moving the grant also settles what `adapter/sso` is. Today its `Client`
interface welds two things together: four methods that talk to CCP
(`PrepareLogin`, `ExchangeCode`, `AccessToken`, `Revoke`) and six that
talk to our `characters` table (`Upsert`, `Get`, `Remove`, `All`,
`FindByName`, `ForUser`), with `sso.go` importing `domain/character` to
say so. T14 deletes three of the six. This task deletes the rest: an
adapter models a system we do not own, and RULES §11 is explicit that
nobody implements someone else's repository. The grant is read, locked
and written by `usecase/session`; the SSO client only speaks to CCP.

## Why this is one Composer session

The table, the claim, the exchange transaction and the runtime keying
are one invariant expressed in four places. SPEC §12 lists them as items
2 and 3, but landing either alone gives a build that authenticates and
cannot say which grant it is using — which is the bug, not a step
toward fixing it.

## Do not

- **Catch a unique violation on `sessions_one_live`.** RULES §2: the
  statement wins the race, never the error handler. `pg_advisory_xact_lock`
  on the character plus revoke-then-insert is what makes `23505`
  unreachable; a `pgerrcode.UniqueViolation` branch means the lock is
  wrong and the branch is hiding it.
- Revoke only *live* predecessors. Revocation is by `revoked_at IS NULL`
  alone: an expired-but-unrevoked row still occupies the partial unique
  index, so a sign-in that skipped it collides on day 31 (SPEC §3.1).
- Call CCP's revoke inside the transaction. It runs after the commit,
  best effort, failures logged and dropped.
- Skip `pg_advisory_xact_lock` on the character at the top of the
  exchange. Two sign-ins arriving together each revoke what their own
  snapshot sees and then both insert — the second fails the unique
  index, which is a `500` on a login reachable by double-clicking.
- Cache the access token or the refresh token under `sub`.
- Revoke "the character's live session" on an authorization failure. The
  verdict is charged to the `sid` that produced the request, read from
  the verified JWT.
- Keep the EVE grant on `characters`. That row holds identity only.
- Give the session a clock. `valid_til` is a `time.Time` column and
  `now()` is Postgres; liveness is a `WHERE` clause, not an injected
  instant (RULES §1). The concurrency tests use a real database, so they
  are not inside a `synctest` bubble.
- Return `(session, tokens, error)` from anything. A revoke that must
  report which rows it cleared and what they held returns one named
  struct (RULES §10).
- Put session SQL anywhere but `internal/domain/session/pgx`, as
  declared consts (RULES §11, §12).
- Re-point `adapter/sso`'s token store from `character.Repository` to
  `session.Repository`. That is the same inversion with a new table. The
  repository leaves the adapter entirely; what stays is a client that
  takes a refresh token in and gives a token back.
- Leave `adapter/sso` importing `internal/domain`. When the last import
  is gone, the `depguard` rule that keeps it gone lands in the same
  commit — a boundary nothing enforces is a boundary that comes back.

## Context

After T14 the grant still sits on the character row and `FOR UPDATE`
locks that row during refresh. This task moves both.

`sessions` per DB.md: `id` (identity, the `sid`), `character_id`,
nullable `refresh_token`, `scopes`, `mcp_client_id`, `client_name`,
`ip`, `created_at`, `valid_til` (created_at + 30 d), `revoked_at`.
Partial unique on `character_id WHERE revoked_at IS NULL`. Revoking
clears `refresh_token` in the same statement, and the value read there
is what gets revoked at CCP after the commit.

`confirm_tokens` gains `session_id` with `ON DELETE CASCADE`: consent
dies with the session, so a replacing sign-in voids every pending
confirmation.

Session metadata is captured once at creation and never updated. `ip`
comes from `CF-Connecting-IP` only when the public listener is
unreachable except through the tunnel; otherwise the socket address
(SPEC §3.1, §10) — T22 owns the trust rule, so take the socket address
here.

`usecase/oauth/oauth.go` is 713 lines before this task adds an exchange
transaction to it. RULES §15 applies: the exchange, the token issuing
and the middleware are separate functions in separate files, not a
longer `oauth.go`.

`adapter/sso/http/tokens.go` is the token store to dismantle: it holds
`chars character.Repository` and calls `Upsert`, `Get`, `Delete` and
`ListByUser` on it, keeping access tokens in memory beside them. The
memory half is right and stays — an access token lives 20 minutes and is
re-derivable (DB.md). The persistence half moves out: `usecase/session`
reads the session row `FOR UPDATE`, re-reads after the lock, calls the
SSO client with the refresh token it found, and writes the rotated token
back inside the same transaction.

`.golangci.yml` has no rule against an adapter importing a domain — its
`depguard` rules cover domain→domain and service→domain only. Add
`adapter-no-domain` once the last such import is deleted.

## Work

1. `internal/domain/session`: the entity and its `Repository`
   (create, revoke-all-for-character, read live by id, lock for
   refresh). `internal/domain/session/pgx` implements it; every query a
   declared const.
2. Migration: create `sessions` with both indexes; add
   `confirm_tokens.session_id` (FK, cascade); drop the grant columns
   from `characters`.
3. Repository behaviour: revoke every unrevoked session of a character,
   clearing tokens and returning one struct describing what was
   cleared; `SELECT … FOR UPDATE` on a session with a post-lock re-read
   for the refresh.
4. Token exchange (`usecase/oauth`), one transaction:
   `pg_advisory_xact_lock(character_id)` → delete the code → revoke
   predecessors → insert the session → commit → issue JWTs with `sub`
   and `sid`. After the commit, revoke the predecessor's refresh token
   at CCP.
5. Both grants verify `sid` against a live session (`revoked_at IS NULL
   AND now() < valid_til`); the refresh JWT's `exp` is the session's
   `valid_til`. A dead session is `401` on `/mcp` and `invalid_grant`
   on refresh.
6. `adapter/sso`: the `Client` interface keeps only what talks to CCP.
   `sso.go` stops importing `domain/character`; `tokens.go` keeps its
   in-memory access-token cache and loses its repository. Add the
   `adapter-no-domain` `depguard` rule in the same commit.
7. Runtime split (SPEC §3.4): allowance, error budget and the shared
   response cache under `sub`; refresh token, scopes and cached access
   token under `sid`. The runtime records its `sid`, and a request with
   a different one rebuilds the grant half before doing anything else.
8. `eve_auth_logout` revokes the session, soft-deletes the character,
   then revokes at CCP after the commit. `eve_auth_status` reports
   `session_expires_at`.
9. Tests, against a real Postgres for the locks and the generated
   `sso.Client` mock for CCP: two concurrent exchanges for one character
   both succeed, the second replacing the first, with no unique
   violation; a token whose `sid` was revoked gets `401`; an
   expired-but-unrevoked row does not block a new sign-in; a runtime
   built from session A rebuilds when a request carries session B; two
   goroutines refreshing one session perform one CCP exchange (the
   post-lock re-read); the CCP revoke is observed after the commit, and
   a failing revoke leaves the row revoked anyway.

## Files

- Add: `internal/domain/session/*`, `internal/domain/session/pgx/*`,
  one migration, the generated `session.Repository` mock
- Edit: `internal/usecase/oauth/*.go` (split, not grown),
  `internal/usecase/session/*.go`, `internal/adapter/sso/sso.go`,
  `internal/adapter/sso/http/{client.go,tokens.go}`,
  `internal/domain/confirm/*`, `internal/service/http/handler.go`,
  `internal/usecase/eve/account.go`, `cmd/eve-mcp/main.go`,
  `.golangci.yml`

## Acceptance

- [x] `sessions` exists with the partial unique index; `characters`
      holds no token
- [x] The exchange takes the advisory lock and revokes with
      `revoked_at IS NULL`, tokens cleared
- [x] `rg -n 'pgerrcode|PgError|23505'` finds nothing
- [x] CCP revoke happens after the commit, never inside
- [x] `sid` is in both tokens and checked on every call
- [x] Grant state is keyed by `sid`; a request with a new `sid` rebuilds
      it, covered by a test
- [x] An authorization failure revokes the requesting `sid` only
- [x] `sso.Client` has no method that reads or writes our database, and
      no package under `internal/adapter/` imports `internal/domain`
- [x] `depguard` has an `adapter-no-domain` rule and the tree is green
      under it
- [x] Concurrent sign-in and concurrent refresh tests pass
- [x] No function in `usecase/oauth` does the exchange *and* the issuing
- [x] No clock is injected anywhere; no database test runs in a
      `synctest` bubble
- [x] `go test ./...`, `make test-store` and `make lint` pass; signing
      in from a second client kicks the first, verified by hand once

## Verify

```bash
make test-store
rg -n 'refresh_token' --glob '*.go' --glob '*.sql'   # sessions and auth_codes only
rg -n 'pgerrcode|PgError|23505' --glob '*.go'
rg -n 'internal/domain' internal/adapter --glob '*.go'   # must be empty
```

## Done

Set `Status: done` here and in [README.md](README.md).
