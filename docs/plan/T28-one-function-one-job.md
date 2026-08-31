# T28 — One function, one job; one result

- Status: `todo`
- Size: L
- Depends on: T27
- RULES: §15 (one function, one job), §10 (a function returns one
  result), §3 (the linter is the style), §6 (the code is the
  documentation)
- SPEC: §7 (Go layout)
- New in the 2026-08-31 audit; no §12 item

## Goal

RULES §15: *"Every function and method — exported or not — does one
thing. A function that fetches, decides, formats and writes is four
functions that have not been named yet. If the name needs 'and', split
it."* RULES §10: *"A function returns one value, or two when the second
is `error` or `bool`. Two business values are one result that has not
been named yet."*

Both are review opinions until a linter carries them, and
`.golangci.yml` currently disables `funlen`, `gocyclo` and `cyclop`.
RULES §3 says the linter is the style. So this task does the splitting
**and** turns the linters on, after which §15 enforces itself and no
later task can quietly regrow a 1400-line file.

It lands here because the tool surface stops moving at T27. Doing it
earlier means splitting functions that T24 and T27 were about to
rewrite.

## Why this is one Composer session

It is one rule applied everywhere, then one config change that locks it
in. Split across sessions, the linters can only be enabled at the end
anyway, and every earlier session's work would be re-litigated by the
last one.

## Do not

- `//nolint` a complexity finding to keep a shape you like. RULES §3:
  the directive is for a site where the linter is wrong and rewriting
  would make the code worse, it names the linter and the reason on the
  same line, and it is not a way to keep a shape.
- Disable a linter in `.golangci.yml` to silence one call site.
- Split by line count. A 70-line function that does one thing stays; a
  20-line one that fetches and decides does not. The linters are a floor
  under the rule, not the rule.
- Add a comment explaining what each extracted function does. The name
  does that (RULES §6); if it cannot, the split is in the wrong place.
- Change behaviour. Every existing test passes untouched, which is the
  only evidence a refactor of this size is safe.
- Extract a helper that takes nine parameters. A long parameter list is
  the same smell as a long body: it is a struct nobody has named.

## Context

The §10 sites, all of them returning two business values:

| Site | Returns |
|---|---|
| `usecase/eve/common.go` `page` | `([]map[string]any, map[string]any)` — T27 replaces it; verify |
| `usecase/eve/industry.go` `sumMining` | two maps |
| `usecase/eve/industry.go` `miningOreRows` | rows and a total |
| `adapter/esi/http/namecache.go` `get` | resolved names and missing ids |
| `tests/` — whatever the ported tool-definition check kept of `lintProps`, if it still answers with two slices |
| `usecase/session/persist.go` `GetConfirm` | three values — T15 fixes it; verify |

The §15 sites, longest first: `usecase/eve/corp.go` (1397 lines, and
`esiRole` is an 88-line `switch` that wants a set),
`adapter/esi/http/client.go` (830, with `request` at 70 and
`GetAllPages` at 71), `usecase/eve/universe.go` (579),
`adapter/sso/http/client.go` (559), `adapter/esi/http/resolver.go` (488,
moved down a level by T15),
`cmd/eve-mcp/main.go` `start` (84 lines doing config, database, session,
OAuth and listeners).

The long parameter lists: `industryJobsResult` (8),
`formatOrders` (9), `formatContracts` (10) — each one request struct.

`usecase/oauth` and `usecase/eve/writes.go` were split by T17 and T24;
confirm rather than redo.

## Work

1. The §10 sites: one named result struct each, `error` or `bool` in the
   second slot or nowhere.
2. The long parameter lists: one request struct each, named for what it
   describes.
3. `cmd/eve-mcp/main.go`: `start` becomes a composition of named steps —
   config, database, adapters, usecases, listeners.
4. Split the large files by subject, not by size: `corp.go` by tool
   area, `client.go` by concern (request, cache, retry, limits),
   `universe.go` and `resolver.go` the same.
5. `esiRole` becomes a lookup, not a `switch` with 88 arms.
6. Enable `funlen`, `gocyclo`, `cyclop`, `gocognit` and `nestif` in
   `.golangci.yml`, with the thresholds recorded in the commit message
   and no per-file exclusions. Remove them from the `disable` list and
   the comment that justified it.
7. `make lint` clean with zero new `//nolint` directives.
8. No new tests, and no changed **expectation**. A test may change only
   where it calls a signature this task changed — a new result struct
   read instead of two values, a request struct built instead of nine
   arguments. If an assertion has to change, the refactor changed
   behaviour: stop and find out which (RULES §4). Review the test diff
   for that specifically; it is the only evidence a refactor this size
   is safe.

## Files

- Edit: `.golangci.yml`, `cmd/eve-mcp/main.go`,
  `internal/adapter/esi/**`, `internal/adapter/sso/http/client.go`,
  `internal/usecase/eve/**`, `internal/usecase/session/persist.go`,
  `tests/`

## Acceptance

- [ ] No function returns two business values
- [ ] No function takes more than four parameters plus `ctx`
- [ ] `funlen`, `gocyclo`, `cyclop`, `gocognit` and `nestif` are enabled
      and `make lint` is clean
- [ ] `git diff` adds no `//nolint` directive
- [ ] No file in `internal/` is longer than 600 lines
- [ ] The test diff contains only call-shape updates — no changed
      assertion, no deleted case, no new test
- [ ] `go test ./...` and `make test-store` pass

## Verify

```bash
make lint
git diff -- '*_test.go'           # read it: call shapes only
wc -l $(git ls-files 'internal/**/*.go') | sort -n | tail -15
go test ./... -count=1
```

## Done

Set `Status: done` here and in [README.md](README.md).
