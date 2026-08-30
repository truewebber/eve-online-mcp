# T16 — Both scope checks: refuse a short login, revoke on drift

- Status: `todo`
- Size: M
- Depends on: T15
- SPEC: §3.2, §3.5, §12.6; AUTH.md standing requirement 6

## Goal

There is exactly one path back into a working connection: `401` on
`/mcp` → OAuth → EVE SSO. So every state where a session's EVE grant is
unusable has to become a dead session, or the connection hangs somewhere
the model cannot describe its way out of.

Two checks, halves of one invariant:

1. **At the callback**, compare the granted `scp` with what the build
   requires. A login that came back short never becomes a session: no
   code is minted, and the player gets a page naming the missing scopes
   and the one place they are fixed.
2. **At every resolution**, revoke the session when its stored `scopes`
   no longer cover that set, when a refresh returns `invalid_grant`, or
   when `owner_hash` changed at login.

Without the first, the second is an infinite browser loop the day a host
forgets one scope in the application registration: sign in, revoke on
first call, `401`, sign in again, forever, with nothing to read.

## Why this is one Composer session

Same required-scope set, same failure mode, two ends of one flow.
Implementing only the revoke side ships the loop.

## Do not

- Revoke on transient ESI failures. 5xx, timeouts and `420` never revoke
  anything; only an authorization verdict does (SPEC §3.5).
- Revoke the character's live session on `invalid_grant` — revoke the
  `sid` that produced the request (T15, SPEC §3.4).
- Soften the drift check to "the scopes this tool needs". Adding a scope
  to the code signs everybody out once, at their next call, deliberately:
  the alternative is tools failing one at a time with an error the player
  cannot act on.
- Return the raw CCP error to the browser. The page names missing scope
  identifiers and where to add them, nothing else.
- Ask CCP for a narrower scope set to avoid the problem.

## Context

`RequestedScopes()` in `internal/domain/write/policy.go` is the required
set: `ReadScopes` (33) ∪ `CorpReadScopes` (11) ∪ every write capability's
scopes (7) = 51. The EVE access token's `scp` claim carries what was
granted.

The callback already exchanges the EVE code and parks a grant in
`auth_codes`; the check goes before that write, so a short login leaves
nothing behind but a `characters` row at worst.

`internal/service/http/pages.go` owns the human pages — the refusal page
belongs there, with the missing identifiers listed verbatim so the host
can paste them into the application form.

## Work

1. A comparison helper in `domain/write`: required minus granted, sorted,
   so both call sites share one answer.
2. Callback: on a non-empty difference, render the refusal page (HTTP
   400) and stop. Log it at warn with the character name and the missing
   scopes — this is a host misconfiguration and it should be visible in
   the pod log, not only in a browser.
3. Resolution: if the session's stored `scopes` fall short, revoke and
   answer `401`.
4. Refresh: `invalid_grant` from CCP revokes the requesting session.
5. Login: `owner_hash` differs from the stored one → revoke every
   session of that character, re-own the row, continue the sign-in.
6. Tests: a granted set missing one scope is refused at the callback and
   creates no `auth_codes` row; a session whose stored scopes fall short
   is revoked once and then answers `401`; `invalid_grant` revokes the
   requesting session and not a sibling; an `owner_hash` change revokes
   and re-owns; a 500 from ESI revokes nothing.

## Files

- Edit: `internal/domain/write/policy.go`, `internal/usecase/oauth/oauth.go`,
  `internal/usecase/session/session.go`, `internal/adapter/sso/sso.go`,
  `internal/service/http/pages.go`

## Acceptance

- [ ] A short grant is refused at the callback with the missing scopes
      named, and writes no `auth_codes` row
- [ ] Scope drift revokes the session; the next call is `401`
- [ ] `invalid_grant` revokes the requesting `sid` only
- [ ] `owner_hash` change revokes and re-owns
- [ ] Transient ESI failures never revoke, covered by a test
- [ ] `go test ./...` passes

## Verify

```bash
go test ./internal/usecase/oauth ./internal/usecase/session ./internal/domain/write -count=1
```

## Done

Set `Status: done` here and in [README.md](README.md).
