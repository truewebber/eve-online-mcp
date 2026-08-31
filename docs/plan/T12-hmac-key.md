# T12 — `HMAC_KEY` env; drop `app_secrets`

- Status: `done`
- Size: S
- Depends on: T11
- RULES: §14 (the application does not migrate), §16 (config is env on
  the binary), §12 (SQL is declared)
- SPEC: §2 (`HMAC_KEY`), §12.4; DB.md "What is deliberately NOT in the
  database"
- Replaces: the second half of the old T12

## Goal

The MCP JWT signing key stops living in `app_secrets` and comes from the
required `HMAC_KEY` env instead. The table goes away.

A signing key is static operator-set config, not data (DB.md). Keeping
it in Postgres puts it in every database backup next to the refresh
tokens it protects, and makes rotation a `DELETE` instead of a restart.

## Why this is one Composer session

It is `cmd` + one adapter file + one migration. Nothing that product
code reads changes shape, and after this task the running server behaves
identically except that everybody re-authenticates once — a new signing
key invalidates old JWTs.

## Do not

- Keep a fallback that generates a key when `HMAC_KEY` is missing.
  Missing is fatal at boot (SPEC §2).
- Put the key in a file.
- Call `goose.Up` from `Open`, `main` or any process path that serves
  traffic (RULES §14). It already is not; keep it that way.
- Change the schema's shape beyond dropping `app_secrets`. `users` goes
  in T14, `sessions` in T17, `mutations` in T19.
- Write a migration that transforms the `users`-era database. DB.md is
  explicit: that database is dropped once, by hand.
- Add env for anything SPEC §2 lists as a constant (RULES §16).

## Context

**goose already landed** and is not re-done here: `sql/` at the
repository root with `-- +goose Up` sections, `make migrate`,
`storetest/goose.go` applying it in tests, and
`storetest/migrate_test.go` asserting `Store.Open` runs no SQL. What is
still only a phrase in three documents is "applied by CI/CD" — there is
no pipeline at all; T13 builds one and runs `make migrate` in it.

What is left is the key. `GetOrCreateSecret(ctx, name)` in
`internal/adapter/store/oauth.go` reads or generates it from
`app_secrets`; `usecase/oauth.Open` takes the `*store.Store` for that
one call. After this task the key arrives from `main` as an option like
every other piece of config, and `oauth.Open` stops needing the database
handle at all — which is what lets T15 retire the package.

Rotation semantics to write down in the task's output: new secret +
restart = every client re-authenticates, EVE grants unaffected.

## Work

1. `HMAC_KEY` in `cmd/eve-mcp/config.go`: required, decoded, at least 32
   bytes, fatal at boot with a message naming `openssl rand -hex 32`.
2. Map it into `oauth.Options`. Delete the `*store.Store` parameter from
   `oauth.Open` if the key was its only use.
3. Delete `GetOrCreateSecret`, `internal/adapter/store/oauth.go` and
   `SecretBytes` in `types.go`.
4. Migration `sql/00003_drop_app_secrets.sql`.
5. Migration test: apply from empty via the same command CI uses, assert
   the expected table set and that `app_secrets` is not in it; apply
   twice, assert idempotence.
6. Config test: empty `HMAC_KEY` and a 16-byte `HMAC_KEY` both refuse to
   boot, each with its own message.

## Files

- Edit: `cmd/eve-mcp/config.go`, `cmd/eve-mcp/main.go`,
  `internal/usecase/oauth/oauth.go`, `internal/adapter/store/types.go`,
  `internal/adapter/store/storetest/migrate_test.go`, `.env.example`
- Delete: `internal/adapter/store/oauth.go`
- Add: `sql/00003_drop_app_secrets.sql`

## Acceptance

- [x] `rg -n 'app_secrets|GetOrCreateSecret|SecretBytes'` finds nothing
      in `internal/`, `cmd/` or `sql/00001`-era code paths
- [x] Missing or short `HMAC_KEY` is fatal at boot, each case tested
- [x] Applying the migrations from empty yields the expected tables and
      applying twice is a no-op, both asserted by a test
- [x] No code reads a key from the database
- [x] `go build ./cmd/eve-mcp`, `go test ./...` and `make lint` pass

## Verify

```bash
rg -n 'app_secrets|GetOrCreateSecret' --glob '*.go' --glob '*.sql'
make test-store
HMAC_KEY= ./eve-mcp   # must refuse to start
```

## Done

Set `Status: done` here and in [README.md](README.md).
