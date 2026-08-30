# T04 — ESI HTTP cache + names on Postgres

- Status: `done`
- Size: M
- Depends on: T03
- SPEC: §5.1 (ETag cache), §8 (`http_cache`, `names`, `blobs`)

## Goal

Stop using SQLite. `adapter/esi` and `adapter/names` read/write the
Postgres cache methods from T03. After this task, **running the server
requires Postgres** for cache (users/tokens can still be files until
T05). That is expected; `make postgres` is already there.

## Why this is one Composer session

Swap the cache backend. Behaviour (ETag, TTL cap 24 h, names, blobs)
must stay. No OAuth, no tools.

## Do not

- Drop `DATA_DIR` or TOML users (T05–T07).
- Reintroduce `modernc.org/sqlite` behind a flag.
- Sleep on 420 inside ESI (existing rule).
- Change cache key algorithm or TTL policy.

## Context

Today:

- `internal/adapter/cache.Store` — SQLite (`http_cache`, `names`, `blobs`)
- `esi.New(..., store *cache.Store, ...)`
- `names.New(esi, store)`
- `session.Open` opens `cache.sqlite3` under `DATA_DIR`

Target: one `*store.Store` (or a narrow interface implemented by it)
passed from `session.Open`. Delete `internal/adapter/cache`.

Prefer a small interface in `adapter/esi` / `adapter/names` so those
packages do not need every store method — but a `*store.Store` pointer
is acceptable if it keeps the diff small.

`session.Open` must take a `*store.Store` (or `DATABASE_URL` and open
it). Opening the pool in `main` and passing it down is cleaner (one
pool for cache + later users). If `main` is not ready to parse
`DATABASE_URL`, add it in this task as **required** for boot, even if
users still sit on disk. Document it in `.env.example` as required for
run. Full env cleanup is T07/T11.

## Work

1. Point ESI + names at Postgres cache methods. Preserve
   `CachedResponse` freshness/age semantics.
2. `session.Open`: no `cache.sqlite3`. Accept `*store.Store`.
3. `cmd/eve-mcp`: `DATABASE_URL` required, `store.Open`, pass into
   `session.Open`. Fail boot with a clear error if DSN is missing.
4. Delete `internal/adapter/cache`. If nothing else imports
   `modernc.org/sqlite`, drop it from `go.mod` (`go mod tidy`). Keep
   `pelletier/go-toml` until T05.
5. Call `PurgeExpired` at startup (same idea as today’s SQLite purge).
6. Existing `adapter/esi/limit_test.go` must still pass (no DB).

## Files

- Edit: `adapter/esi/esi.go`, `adapter/names/names.go`,
  `usecase/session/session.go`, `cmd/eve-mcp/main.go`,
  `cmd/eve-mcp/config.go`, `.env.example`
- Delete: `internal/adapter/cache/`
- Maybe: `go.mod` / `go.sum`

## Acceptance

- [x] No `modernc.org/sqlite` in `go.mod` if unused
- [x] No `cache.sqlite3` path in the repo
- [x] Server refuses to start without `DATABASE_URL`
- [x] `go test ./internal/adapter/esi ./internal/adapter/store` pass
- [x] With Compose + `.env`, `./eve-mcp` boots and `GET /healthz` is ok

## Verify

```bash
make postgres
# .env has CLIENT_ID + DATABASE_URL
go test ./...
go build -o eve-mcp ./cmd/eve-mcp
./eve-mcp &
curl -s http://127.0.0.1:8766/healthz
# public / still serves; /mcp still 401 without a token
```

## Done

Set `Status: done` here and in [README.md](README.md).
