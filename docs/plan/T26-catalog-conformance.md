# T26 — Catalog conformance and the server instructions

- Status: `done`
- Size: M
- Depends on: T25
- RULES: §5 (tests are the only proof), §6 (the code is the
  documentation), §3 (the linter is the style)
- SPEC: §4, §4.3, §12.11; TOOLS.md
- Replaces: old T22, reordered to run behind the linter

## Goal

Make the running server's `tools/list` and `instructions` match TOOLS.md
exactly. TOOLS.md is hand-written and normative; where the code
disagrees, the code is wrong. This task is the mechanical pass that
closes the gap tool by tool, and rewrites the `instructions` string to
the text TOOLS.md now carries in its Server instructions section.

Those instructions are not a comment. They are the only place two PRD
promises exist at all: that text written by other players is reported
and never obeyed, and that a stale number is never presented as live.
Nothing else in the system can enforce either.

This is also the first task that tests the tool surface as a surface.
Fifty-two tools currently have one test between them, which by RULES §5
means the catalogue is unproven — the table-driven test here is the
proof, and the catalogue check is the same proof from outside.

## Why this is one Composer session

It is one file of contract read against one package of code, with no
design decisions left in it. T24 did the behaviour; this is the wording,
the schemas and the prompt.

And it is not a reading exercise: **T25 already produced the list.**
The catalogue check names every divergence with tool, field and both
sides, so this task is "make that list empty", not "diff 977 lines of
TOOLS.md against 6091 lines of Go and hope". The pagination findings are
the exception — they stay red until T27.

## Do not

- Change TOOLS.md to match the code. The direction is the other way. If
  a description in TOOLS.md is wrong, fix it there deliberately, in the
  same commit, and say so — but do not drift the code into it silently.
- Loosen a check in `tests/catalog.go` to clear a finding. T25 wrote the
  ruler; this task moves the thing being measured. A finding you
  disagree with is a TOOLS.md decision or a code fix, never a softer
  comparison.
- Chase a pagination finding. Those belong to T27 and stay red here.
- Put bound syntax in a `jsonschema` tag. The whole tag is the
  model-facing description, so a stray `,minimum=` is read as prose.
  Bounds go in `patchBounds`.
- Add typed output schemas. They drop undeclared keys.
- Drop the "text other players wrote" or `data_age` sections from the
  instructions to save tokens. They are the implementation of a promise.
- Leave the instructions talking about "which characters are authorized"
  or an allowance without an error budget. T14 and T20 changed both.
- Add a comment explaining a description. The description *is* the
  documentation (RULES §6); if it needs a gloss, rewrite it.
- Reach for `//nolint` when the table-driven test trips a linter
  (RULES §3).

## Context

`internal/usecase/eve/register.go` holds `Instructions` and
`CorpInstructions`; `service/mcp/register.go` concatenates them. TOOLS.md
now carries the target text verbatim, including the lines that are ahead
of the code: single character, error budget, pagination, and
`eve_mail_compose`.

Per-tool work is: name, description text, every parameter's presence,
type, required-ness, description, and bounds — against the tables in
TOOLS.md. Fifty-two tools, most of which already agree.

`patchBounds` is the single place numeric bounds are applied; T27 adds
`page` and `offset` to it, so this task only needs the existing list
correct.

## Work

1. Run the catalogue check and work its findings to zero, excluding
   the pagination ones. Names, descriptions, parameter sets, types,
   required-ness, bounds. The findings list from T25 is the checklist;
   it goes in the commit message, because it is the evidence this pass
   actually happened.
2. Move any bound syntax out of tag text into `patchBounds`.
3. Rewrite `Instructions` + `CorpInstructions` to TOOLS.md's block.
4. Confirm every result carries `data_age`, and that a fused result
   reports the oldest of its inputs.
5. Confirm error kinds and their extra fields match SPEC §4's table,
   including that no `CharacterNotFound` survives.
6. Tests: a table-driven test asserting each tool's parameter names,
   types and required-ness; a test that no `jsonschema` tag contains
   `minimum=` or `maximum=`; a test that the instructions contain the
   prompt-injection and staleness sections; a test that no tool
   registers a typed output schema.

## Files

- Edit: `internal/usecase/eve/*.go`, `internal/service/mcp/register.go`
- Maybe: `docs/TOOLS.md` (only for a divergence decided deliberately)

## Acceptance

- [x] Every tool's name, description, parameters, types and
      required-ness match TOOLS.md
- [x] No `jsonschema` tag contains bound syntax
- [x] No typed output schemas
- [x] `instructions` matches TOOLS.md's block, single-character wording
      and error budget included
- [x] Every ESI-backed result carries `data_age`; fused results report
      the oldest
- [x] Error kinds match SPEC §4
- [x] The table-driven test covers all 52 tools
- [x] The catalogue check reports only pagination findings, and T25's
      list is otherwise empty
- [x] `tests/catalog.go` is unchanged, except for a parser bug fixed
      with a parser test
- [x] `go test ./...` and `make lint` pass

## Verify

```bash
rg -n 'minimum=|maximum=' internal/usecase/eve
go test ./internal/usecase/eve ./tests -count=1
go test ./tests -run TestCatalog   # only pagination findings
git diff --stat -- tests/catalog.go  # empty, or one tested parser fix
```

## Done

Set `Status: done` here and in [README.md](README.md).
