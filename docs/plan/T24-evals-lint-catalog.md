# T24 — `evals lint` against the catalog

- Status: `todo`
- Size: M
- Depends on: T22, T23
- SPEC: §4 (tool contract), §4.3, §12.13

## Goal

`docs/` is normative and the code follows it — which is a policy until
something checks it. This task makes the check real: `go run ./evals lint`
reads the hand-written contracts and diffs them against the running
server and the repo.

Three diffs:

1. **TOOLS.md ↔ `tools/list`** — names, per-parameter presence, type,
   required-ness, description, bounds and pagination parameter.
2. **TOOLS.md ↔ `instructions`** — the served string against the Server
   instructions block.
3. **ESI.md ↔ the call sites, both ways** — an endpoint called with no row
   fails, and a row naming a call site that does not exist fails. The
   second direction is the one that rots: the `newmail` row claimed a
   caller for weeks with nothing behind it.

## Why this is one Composer session

It is a parser, not a flag — which is exactly why it is not folded into
T22. Estimating it as part of "catalog conformance" is how it turns into
an afternoon that was supposed to be ten minutes.

## Do not

- Turn TOOLS.md into YAML or generate it from the code. It is
  hand-written on purpose: it is the document a human reviews before the
  model sees it, and a generated file cannot disagree with the code, which
  is the entire value.
- Compare descriptions loosely. Whitespace-normalise, then require
  equality. A "close enough" description check catches nothing.
- Skip the bounds column. Bounds live in `patchBounds` precisely because
  they must not be in tag text, so the served schema is where to read them.
- Fail on ESI.md's four rows that are ahead of the code without saying
  which, and why. Once T21 lands there should be none — if there are, name
  them.
- Require a live EVE character. `tools/list` needs a bearer, which the
  harness already handles; nothing here needs real account data.

## Context

`evals/main.go` already speaks JSON-RPC to a running server, already calls
`tools/list`, and already has a `lint` entry point — it just does not read
TOOLS.md. `initialize` returns the instructions string.

TOOLS.md's shape is regular enough to parse without a Markdown library:
`### \`eve_x\`` starts a tool, the prose until the next table is its
description, and the parameter table has fixed columns
(Parameter, Type, Required, Bounds, Description) with `shared` meaning
"look it up in the Conventions table".

The awkward parts, worth handling deliberately rather than discovering:
`shared` descriptions, the `modules` sub-table on `eve_fitting_save`, the
Bounds column's `—`, and bold `**yes**` in Required.

Call-site extraction for the third diff: every
`ESI.Get/GetAllPages/GetCursorPages/Post/Put/Delete` literal in
`internal/`, normalised by replacing format verbs with `{}`, matched
against ESI.md's method + path column normalised the same way.

## Work

1. TOOLS.md parser producing a tool map, resolving `shared` descriptions
   from the Conventions table.
2. Diff against `tools/list`: report every difference with tool name,
   field and both sides. Exit non-zero on any.
3. Diff the instructions string against the block.
4. Call-site extractor and the two-way ESI.md diff.
5. Wire all four into `lint`, keep the existing checks, and make the
   output readable when there are twenty findings rather than one.
6. Tests for the parser itself, including one deliberately broken
   fixture per rule — a parser with no tests is a second source of
   truth.

## Files

- Edit: `evals/main.go`
- Add: `evals/catalog.go`, `evals/catalog_test.go`, `evals/testdata/*`

## Acceptance

- [ ] `go run ./evals lint` is clean against the current server and docs
- [ ] Renaming a tool, changing a parameter type, dropping a bound, or
      editing a description in either place makes lint fail with a
      precise message
- [ ] Editing the instructions in code but not in TOOLS.md fails
- [ ] Adding an ESI call with no row fails; deleting a call site while
      leaving its row fails
- [ ] The parser has its own tests
- [ ] `go test ./...` passes

## Verify

```bash
go test ./evals -count=1
go run ./evals lint
```

## Done

Set `Status: done` here and in [README.md](README.md).
