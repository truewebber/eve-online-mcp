# T18 — Both scope checks: refuse a short login, revoke on drift

- Status: `done`
- Size: M
- Depends on: T17
- RULES: §5 (tests), §9 (the user sees only static errors), §6 (the code
  is the documentation)
- SPEC: §3.2, §3.5, §12.6; AUTH.md standing requirement 6
- Replaces: old T16

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
  `sid` that produced the request (T17, SPEC §3.4).
- Soften the drift check to "the scopes this tool needs". Adding a scope
  to the code signs everybody out once, at their next call,
  deliberately: the alternative is tools failing one at a time with an
  error the player cannot act on.
- Return the raw CCP error to the browser. RULES §9: the internal error
  is logged, and what crosses is static. The missing scope identifiers
  are the one dynamic part, and they are ours — constants out of
  `policy.go`, never a string CCP sent us. `error_description` from CCP
  is logged, not rendered. T23 owns the rest of the page catalogue.
- Explain the rule in a comment on the check. Name the helper so the
  rule is legible without one (RULES §6).
- Ask CCP for a narrower scope set to avoid the problem.

## Context

`RequestedScopes()` in `internal/domain/write/policy.go` is the required
set: `ReadScopes` (33) ∪ `CorpReadScopes` (11) ∪ every write
capability's scopes (7) = 51. The EVE access token's `scp` claim carries
what was granted.

The callback already exchanges the EVE code and parks a grant in
`auth_codes`; the check goes before that write, so a short login leaves
nothing behind but a `characters` row at worst.

`internal/service/http/pages.go` owns the human pages — the refusal page
belongs there, with the missing identifiers listed verbatim so the host
can paste them into the application form.

## Work

1. A comparison helper in `domain/write`: required minus granted,
   sorted, returning one slice, so both call sites share one answer.
2. Callback: on a non-empty difference, render the refusal page (HTTP
   400) and stop. Log it at warn with the character name and the missing
   scopes — this is a host misconfiguration and it belongs in the pod
   log, not only in a browser.
3. Resolution: if the session's stored `scopes` fall short, revoke and
   answer `401`.
4. Refresh: `invalid_grant` from CCP revokes the requesting session.
5. Login: `owner_hash` differs from the stored one → revoke every
   session of that character, re-own the row, continue the sign-in.
6. Tests: a granted set missing one scope is refused at the callback and
   creates no `auth_codes` row; a session whose stored scopes fall short
   is revoked once and then answers `401`; `invalid_grant` revokes the
   requesting session and not a sibling; an `owner_hash` change revokes
   and re-owns; a 500 from ESI revokes nothing; the refusal page
   contains the missing identifiers and no substring of any Go error.

## Files

- Edit: `internal/domain/write/policy.go`,
  `internal/usecase/oauth/*.go`, `internal/usecase/session/session.go`,
  `internal/adapter/sso/sso.go`, `internal/service/http/pages.go`,
  `internal/service/http/handler.go`

## Acceptance

- [x] A short grant is refused at the callback with the missing scopes
      named, and writes no `auth_codes` row
- [x] Scope drift revokes the session; the next call is `401`
- [x] `invalid_grant` revokes the requesting `sid` only
- [x] `owner_hash` change revokes and re-owns
- [x] Transient ESI failures never revoke, covered by a test
- [x] The refusal page carries no `err.Error()` and no CCP-supplied
      string; both are in the log
- [x] `go test ./...`, `make test-store` and `make lint` pass

## Verify

```bash
go test ./internal/usecase/oauth ./internal/usecase/session ./internal/domain/write ./internal/service/http -count=1
```

## Done

Set `Status: done` here and in [README.md](README.md).
