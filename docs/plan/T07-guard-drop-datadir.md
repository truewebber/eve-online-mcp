# T07 — Confirm tokens + mail cap in Postgres; drop `DATA_DIR`

- Status: `done`
- Size: M
- Depends on: T04, T06
- SPEC: §4.1 (confirm cycle), §5.2 (mail cap), §8, §12.0 (`DATABASE_URL`
  replaces `DATA_DIR`)

## Goal

Confirm tokens and the mail counter survive across replicas. Process
config no longer has `DATA_DIR`. Install (launchd/systemd) does not use
a data directory as the reason for existence — working directory can
still be wherever `.env` lives, but nothing is persisted there.

## Why this is one Composer session

Guard persistence + deleting the last file-store env. Audit log and
write budget still exist until T08; do not confuse them with this task.
You may keep writing `audit.jsonl` only if you still have a path — once
`DATA_DIR` is gone, **stop writing the audit file** (or buffer it
nowhere). Prefer: drop the file sink here if it has no directory, and
leave the in-memory budget until T08. Simplest: set `AuditFile` empty
in `main` now so T08 only deletes the remaining API.

## Do not

- Remove the confirm cycle or the mail cap.
- Remove `WRITE_MODE` / `WRITE_ALLOW` (T08).
- Add a general write budget in Postgres.

## Context

Today `domain/write.Guard` holds `pending` and `recentMail` in memory
and appends `audit.jsonl`. `Authorize` / `Record` have no `userID`
because each user session has its own Guard (`ForUser`).

Target: Guard stays per-user **or** takes `userID` on each call. Tokens
live in `confirm_tokens`; mail events in `mail_log`. TTL 300 s constant
is fine to still pass in Options until T08 hard-codes it.

`domain` must not import `adapter/store`. Define a small interface in
`domain/write` (or pass funcs) implemented by store.

Install path (`cmd/eve-mcp/service.go`): drop `serviceDataDir` as a
token store. Working directory can be `$HOME` or still the OS config
dir **only** so `.env` is found — document that. `DATABASE_URL` in
`.env` is what matters. `MkdirAll(DataDir)` in `config.go` goes away.

## Work

1. Persist confirm tokens (single use, digest + tool match unchanged).
2. Mail cap: count `mail_log` in the rolling hour on `Authorize`;
   insert on `Record` for `mail_send`.
3. Periodic or on-access purge of expired confirm tokens.
4. Remove `DATA_DIR` from `config`, `.env.example`, install scripts.
5. `DATABASE_URL` required (if T04 already did this, just delete
   `DATA_DIR`).
6. Makefile `run`: depend on `postgres` so a laptop `make run` is the
   full local loop.

## Files

- Edit: `domain/write/guard.go`, `domain/write/policy.go`,
  `usecase/session/session.go`, `cmd/eve-mcp/config.go`,
  `cmd/eve-mcp/main.go`, `cmd/eve-mcp/service.go`, `Makefile`,
  `.env.example`
- Tests: confirm mismatch / expiry / single use; mail cap 5

## Acceptance

- [x] `rg DATA_DIR` is empty except maybe `docs/plan` / historical SPEC
      notes about the old layout
- [x] Confirm cycle still returns `confirmation_required` + `will_do`
- [x] Sixth mail in an hour is `WriteBlocked` with an actionable message
- [x] `make run` starts Postgres then the binary
- [x] Launchd/systemd unit no longer claims a data dir for tokens

## Verify

```bash
rg -n 'DATA_DIR|audit\.jsonl' --glob '!docs/**' --glob '!.git/**'
go test ./internal/domain/write ./internal/adapter/store ./internal/usecase/session
make run   # needs CLIENT_ID + DATABASE_URL in .env
curl -s http://127.0.0.1:8766/healthz
```

## Done

Set `Status: done` here and in [README.md](README.md). After this, the
local loop in [README.md](README.md) is real.
