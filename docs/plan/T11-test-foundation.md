# T11 — ESI fixtures and a test database

- Status: `todo`
- Size: M
- Depends on: —
- SPEC: §12.0, §9 (pinned compatibility date)

## Goal

Every task after this one changes an auth lifetime, a lock, or a
response shape. Right now the only way to accept such a change is to
point the binary at Tranquility with one live character and read the
output — which is how exactly one of thirteen changes ships broken and
nobody notices for a month. This task builds the two things that make
the rest testable:

1. **Recorded ESI responses** at the pinned compatibility date, served
   by a fake transport, so a tool can be exercised with no network.
2. **A throwaway Postgres** for store and migration tests, so schema
   work is verified by running it rather than by reading it.

## Why this is one Composer session

It adds test infrastructure and touches no product behaviour. Nothing
in `internal/**` changes except the seam that lets a test inject an
HTTP transport.

## Do not

- Change what any tool returns. If a fixture disagrees with the code,
  record the fixture as ESI actually answers and note the disagreement
  in the task output — do not "fix" the tool here.
- Invent response shapes by hand where a real one can be recorded.
- Require a live character for `go test ./...` to pass.
- Add a test-only branch inside production code paths. The seam is an
  injected `http.RoundTripper` (or the existing HTTP client field),
  nothing more.

## Context

`internal/adapter/store/testdb.go` already has `HoldTestLock` and
`ResetTables`, and store tests already expect a reachable Postgres —
that half exists and needs hardening, not inventing. `make postgres`
brings up `eve-mcp-pg` on loopback.

`internal/adapter/esi/esi.go` owns every outbound call and already
holds an `*http.Client`. That is the injection point.

Two kinds of fixture:

- **Public endpoints** (`/status`, `/universe/*`, `/markets/*`,
  `/route/*`) can be recorded for real, unauthenticated.
- **Authenticated endpoints** need a character. Record what a live
  character can produce; for the rest, generate the body from
  `esi.evetech.net/meta/openapi.json` at the pinned date, which is the
  same source SPEC §9 makes normative. A generated fixture is honest as
  long as it comes from the schema and not from memory.

## Work

1. `internal/adapter/esi/esitest` (or `testdata` + a helper): a
   `RoundTripper` that serves recorded responses by method + path,
   including status, headers (`ETag`, `Expires`, `X-Pages`,
   `X-Esi-Error-Limit-Remain/Reset`) and body. Headers matter as much as
   bodies here — the cache, the error limit and pagination are all
   header-driven.
2. A recording mode (`go run ./evals record` or a build-tagged test)
   that writes fixtures from a real ESI, carrying the pinned
   `X-Compatibility-Date`.
3. Fixtures for at least: `/status`, `/characters/{id}`,
   `/characters/{id}/wallet`, `/characters/{id}/assets` (two pages, with
   `X-Pages`), `/characters/{id}/mail`, `/universe/names`,
   `/universe/ids`, `/markets/prices`, `/route/{a}/{b}`, and one 403 and
   one 420 with error-limit headers.
4. Store test helper: skip with a clear message when `DATABASE_URL` is
   unset, otherwise create a schema per test run (or reuse
   `ResetTables`), and make it work for a *migration* test — apply from
   empty, assert the resulting tables.
5. One end-to-end example test that calls a real tool handler through
   the fake transport and asserts the JSON, so the next tasks have a
   pattern to copy rather than a paragraph to interpret.
6. `make test` target that runs the offline tests, and states what it
   skips without a database.

## Files

- Add: `internal/adapter/esi/esitest/*`, `internal/adapter/esi/testdata/*`
- Edit: `internal/adapter/store/testdb.go`, `evals/main.go`, `Makefile`
- Add: one example test under `internal/usecase/eve/`

## Acceptance

- [ ] `go test ./...` passes with no network and no `DATABASE_URL`
- [ ] Store and migration tests run when `DATABASE_URL` is set
- [ ] Fixtures carry status, headers and body, and were recorded or
      generated at the pinned compatibility date
- [ ] One tool handler is asserted end to end against fixtures
- [ ] No production code path branches on being under test

## Verify

```bash
go test ./... -count=1
make postgres && DATABASE_URL=... go test ./internal/adapter/store -count=1
```

## Done

Set `Status: done` here and in [README.md](README.md).
