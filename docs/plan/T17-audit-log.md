# T17 — `mutations` audit log; the mail cap counts from it

- Status: `todo`
- Size: M
- Depends on: T15
- SPEC: §4.1, §5.4, §8, §12.7; DB.md `mutations`

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
through. The table, the write, the cap query and the `eve_auth_status`
field are one seam.

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

## Context

Today `store.CountMailSince` / `InsertMail` maintain `mail_log`, and
nothing records non-mail mutations at all.

`mutations` per DB.md: `id`, `character_id`, nullable `session_id`
(`ON DELETE SET NULL`), `tool`, `capability`, `args_digest`, `summary`
(≤ 200 chars), `outcome` (`ok` / `error`), `esi_status`, `error`
(≤ 200 chars), `created_at`. Two indexes: `(character_id, created_at
DESC)` and a partial one for the cap query on
`tool = 'eve_mail_send' AND outcome = 'ok'`.

`args_digest` is the same sha256 the confirm token carried, which is what
lets an auditor tie a row back to the preview that authorised it.

`summary` is the preview's short form — "mail to 2 recipients, subject
'Fleet tonight'". A subject is a player's own words about their own mail,
which is the line DB.md draws; bodies are on the other side of it.

## Work

1. Migration: create `mutations` with both indexes; drop `mail_log`.
2. `Guard.Record(tool, capability, args, outcome, esiStatus, err)`
   inserts one row, deriving the digest the same way the confirm token
   does and truncating `summary` and `error` at 200 chars.
3. Mail cap: inside one advisory lock keyed by character, count `ok`
   `eve_mail_send` rows in the rolling hour, then let the send proceed
   and record it. Over cap → `WriteBlocked` at `Guard.Authorize` time.
4. `eve_auth_status` reports `mails_last_hour`, `mails_remaining_this_hour`
   and `mail_cap_per_hour` from the log.
5. Delete `CountMailSince` / `InsertMail` and the `mail_log` type.
6. Ship the mail-cap rejection counter (SPEC §11) in the same commit as
   the refusal that increments it.
7. Tests: a failed ESI write is recorded with its status; a refusal
   before ESI is not recorded; the cap query counts only successful
   sends inside the window; two concurrent sends at 4-this-hour produce
   exactly one send and one `WriteBlocked`; no row contains a body.

## Files

- Edit: `internal/domain/write/guard.go`, `internal/domain/write/persist.go`,
  `internal/adapter/store/guard.go`, `internal/usecase/eve/writes.go`,
  `internal/usecase/eve/account.go`
- Add: one migration

## Acceptance

- [ ] Every mutation that reached ESI is recorded, success or failure
- [ ] Refusals before ESI are not recorded
- [ ] The cap counts from `mutations` under one advisory lock, with a
      concurrency test
- [ ] `mail_log` is gone
- [ ] No column holds a mail body, contact list or fitting
- [ ] `eve_auth_status` reports remaining sends from the log
- [ ] `go test ./...` passes

## Verify

```bash
rg -n 'mail_log|CountMailSince|InsertMail' --glob '*.go'
go test ./internal/domain/write ./internal/adapter/store -count=1
```

## Done

Set `Status: done` here and in [README.md](README.md).
