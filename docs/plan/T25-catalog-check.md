# T25 — The catalogue check: TOOLS.md and ESI.md as tests

- Status: `done`
- Size: M
- Depends on: T24
- RULES: §5 (tests are the only proof), §10 (one result),
  §15 (one function, one job)
- SPEC: §4 (tool contract), §4.3, §12.13
- Replaces: old T24 (the linter), moved ahead of the conformance pass

## Goal

`docs/` is normative and the code follows it — which is a policy until
something checks it. This task makes the check real: a test in `tests/`
reads the hand-written contracts and diffs them against the running
server and the repo.

**It runs before the conformance pass on purpose.** T26 has to reconcile
52 tools across six fields each — 977 lines of TOOLS.md against 6091
lines of `usecase/eve` — and T27 has to place nineteen tools in four
pagination classes. Done by hand, both are a diff nobody can prove they
finished. Done against this check, both are a list that shrinks to zero.
So this task's output is *a failing test with a complete list of
findings*, and the two tasks after it are what make it pass.

Three diffs:

1. **TOOLS.md ↔ `tools/list`** — names, per-parameter presence, type,
   required-ness, description, bounds and pagination parameter.
2. **TOOLS.md ↔ `instructions`** — the served string against the Server
   instructions block.
3. **ESI.md ↔ the call sites, both ways** — an endpoint called with no
   row fails, and a row naming a call site that does not exist fails.
   The second direction is the one that rots: the `newmail` row claimed
   a caller for weeks with nothing behind it.

## Why this is one Composer session

It is a parser, not a flag — which is exactly why it is not folded into
the conformance pass. Estimating it as part of "catalog conformance" is
how it turns into an afternoon that was supposed to be ten minutes.

## Do not

- **Tune the check until it passes.** The findings are the point. A test
  written to agree with the code it tests is a test that can never
  disagree with it, which is the one thing it exists to do. If a finding
  looks wrong, the answer is a parser test proving TOOLS.md says what
  you think it says — not a loosened comparison.
- Fix a single tool while you are here. This task ships the ruler; T26
  and T27 ship the conformance. Mixing them means the same session both
  writes the ruler and decides what it measures.
- Build a CLI for it. It is a `go test` in `tests/`, run by the same
  pipeline as everything else — the hand-rolled runner is what T13
  deleted.
- Turn TOOLS.md into YAML or generate it from the code. It is
  hand-written on purpose: it is the document a human reviews before the
  model sees it, and a generated file cannot disagree with the code,
  which is the entire value.
- Compare descriptions loosely. Whitespace-normalise, then require
  equality. A "close enough" description check catches nothing.
- Skip the bounds column. Bounds live in `patchBounds` precisely because
  they must not be in tag text, so the served schema is where to read
  them.
- Fail on ESI.md rows that are ahead of the code without saying which,
  and why. After T24 there should be none — if there are, name them.
- Require a live EVE character. `tools/list` needs a bearer, which T13's
  harness already mints; nothing here needs real account data.
- Ship the parser without tests. A parser with no tests is a second
  source of truth (RULES §5).
- Return `(tools, warnings, err)` from the parser. One result
  (RULES §10).

## Context

T13's harness already boots the server in-process against a throwaway
Postgres and the fixture transport, and hands back a bearer. `tools/list`
and the `instructions` string from `initialize` are two calls away.

TOOLS.md's shape is regular enough to parse without a Markdown library:
`### \`eve_x\`` starts a tool, the prose until the next table is its
description, and the parameter table has fixed columns (Parameter, Type,
Required, Bounds, Description) with `shared` meaning "look it up in the
Conventions table".

The awkward parts, worth handling deliberately rather than discovering:
`shared` descriptions, the `modules` sub-table on `eve_fitting_save`,
the Bounds column's `—`, and bold `**yes**` in Required.

Call-site extraction for the third diff: every
`ESI.Get/GetAllPages/GetCursorPages/Post/Put/Delete` literal in
`internal/`, normalised by replacing format verbs with `{}`, matched
against ESI.md's method + path column normalised the same way. The docs
live above `tests/`, so the test finds them from the module root, not a
relative guess.

The parser is its own file with its own unit test; the diff is the test
that consumes it (RULES §15).

## Work

1. TOOLS.md parser producing one result — a tool map — resolving
   `shared` descriptions from the Conventions table.
2. Diff against `tools/list`: one `t.Errorf` per difference, naming tool,
   field and both sides.
3. Diff the instructions string against the block.
4. Call-site extractor and the two-way ESI.md diff.
5. Make the output readable when there are twenty findings rather than
   one — because there will be. Group by tool, then by field.
6. Unit tests for the parser itself, including one deliberately broken
   fixture per rule.
7. Run it and **write the findings down** in the task output, grouped
   into "T26 fixes this" (names, descriptions, types, required-ness,
   bounds, instructions) and "T27 fixes this" (missing pagination
   parameters and response fields). That list is this task's real
   deliverable; the two tasks after it are graded against it.

## Files

- Add: `tests/catalog_test.go`, `tests/catalog.go` (the parser),
  `tests/catalog_parser_test.go`, `tests/testdata/*`

## Acceptance

- [x] `go test ./tests -run TestCatalog` fails against the current
      server, naming every divergence with tool, field and both sides.
      It is **not** expected to pass — T26 and T27 make it so
- [x] The findings are written down and split by which task fixes them
- [x] Renaming a tool, changing a parameter type, dropping a bound, or
      editing a description in either place makes it fail with a precise
      message
- [x] Editing the instructions in code but not in TOOLS.md fails
- [x] Adding an ESI call with no row fails; deleting a call site while
      leaving its row fails
- [x] The parser has its own unit tests, one per rule, including broken
      fixtures
- [x] No tool description, schema or `patchBounds` entry changed in this
      commit — the ruler does not move the thing it measures
- [x] `make lint` passes, and `go test ./...` fails only on this new
      check

## Verify

```bash
go test ./tests -run TestCatalogParser -count=1   # green
go test ./tests -run TestCatalog -count=1         # red, with the findings
git diff --stat -- internal/usecase/eve           # must be empty
```

## Findings

`go test ./tests -run TestCatalog` (needs `DATABASE_URL`) reports **43**
findings. ESI.md ↔ call sites is clean after T24 — no row without a
caller, no caller without a row.

### T26 fixes this

Names, descriptions, types, required-ness, bounds, instructions.

**`instructions`** — the served string still talks about "one player's
own account", names CCP headers on `EsiRateLimited`, and omits the
single-character wording, error budget, pagination walk, and
`eve_mail_compose`. TOOLS.md's Server instructions block is the target.

**Tool descriptions** (TOOLS.md has the extra sentence unless noted):

- `eve_auth_status` — "who this connection is" vs "who is authorized here"
- `eve_auth_logout` — revoke-and-sign-out wording vs soft-delete identity
- `eve_calendar_list` — missing Returns/`next_cursor` line
- `eve_mail_list` — missing "next_cursor when older mail exists"
- `eve_mail_read` — missing "text other players wrote" sentence
- `eve_mail_mark` — code adds "Needs a confirm_token in confirm mode"
- `eve_mail_compose` — missing "prefer over send" and "window requested" paragraphs
- `eve_mail_send` — missing compose/cap closing sentence
- `eve_mail_read.mail_id` — schema description still carries `,minimum=1`
- `eve_market_contracts` — "within the page" + `page, total_pages` in Returns
- `eve_social_killmails` — Returns missing `page, total_pages`
- `eve_assets_blueprints` — Returns missing "the counts describe the page"
- `eve_ui_open_window` — refuse-with-the-list vs "outside market, info or contract"
- `eve_market_price.history_days` — `,minimum=0,maximum=365` left in the tag
- `eve_contacts_set.standing` — `,minimum=-10,maximum=10` left in the tag
- `eve_assets_list` / `eve_corp_assets_list` `min_value` — `,minimum=0` in the tag
- `eve_assets_list` / `eve_corp_assets_list` `items` — `,minimum=1,maximum=200` in the tag

**Bounds** (schema vs Bounds column; bound syntax in tags is the same T26 pass):

- `min_value` on both asset-list tools: TOOLS.md `≥ 0`, schema has none
- `items` on both asset-list tools: TOOLS.md `1–200`, `patchBounds` applies `1–500`
- `standing` on `eve_contacts_set`: TOOLS.md `−10–10`, schema has none

### T27 fixes this

Missing pagination **input** parameters (TOOLS.md table vs `tools/list`).
`tools/list` has no output schema, so `next_cursor` / `page`+`total_pages`
/ `total` are not findings here — they travel with the same class.

**`page` absent:** `eve_assets_blueprints`, `eve_market_contracts`,
`eve_social_killmails`, `eve_corp_blueprints`, `eve_corp_contracts`,
`eve_corp_industry_jobs`, `eve_corp_killmails`, `eve_corp_orders`,
`eve_corp_structures`.

**`offset` absent:** `eve_assets_list`, `eve_assets_find`,
`eve_industry_mining`, `eve_wallet_history`, `eve_corp_assets_list`,
`eve_corp_assets_find`, `eve_corp_mining`, `eve_corp_wallet`.

**Cursor absent:** `eve_mail_list.last_mail_id`.
`eve_calendar_list.from_event` already matches.

## Done

Set `Status: done` here and in [README.md](README.md).
