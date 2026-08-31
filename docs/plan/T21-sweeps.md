# T21 — Sweeps: expire sessions, purge clients, revoke abandoned grants

- Status: `todo`
- Size: M
- Depends on: T17, T19
- RULES: §1 (time is not a test seam), §2 (constraints are not control
  flow), §12 (declared SQL), §7 (the logger is a dependency),
  §11 (a domain sweeps its own table)
- SPEC: §12.9, §3.5; DB.md "Sweeps"
- Replaces: old T19

## Goal

One goroutine per pod, every five minutes, the run guarded by
`pg_try_advisory_lock` so exactly one pod sweeps and the rest skip
without blocking. Six rules, three of which do not exist today and one
of which is a real hole:

- **Expire sessions** past `valid_til`: set `revoked_at`, clear
  `refresh_token`, revoke it at CCP. Without this a player who signs in
  once and never comes back leaves a usable EVE grant in the database
  forever, and the 30-day session lifetime is a claim about our JWTs
  only (AUTH.md, leak audit 4).
- **Purge `oauth_clients`** that were soft-deleted more than 30 days
  ago. Registration is anonymous, so this is the one table an untrusted
  caller can grow; a sweep that only soft-deletes leaves the count
  monotonic and turns AUTH.md's standing requirement 4 into a wish.
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

- Give the sweeper a clock so a test can advance it. The ticker is real
  `time.Ticker`; the interval test runs the runner inside
  `synctest.Test`, where timers are virtual (RULES §1). The rules
  themselves are tested against a real Postgres with rows aged by SQL —
  and that test is **not** inside a bubble, because I/O outside the
  bubble is not durably blocking and the bubble deadlocks.
- Put the cutoff in Go. `expires_at < now()` and `created_at < now() -
  interval '90 days'` are the statement's business; a Go-side `time.Now`
  passed as a parameter is a second clock disagreeing with the first.
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
- Write one `Sweep()` that does six things. Each rule is a named
  function; the runner calls them in order and counts (RULES §15).
- Create a package that deletes from every table. Each rule is a method
  on the repository of the domain that owns the table (RULES §11); the
  runner is a small type in `usecase` that holds them and the logger it
  was constructed with (RULES §7).

## Context

Nothing sweeps today. The domains that own the swept tables all exist
after T15 and T17: `loginstate`, `authcode`, `confirm`, `session`,
`mutation`, `oauthclient`.

The two CCP-touching rules need the same helper: read the rows and their
tokens in one transaction, null the tokens and mark the rows, commit,
then fire the revokes through the `sso.Client`.

The expiry rule is also a §3.5 revocation trigger, so a session it
touches must be indistinguishable from one revoked any other way — same
column, same clearing, so `401` follows on the next call with no extra
code.

## Work

1. A sweeper in `usecase`: a goroutine started from `main`, 5 min
   ticker, each run wrapped in `pg_try_advisory_lock` / unlock, one
   structured log line per run with per-rule counts.
2. The three plain deletes (`login_states`, `confirm_tokens`,
   `mutations`) and the session purge, each a declared const on its
   domain's repository.
3. Expiry rule with the revoke-then-call helper and a per-run ceiling.
4. `auth_codes` rule using the same helper.
5. `oauth_clients`: soft-delete registrations older than 30 days with no
   session, then hard-delete rows soft-deleted more than 30 days ago.
6. Tests: each rule deletes exactly what it should and nothing adjacent;
   an expired session comes out revoked with a NULL token; a failed CCP
   revoke still leaves the row revoked; two sweepers running at once do
   the work once; a soft-deleted client older than the second window is
   actually gone; the interval test, separately and under `synctest`,
   shows one run per tick and none while another holds the lock.

## Files

- Add: `internal/usecase/sweep/*` (runner + its test), sweep methods on
  the existing domain repositories
- Edit: `cmd/eve-mcp/main.go`

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
- [ ] No clock is injected; the database tests are not inside a
      `synctest` bubble and the interval test is
- [ ] Every sweep query is a declared const on its domain's repository
- [ ] `go test ./...`, `make test-store` and `make lint` pass

## Verify

```bash
make test-store
rg -n 'pg_try_advisory_lock' --glob '*.go'
rg -n 'synctest' internal/usecase/sweep
```

## Done

Set `Status: done` here and in [README.md](README.md).
