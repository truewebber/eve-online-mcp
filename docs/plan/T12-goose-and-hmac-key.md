# T12 — goose under an advisory lock; `HMAC_KEY` out of the database

- Status: `todo`
- Size: M
- Depends on: T11
- SPEC: §2 (`HMAC_KEY`), §12.4; DB.md "Migrations"

## Goal

Two startup concerns, both infrastructure, neither touching product
behaviour:

1. The hand-rolled `schema_migrations` migrator becomes **goose**
   (`github.com/pressly/goose/v3`) with embedded SQL, and the whole run
   is wrapped in a Postgres **advisory lock**. Rolling updates start
   several pods at once, so the lock is load-bearing, not paranoia.
2. The MCP JWT signing key stops living in `app_secrets` and comes from
   the required `HMAC_KEY` env instead. The table goes away.

## Why this is one Composer session

Both are `cmd` + `adapter/store` changes with a single blast radius:
startup. Neither changes a table that product code reads, and after this
task the running server behaves identically except that everybody
re-authenticates once (a new signing key invalidates old JWTs).

## Do not

- Change the schema's *shape* here beyond dropping `app_secrets`. The
  target layout lands in T13 (cache tables out) and T14/T15 (sessions
  in).
- Write a migration that transforms the `users`-era database. DB.md is
  explicit: that database gets dropped once, by hand. Say so in the
  migration's comment.
- Keep a fallback that generates a key when `HMAC_KEY` is missing.
  Missing means fatal at boot (SPEC §2).
- Put the key in a file.

## Context

`internal/adapter/store/migrate.go` today creates `schema_migrations`
and applies `sql/*.sql` in name order inside a transaction, with no
locking. Migrations are embedded already, which is the part to keep.

`GetOrCreateSecret(ctx, name)` in `oauth.go` reads or generates the HMAC
key from `app_secrets`; `usecase/oauth` consumes it. After this task the
key is passed in from `main` as `Options`, like every other piece of
config, and `cmd/eve-mcp/config.go` validates that it decodes to at
least 32 bytes.

Rotation semantics to write down in the task's output: new secret +
restart = every client re-authenticates, EVE grants unaffected.

## Work

1. Add `github.com/pressly/goose/v3`. Keep `sql/` embedded; rename files
   to goose's convention with `-- +goose Up` sections.
2. `migrate(ctx)`: take `pg_advisory_lock` on a fixed key, run
   `goose.Up`, release. Fail the boot on error — a pod that cannot
   migrate must not serve.
3. New migration dropping `app_secrets`.
4. `HMAC_KEY` becomes required in `cmd/eve-mcp/config.go`, validated for
   length, mapped into `oauth.Options`. Delete `GetOrCreateSecret` and
   its callers.
5. Migration test (T11 gives the harness): apply from an empty database,
   assert the expected table set; apply twice, assert idempotence.
6. Concurrency test or a documented manual check: two processes
   migrating at once, one waits, neither errors.

## Files

- Edit: `internal/adapter/store/migrate.go`, `internal/adapter/store/oauth.go`,
  `internal/adapter/store/sql/*`, `internal/usecase/oauth/oauth.go`,
  `cmd/eve-mcp/config.go`, `cmd/eve-mcp/main.go`, `go.mod`
- Add: one migration file

## Acceptance

- [ ] goose applies the embedded SQL under an advisory lock
- [ ] Applying twice is a no-op; applying from empty yields the expected
      tables, asserted by a test
- [ ] `app_secrets` is gone; no code reads a key from the database
- [ ] Missing or short `HMAC_KEY` is fatal at boot with a clear message
- [ ] `go build ./cmd/eve-mcp` and `go test ./...` pass

## Verify

```bash
rg -n 'app_secrets|GetOrCreateSecret' --glob '*.go'
go test ./internal/adapter/store -count=1
HMAC_KEY= ./eve-mcp   # must refuse to start
```

## Done

Set `Status: done` here and in [README.md](README.md).
