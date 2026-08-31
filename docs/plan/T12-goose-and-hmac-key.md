# T12 — goose in CI/CD; `HMAC_KEY` out of the database

- Status: `todo`
- Size: M
- Depends on: T11
- SPEC: §2 (`HMAC_KEY`), §12.4; DB.md "Migrations"; RULES.md §14

## Goal

Two infrastructure changes, neither touching product behaviour:

1. The hand-rolled `schema_migrations` migrator becomes **goose**
   (`github.com/pressly/goose/v3`) with SQL in `sql/` at the repository root. Applying
   those files is an operator step or CI/CD, never a path inside the
   running server (RULES.md §14). `Store.Open` connects; it does not
   run SQL.
2. The MCP JWT signing key stops living in `app_secrets` and comes from
   the required `HMAC_KEY` env instead. The table goes away.

## Why this is one Composer session

Both are `cmd` + `adapter/store` + a Makefile/CI hook. Neither changes
a table that product code reads, and after this task the running server
behaves identically except that everybody re-authenticates once (a new
signing key invalidates old JWTs).

## Do not

- Call `goose.Up` (or the old `migrate`) from `Store.Open`, `main`, or
  any other process path that serves traffic.
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
and applies `sql/*.sql` in name order from `Open`. That call is the
thing this task deletes. The SQL files stay; the apply step moves out.

`GetOrCreateSecret(ctx, name)` in `oauth.go` reads or generates the HMAC
key from `app_secrets`; `usecase/oauth` consumes it. After this task the
key is passed in from `main` as `Options`, like every other piece of
config, and `cmd/eve-mcp/config.go` validates that it decodes to at
least 32 bytes.

Rotation semantics to write down in the task's output: new secret +
restart = every client re-authenticates, EVE grants unaffected.

## Work

1. Add `github.com/pressly/goose/v3`. Keep `sql/`; rename files to
   goose's convention with `-- +goose Up` sections.
2. Delete `migrate` from `Store.Open`. A Makefile target (and the same
   command in CI) runs goose against `DATABASE_URL`. A lock around that
   job belongs to the job, not to the server, if two applies can race.
3. New migration dropping `app_secrets`.
4. `HMAC_KEY` becomes required in `cmd/eve-mcp/config.go`, validated for
   length, mapped into `oauth.Options`. Delete `GetOrCreateSecret` and
   its callers.
5. Migration test (T11 gives the harness): apply from an empty database
   via the same command CI will use, assert the expected table set;
   apply twice, assert idempotence.

## Files

- Edit: `internal/adapter/store/migrate.go`, `internal/adapter/store/oauth.go`,
  `sql/*`, `internal/adapter/store/store.go`,
  `internal/usecase/oauth/oauth.go`, `cmd/eve-mcp/config.go`,
  `cmd/eve-mcp/main.go`, `Makefile`, `go.mod`
- Add: one migration file

## Acceptance

- [ ] goose applies the SQL from a Makefile/CI command, not from `Open`
- [ ] Applying twice is a no-op; applying from empty yields the expected
      tables, asserted by a test
- [ ] `app_secrets` is gone; no code reads a key from the database
- [ ] Missing or short `HMAC_KEY` is fatal at boot with a clear message
- [ ] `go build ./cmd/eve-mcp` and `go test ./...` pass

## Verify

```bash
rg -n 'app_secrets|GetOrCreateSecret|func \(s \*Store\) migrate' --glob '*.go'
go test ./internal/adapter/store -count=1
HMAC_KEY= ./eve-mcp   # must refuse to start
```

## Done

Set `Status: done` here and in [README.md](README.md).
