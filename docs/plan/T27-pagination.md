# T27 — Pagination across the list tools

- Status: `todo`
- Size: L
- Depends on: T26
- RULES: §10 (a function returns one result), §15 (one function, one
  job), §5 (tests)
- SPEC: §4 (pagination contract), §12.12; TOOLS.md "Pagination by tool"
- Replaces: old T23, reordered to run behind the linter

## Goal

Today nothing past the first `limit` rows is reachable by any tool:
there is no page, no cursor, no offset anywhere. A player cannot read
older mail or see the 600th item in a hangar, while PRD promises full
asset lists and wallet movements over time.

The fix mirrors ESI rather than inventing a scheme, in four classes:

- **Cursor** where the endpoint pages by id: `eve_mail_list`
  (`last_mail_id`), `eve_calendar_list` (`from_event`). The tool exposes
  the same cursor under the same name and returns `next_cursor`.
- **`page`** where the endpoint pages by number and the tool keeps its
  order: nine tools. The result carries `page` and `total_pages`, and
  its header counts describe the page it returned.
- **`offset`** where the tool folds or re-sorts everything it read:
  eight tools. It reads to its page cap and paginates its own assembled
  output, returning `total`.
- **Nothing** where ESI answers in one response: ten tools.
  Completeness comes from filters; inventing a parameter here would
  misplace the truncation.

TOOLS.md's Pagination table is the assignment, tool by tool. It is not a
judgement call per tool.

## Why this is one Composer session

Nineteen tools, one rule, one shared helper per class. Splitting it by
domain would spread the same three helpers across three sessions and
guarantee they diverge.

The nineteen are not something to find, either: after T25 and T26 the
only findings the catalogue check still reports are the pagination
ones, so the task starts with the list and ends when it is empty.

## Do not

- Return `(rows, nextCursor, total, err)` from a helper. RULES §10: one
  named result — a `Page` carrying rows and whichever of
  `next_cursor` / `total` / `total_pages` its class produces. This also
  fixes `page()` in `common.go`, which returns two business values
  today.
- Give a folding tool a `page` parameter. The second ESI page of raw
  asset rows is not "the second page of locations", and grouping by it
  produces wrong totals — that is why the class exists.
- Give a passthrough tool an `offset`. One tool call is one ESI request
  there; the caller's page number is the bound, and the page cap does
  not apply.
- Apply a page cap to a passthrough tool, or remove it from a folding
  one.
- Silently drop rows the caller has no parameter to reach. Every
  truncated result says so.
- Change what `limit` means. It bounds one page, never the query.
- Rename ESI's cursor parameters. Keeping `last_mail_id` and
  `from_event` is what makes TOOLS.md and ESI.md checkable against each
  other.
- Add pagination to a tool TOOLS.md puts in the fourth class.
- Write one helper that handles all three classes with a mode flag. Three
  helpers, three names (RULES §15).
- Loosen `tests/catalog.go` to clear the last findings. Same rule as
  T26: the ruler does not move.

## Context

`internal/adapter/esi` already has both paging helpers — `GetAllPages`
(number-paged) and `GetCursorPages` — so a passthrough is a single-page
`Get` with the caller's number, and a fold keeps using `GetAllPages` up
to its cap.

Page caps stay where SPEC §4.2 puts them, and now apply only to the
folding class: assets 80, most others 40, wallet journal 10, mining
observer detail 10.

Response fields by class: `next_cursor` (cursor), `page` +
`total_pages` (passthrough, from `X-Pages`), `total` (fold). `X-Pages`
is a response header, so the passthrough helper has to surface it rather
than only the body — and it surfaces it inside the result struct, not as
an extra return value.

`eve_wallet_history` and `eve_corp_wallet` are folds even though their
rows are one-to-one with the endpoint's, because they summarise the
window they read. Their internal transaction cursor stays internal.

## Work

1. Three small helpers with one result type each: cursor passthrough,
   single-page passthrough with `X-Pages`, and offset-over-assembled-
   output.
2. Apply per TOOLS.md's table, 19 tools.
3. `patchBounds`: `page` ≥ 1, `offset` ≥ 0, cursors ≥ 1.
4. Header counts in the passthrough class describe the returned page,
   and the result says which page of how many; fold headers keep
   describing the whole window read, and say how deep that went.
5. Tests against T11's fixtures (record a two-page endpoint if it is not
   there yet): a cursor round-trip reaches the second batch; `page=2`
   fetches exactly one request and reports `total_pages`; `offset`
   walks a folded list and totals stay stable across offsets; a
   fourth-class tool has no pagination parameter; a truncated result is
   labelled.

## Files

- Edit: `internal/adapter/esi/esi.go` and
  `internal/adapter/esi/http/client.go` (helpers, `X-Pages` surfacing),
  `internal/usecase/eve/*.go` (19 tools),
  `internal/usecase/eve/common.go` (`patchBounds`, `page()`)

## Acceptance

- [ ] Every tool's class matches TOOLS.md's Pagination table
- [ ] Cursor tools return `next_cursor`; passthrough tools return `page`
      and `total_pages`; folds return `total`
- [ ] Every paging helper returns one result plus `error`
- [ ] A passthrough call makes one ESI request and ignores page caps
- [ ] Folds still honour their caps
- [ ] Bounds are in `patchBounds`, not in tag text
- [ ] No list tool can truncate without saying so
- [ ] The catalogue check is **fully clean** — this is the task that
      empties T25's list
- [ ] `tests/catalog.go` is unchanged
- [ ] `go test ./...` and `make lint` pass

## Verify

```bash
go test ./internal/usecase/eve ./internal/adapter/esi/... ./tests -count=1
go test ./tests -run TestCatalog   # green
git diff --stat -- tests/catalog.go  # empty
```

## Done

Set `Status: done` here and in [README.md](README.md).
