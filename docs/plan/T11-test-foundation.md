# T11 — Test foundation: generated mocks and recorded ESI

- Status: `todo`
- Size: L
- Depends on: —
- RULES: §13 (mocks are generated), §5 (tests are the only proof),
  §1 (time is not a test seam), §4 (a failing test is a diagnosis)
- SPEC: §12.0, §9 (pinned compatibility date)

## Goal

RULES §5 says a test is the only proof the code works, and RULES §13
says what a test may be made of. Neither is true in this tree yet:

- `internal/logtest.Silent` is a hand-typed `log.Logger` — RULES §13
  names it as the anti-pattern, by path. `internal/domain/write` has a
  hand-typed `memPersist` next to it.
- Every ESI test starts an `httptest` server with an inline handler, so
  it proves the code parses *the test's* opinion of ESI, not ESI.

Both halves of that get fixed here, because every task after this one
changes an auth lifetime, a lock, or a response shape, and there is
currently no way to accept such a change except pointing the binary at
Tranquility with one live character.

## Why this is one Composer session

It adds test infrastructure and touches no product behaviour. Nothing in
`internal/**` changes except two deletions, one seam that lets a test
inject an `http.RoundTripper`, and `go:generate` lines.

## Do not

- Hand-write another double. If an interface needs a stand-in, it is
  `mockgen` output (RULES §13).
- Scatter the mocks one package per interface, or one per domain. RULES
  §13 says *one* package, so that tests import one path and `go test`
  builds less. `.golangci.yml` carries a `depguard` exclusion for
  `internal/domain/**/mocks/**` written in anticipation of the other
  layout; with `internal/mocks` that line matches nothing and is deleted
  in this task rather than left as a fossil.
- Add a `Clock`, a `now func() time.Time`, a `WithClock` option or a
  `timeNow` field to make something testable (RULES §1). Time-dependent
  behaviour is tested with `testing/synctest`; the production function
  keeps calling `time.Now()` itself.
- Wrap a test that talks to Postgres or a real socket in
  `synctest.Test`. I/O outside the bubble is not durably blocking and
  the bubble deadlocks (RULES §1).
- Add a test-only branch inside a production code path. The seam is the
  injected `http.RoundTripper` on the shared client, nothing more.
- Change what any tool returns. If a fixture disagrees with the code,
  record the fixture as ESI actually answers and note the disagreement
  in the task output — do not "fix" the tool here.
- Invent response shapes by hand where a real one can be recorded.
- Require a live character for `go test ./...` to pass.

## Context

**The Postgres half already landed** and is not re-done here:
`internal/adapter/store/storetest` applies `sql/` with goose against a
per-run database, `ResetTables` and `HoldTestLock` live in `testdb.go`,
and `storetest/migrate_test.go` asserts that `Store.Open` applies no SQL
(RULES §14). It moves to `internal/postgres/pgtest` in T15; leave it
where it is.

Interfaces that need a generated mock, all ours except the last:
`esi.Client` and `esi.TokenSource`, `sso.Client`, `write.Persist`, and
the `Repository` of `domain/{authcode,character,confirm,loginstate,
oauthclient}`. `log.Logger` belongs to `github.com/truewebber/gopkg` —
RULES §13 covers a dependency's interface too, and generating it is what
deletes `internal/logtest`.

`internal/adapter/esi/http/client.go` owns every outbound call and takes
an `*http.Client` at construction. That is the injection point.

Two things that look like walls and are not, worth knowing before you
hit them. A generated `log.Logger` mock fails a test on any unexpected
call, and most tests log incidentally without caring — a shared setup
helper that puts `.AnyTimes()` on every method is the answer. That is
configuration of a generated mock, not a hand-written double, and it is
what §13 leaves room for. And a tool handler takes a `*session.Session`,
which carries the Guard and therefore a database; pick a read-only tool
for the example test, where the Guard is never consulted, rather than
inventing a way to build half a session.

Two kinds of fixture:

- **Public endpoints** (`/status`, `/universe/*`, `/markets/*`,
  `/route/*`) can be recorded for real, unauthenticated.
- **Authenticated endpoints** need a character, so **the host records
  them**, not this session. The task ships a recording mode and the
  operator runs it once against their own character; the bodies land in
  `testdata` and are reviewed for anything personal before they are
  committed. That is an operator step, and it is the difference between
  a fixture and a guess — three later tasks (T20, T24, T27) accept
  themselves against these files.
- If a recorded body is not there yet, generate it from
  `esi.evetech.net/meta/openapi.json` at the pinned date — the same
  source SPEC §9 makes normative — and **say in the task output which
  fixtures are schema-generated**. A generated fixture is honest as long
  as it comes from the schema and is labelled; one written from memory
  is a test that agrees with whoever wrote it.

## Work

1. Add `go.uber.org/mock` and `mockgen` as a `tool` directive. A
   `make generate` target runs `go generate ./...`.
2. `//go:generate mockgen` on every interface listed above, adapters and
   domains alike, all output into one package: `internal/mocks`. It
   sits above the layer split because it imports from both sides, and
   only tests import it. Drop the now-dead `internal/domain/**/mocks/**`
   line from `depguard`'s `domain-boundaries` rule.
3. Delete `internal/logtest`. Every call site takes the generated
   `log.Logger` mock with the calls it expects, or the real logger where
   the test asserts nothing about logging.
4. Delete `internal/domain/write/persist_mem_test.go`; `guard_test.go`
   drives the generated `write.Persist` mock.
5. `internal/adapter/esi/http/esitest`: a `RoundTripper` that serves
   recorded responses by method + path, including status, headers and
   body. Headers are the point — the cache, the error limit and
   pagination are all header-driven, so `ETag`, `Expires`,
   `Cache-Control`, `X-Pages` and `X-Esi-Error-Limit-Remain/Reset` are
   part of the fixture, not decoration.
6. A recording mode in the standard Go golden-file shape — an `-update`
   flag on the fixture test — that writes fixtures from real ESI
   carrying the pinned `X-Compatibility-Date`. It is the only thing in
   the tree that talks to CCP, and it never runs in CI.
7. Fixtures for at least: `/status`, `/characters/{id}`,
   `/characters/{id}/wallet`, `/characters/{id}/assets` (two pages, with
   `X-Pages`), `/characters/{id}/mail`, `/universe/names`,
   `/universe/ids`, `/markets/prices`, `/route/{a}/{b}`, one 403, and
   one 420 with error-limit headers.
8. One end-to-end example test that calls a real tool handler through
   the fake transport and asserts the JSON, so the next tasks have a
   pattern to copy rather than a paragraph to interpret.
9. `make test` runs the offline tests and states what it skips without a
   database; `make test-store` keeps the rest.

## Files

- Add: `internal/mocks/*` (generated), `internal/adapter/esi/http/esitest/*`,
  `internal/adapter/esi/http/testdata/*`, one example test under
  `internal/usecase/eve/`
- Edit: `internal/adapter/esi/http/client.go` (transport seam only),
  `internal/domain/write/guard_test.go`, every test importing `logtest`,
  `Makefile`, `go.mod`, `.golangci.yml`
- Delete: `internal/logtest/`, `internal/domain/write/persist_mem_test.go`

## Acceptance

- [ ] `rg -n 'logtest|memPersist|type (mem|stub|fake|silent)[A-Z]'`
      finds nothing outside `internal/mocks`
- [ ] Every interface we own, plus `log.Logger`, has a generated mock in
      `internal/mocks` and nowhere else; `make generate` leaves a clean
      tree
- [ ] `.golangci.yml` carries no exclusion for a mocks path that does
      not exist
- [ ] `rg -n 'Clock|now func\(\)|WithClock|timeNow'` finds nothing
- [ ] `go test ./...` passes with no network and no `DATABASE_URL`
- [ ] Store and migration tests still run when `DATABASE_URL` is set
- [ ] Fixtures carry status, headers and body at the pinned
      compatibility date, and every schema-generated one is named as
      such in the task output
- [ ] `-update` re-records fixtures end to end, so the host can fill in
      the authenticated ones with one `go test` invocation
- [ ] One tool handler is asserted end to end against fixtures
- [ ] No production code path branches on being under test
- [ ] `make lint` is clean

## Verify

```bash
make generate && git diff --exit-code
go test ./... -count=1
make test-store
make lint
```

## Done

Set `Status: done` here and in [README.md](README.md).
