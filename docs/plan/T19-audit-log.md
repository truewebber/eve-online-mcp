# T19 — `mutations` audit log; the mail cap counts from it

- Status: `done`
- Size: M
- Depends on: T17
- RULES: §2 (constraints are not control flow), §11 (`domain/mutation`),
  §12 (declared SQL), §1 (no clock), §5 (tests)
- SPEC: §4.1, §5.4, §8, §12.7; DB.md `mutations`
- Replaces: old T17

## Goal

One append-only row per in-game change the server attempted, successful
or not. It is what makes PRD's "nothing mutates without a confirmation"
checkable after the fact rather than trusted, and it is where the mail
cap counts from — so `mail_log` goes away.

The cap is the one exact counter in SPEC §5, and only because the count
and the insert it authorises happen under one advisory lock keyed by the
character. Two concurrent sends both reading "4 this hour" is reachable
with two goroutines in one pod.

## Why this is one Composer session

`Guard.Record` is already the single funnel every mutation passes
through, and T15 already gave `mail_log` its final home in
`domain/mutation`. The table, the write, the cap query and the
`eve_auth_status` field are one seam.

## Do not

- Store message bodies, contact lists or fitting contents. The digest
  identifies the arguments and the summary describes them; a log that
  quotes what players write to each other is a second copy of their mail
  with none of the reasons to keep it (DB.md).
- Record a mutation that was refused before ESI. No confirm token or a
  spent cap is not a mutation.
- Skip recording a failure. "Did the assistant send that mail" must be
  answerable when the answer is "it tried and got a 520".
- Count anything but successful `eve_mail_send` rows toward the cap.
- Count outside the lock, or take `FOR UPDATE` on `characters` for it —
  the row lock lives on `sessions` now, and a second differently-scoped
  lock on the identity table is how two unrelated paths wait on each
  other (SPEC §5.4).
- Reach for a SQLSTATE. The cap is decided by a `SELECT count(*)` under
  an advisory lock, not by letting an insert fail (RULES §2).
- Take a clock so a test can pretend an hour passed. The window is
  `created_at >= now() - interval '1 hour'` in the statement; a store
  test that needs an older row updates `created_at` in SQL and runs the
  real path (RULES §1).
- Give `Guard.Record` a five-value return. One request struct in, an
  `error` out (RULES §10).

## Context

`internal/domain/mutation` and its `pgx` implementation exist since T15,
still backed by `mail_log`. This task gives them their real table.

`mutations` per DB.md: `id`, `character_id`, nullable `session_id`
(`ON DELETE SET NULL`), `tool`, `capability`, `args_digest`, `summary`
(≤ 200 chars), `outcome` (`ok` / `error`), `esi_status`, `error`
(≤ 200 chars), `created_at`. Two indexes: `(character_id, created_at
DESC)` and a partial one for the cap query on `tool = 'eve_mail_send'
AND outcome = 'ok'`.

`args_digest` is the same sha256 the confirm token carried, which is
what lets an auditor tie a row back to the preview that authorised it.

`summary` is the preview's short form — "mail to 2 recipients, subject
'Fleet tonight'". A subject is a player's own words about their own
mail, which is the line DB.md draws; bodies are on the other side of it.

## Work

1. Migration: create `mutations` with both indexes; drop `mail_log`.
2. `mutation.Repository` gains the append and the cap count; the pgx
   implementation carries both as declared consts, the count inside the
   advisory lock.
3. `Guard.Record` takes one record struct — tool, capability, args,
   outcome, ESI status, error — and inserts one row, deriving the digest
   the same way the confirm token does and truncating `summary` and
   `error` at 200 chars.
4. Mail cap: inside one `pg_advisory_xact_lock` keyed by character (its
   own key namespace, not the sign-in's), count `ok` `eve_mail_send`
   rows in the rolling hour, then let the send proceed and record it.
   Over cap → `WriteBlocked` at `Guard.Authorize` time.
5. `eve_auth_status` reports `mails_last_hour`,
   `mails_remaining_this_hour` and `mail_cap_per_hour` from the log.
6. Ship the mail-cap rejection counter (SPEC §11) in the same commit as
   the refusal that increments it.
7. Tests: a failed ESI write is recorded with its status; a refusal
   before ESI is not recorded; the cap query counts only successful
   sends inside the window, verified by ageing a row in SQL; two
   concurrent sends at four-this-hour produce exactly one send and one
   `WriteBlocked`; no row contains a body.

## Files

- Edit: `internal/domain/mutation/*`, `internal/domain/mutation/pgx/*`,
  `internal/domain/write/guard.go`, `internal/domain/write/persist.go`,
  `internal/usecase/session/persist.go`, `internal/usecase/eve/writes.go`,
  `internal/usecase/eve/account.go`
- Add: one migration; regenerate the `mutation.Repository` and
  `write.Persist` mocks

## Acceptance

- [x] Every mutation that reached ESI is recorded, success or failure
- [x] Refusals before ESI are not recorded
- [x] The cap counts from `mutations` under one advisory lock, with a
      concurrency test against a real database
- [x] `mail_log` is gone from the schema and the code
- [x] No column holds a mail body, contact list or fitting
- [x] `eve_auth_status` reports remaining sends from the log
- [x] No clock is injected; no SQLSTATE is inspected; every query is a
      declared const
- [x] `go test ./...`, `make test-store` and `make lint` pass

## Verify

```bash
rg -n 'mail_log|CountMailSince|InsertMail' --glob '*.go' --glob '*.sql'
go test ./internal/domain/write -count=1 && make test-store
```

## Done

Set `Status: done` here and in [README.md](README.md).
