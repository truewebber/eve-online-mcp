# T19 — Sweeps: expire sessions, purge clients, revoke abandoned grants

- Status: `todo`
- Size: M
- Depends on: T15, T17
- SPEC: §12.9, §3.5; DB.md "Sweeps"

## Goal

One goroutine per pod, every five minutes, the run guarded by
`pg_try_advisory_lock` so exactly one pod sweeps and the rest skip
without blocking. Six rules, three of which do not exist today and one of
which is a real hole:

- **Expire sessions** past `valid_til`: set `revoked_at`, clear
  `refresh_token`, revoke it at CCP. Without this a player who signs in
  once and never comes back leaves a usable EVE grant in the database
  forever, and the 30-day session lifetime is a claim about our JWTs
  only (AUTH.md, leak audit 4).
- **Purge `oauth_clients`** that were soft-deleted more than 30 days ago.
  Registration is anonymous, so this is the one table an untrusted caller
  can grow; a sweep that only soft-deletes leaves the count monotonic and
  turns AUTH.md's standing requirement 4 into a wish.
- **Revoke the parked grant of an abandoned `auth_codes` row** before
  deleting it. A code nobody redeemed still parked a live refresh token;
  deleting the row without telling CCP leaves a grant alive that nothing
  on this side can ever use or revoke.

Plus the three that are just deletes: expired `login_states`,
`confirm_tokens`, and `mutations` older than 90 days, and the purge of
sessions revoked more than 90 days ago.

## Why this is one Composer session

One runner, one lock, one table of rules. Two of them make an HTTP call
and share that mechanism.

## Do not

- Sweep `characters`. It is the identity, it holds no secret, and a
  returning player must keep the same `sub`.
- Hold the advisory lock across the whole pod lifetime. Take it per run,
  with `try`, and skip when another pod holds it.
- Retry a failed CCP revoke. The column is already NULL by then, and
  re-holding a secret to enable a retry undoes the point. Log it.
- Let a CCP call block the transaction. Revoke locally first, commit,
  then call — same shape as the sign-in exchange (SPEC §3.1).
- Turn one sweep into a thousand serial HTTP calls. Both CCP-touching
  rules are batched per run with a ceiling.
- Put any of this on a request path.

## Context

Nothing sweeps today. `store.PurgeExpired` and `CachePurgeExpired` exist
from the cache era and go away with T13 if they have not already.

The two CCP-touching rules need the same helper: read the rows and their
tokens in one transaction, null the tokens and mark the rows, commit,
then fire the revokes.

The expiry rule is also a §3.5 revocation trigger, so a session it
touches must be indistinguishable from one revoked any other way — same
column, same clearing, so `401` follows on the next call with no extra
code.

## Work

1. Sweeper: a goroutine started from `main`, 5 min ticker, each run
   wrapped in `pg_try_advisory_lock` / unlock, structured log line per
   run with per-rule counts.
2. The three plain deletes (`login_states`, `confirm_tokens`,
   `mutations`) and the session purge.
3. Expiry rule with the revoke-then-call helper and a per-run ceiling.
4. `auth_codes` rule using the same helper.
5. `oauth_clients`: soft-delete registrations older than 30 days with no
   session, then hard-delete rows soft-deleted more than 30 days ago.
6. Tests: each rule deletes exactly what it should and nothing adjacent;
   an expired session comes out revoked with a NULL token; a failed CCP
   revoke still leaves the row revoked; two sweepers running at once do
   the work once; a soft-deleted client older than the second window is
   actually gone.

## Files

- Edit: `internal/adapter/store/*.go`, `cmd/eve-mcp/main.go`
- Add: `internal/adapter/store/sweep.go` (or equivalent)

## Acceptance

- [ ] Sweeps run under `pg_try_advisory_lock`, once per interval across
      all pods
- [ ] Expired sessions end up revoked with a cleared token and a CCP
      revoke attempted
- [ ] Abandoned `auth_codes` grants are revoked before deletion
- [ ] Soft-deleted `oauth_clients` are eventually deleted, not just
      marked
- [ ] `characters` is never touched
- [ ] No CCP call inside a transaction; failures logged, not retried
- [ ] `go test ./...` passes

## Verify

```bash
go test ./internal/adapter/store -count=1
rg -n 'pg_try_advisory_lock' --glob '*.go'
```

## Done

Set `Status: done` here and in [README.md](README.md).
