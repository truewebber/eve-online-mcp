# T22 — Catalog conformance and the server instructions

- Status: `todo`
- Size: M
- Depends on: T21
- SPEC: §4, §4.3, §12.11; TOOLS.md

## Goal

Make the running server's `tools/list` and `instructions` match TOOLS.md
exactly. TOOLS.md is hand-written and normative; where the code disagrees,
the code is wrong. This task is the mechanical pass that closes the gap
tool by tool, and rewrites the `instructions` string to the text TOOLS.md
now carries in its Server instructions section.

Those instructions are not a comment. They are the only place two PRD
promises exist at all: that text written by other players is reported and
never obeyed, and that a stale number is never presented as live. Nothing
else in the system can enforce either.

## Why this is one Composer session

It is one file of contract read against one package of code, with no
design decisions left in it. T21 did the behaviour; this is the wording,
the schemas and the prompt.

## Do not

- Change TOOLS.md to match the code. The direction is the other way. If a
  description in TOOLS.md is wrong, fix it there deliberately, in the same
  commit, and say so — but do not drift the code into it silently.
- Put bound syntax in a `jsonschema` tag. The whole tag is the
  model-facing description, so a stray `,minimum=` is read as prose.
  Bounds go in `patchBounds`.
- Add typed output schemas. They drop undeclared keys.
- Drop the "text other players wrote" or `data_age` sections from the
  instructions to save tokens. They are the implementation of a promise.
- Leave the instructions talking about "which characters are authorized"
  or an allowance without an error budget. T14 and T18 changed both.

## Context

`internal/usecase/eve/register.go` holds `Instructions` and
`CorpInstructions`; `service/mcp/register.go` concatenates them. TOOLS.md
now carries the target text verbatim, including the lines that are ahead
of the code: single character, error budget, pagination, and
`eve_mail_compose`.

Per-tool work is: name, description text, every parameter's presence,
type, required-ness, description, and bounds — against the tables in
TOOLS.md. Fifty-two tools, most of which already agree.

`patchBounds` is the single place numeric bounds are applied; T23 adds
`page` and `offset` to it, so this task only needs the existing list
correct.

## Work

1. Read TOOLS.md tool by tool against `usecase/eve/*.go`. Fix names,
   descriptions, parameter sets, types and required-ness. Keep a list of
   every divergence found — it goes in the commit message, because it is
   the evidence this pass actually happened.
2. Move any bound syntax out of tag text into `patchBounds`.
3. Rewrite `Instructions` + `CorpInstructions` to TOOLS.md's block.
4. Confirm every result carries `data_age`, and that a fused result
   reports the oldest of its inputs.
5. Confirm error kinds and their extra fields match SPEC §4's table,
   including that no `CharacterNotFound` survives.
6. Tests: a table-driven test asserting each tool's parameter names,
   types and required-ness; a test that no `jsonschema` tag contains
   `minimum=` or `maximum=`; a test that the instructions contain the
   prompt-injection and staleness sections.

## Files

- Edit: `internal/usecase/eve/*.go`, `internal/service/mcp/register.go`
- Maybe: `docs/TOOLS.md` (only for a divergence decided deliberately)

## Acceptance

- [ ] Every tool's name, description, parameters, types and
      required-ness match TOOLS.md
- [ ] No `jsonschema` tag contains bound syntax
- [ ] No typed output schemas
- [ ] `instructions` matches TOOLS.md's block, single-character wording
      and error budget included
- [ ] Every ESI-backed result carries `data_age`; fused results report
      the oldest
- [ ] Error kinds match SPEC §4
- [ ] `go test ./...` passes and `go run ./evals lint` is clean

## Verify

```bash
rg -n 'minimum=|maximum=' internal/usecase/eve
go test ./internal/usecase/eve -count=1
go run ./evals lint
```

## Done

Set `Status: done` here and in [README.md](README.md).
