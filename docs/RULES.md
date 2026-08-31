# eve-mcp — Development Rules

**This document is normative.** These rules are not discussed. A PR, a
task file, a "for testability" comment, or a review note that disagrees
with this file is wrong. The product contracts live in
[PRD.md](PRD.md), [SPEC.md](SPEC.md), [TOOLS.md](TOOLS.md),
[ESI.md](ESI.md), [AUTH.md](AUTH.md) and [DB.md](DB.md). This file owns
how the code that implements them is written.

The sections below are the first unbreakable rules. Later sections may
be added; none of these is weakened by that.

## 1. Time is not a test seam

Production types do not carry a clock.

`time.Now` belongs in one of two places:

1. **Inside the function that needs "now".** The implementation reads
   the clock at the moment it runs. That call is a local detail of the
   function — not a field, not a constructor argument, not an interface.
2. **As a function argument that is part of the business question.**
   "Count mail since `since`", "is this session live at `at`", "expire
   everything before `cutoff`". The instant is domain input. It is not
   a hook so a test can pretend it is Tuesday.

**Forbidden** — these are the same crime:

- `now func() time.Time` on a struct
- a `Clock` interface, `NowFunc`, `timeNow` field, `WithClock` option
- storing `time.Now` (the function) so a test can overwrite it
- threading "current time" through constructors, options, or context
  *so that tests can stub it*

A `time.Time` **value** on a struct is data (`ExpiresAt`, `CreatedAt`,
`valid_til`). That is not a clock. Defaulting a zero `CreatedAt` to
`time.Now()` *inside the write* is form (1) and is fine.

**Tests do not receive a clock.** Time-dependent and concurrent
behaviour is tested with
[`testing/synctest`](https://pkg.go.dev/testing/synctest).
`synctest.Test` runs the real code in a bubble whose `time.Now`,
`time.Sleep` and timers are virtual; `synctest.Wait` (or
`synctest.Sleep`) waits until the bubble is durably blocked. The
production function still calls `time.Now()` itself.

When the instant is domain input, the test passes a `time.Time`. That
is exercising the API, not stubbing a clock.

A store test may persist a past timestamp
(`UPDATE … SET created_at = now() - interval '10 minutes'`) and then
run the real path. The clock is still Postgres `now()`, not a field on
`Store`. Do not wrap a test that talks to a real database or a real
network in `synctest.Test`: I/O outside the bubble is not durably
blocking and the bubble deadlocks.

## 2. Postgres constraints are not control flow

Write SQL that does not lose. Do not catch a unique, check, foreign-key
or exclusion violation and translate it into a business error.

The statement itself handles the race: `ON CONFLICT DO UPDATE`,
`ON CONFLICT DO NOTHING`, a `WHERE` on the conflict target,
`INSERT … SELECT`, `FOR UPDATE`, an advisory lock. A constraint that
still fires is a bug in the statement or the schema, and the error
propagates.

`RowsAffected() == 0` after a predicated `UPDATE` or
`INSERT … ON CONFLICT … WHERE` is a result, not a violation — that is
how ownership refuse works today and it stays.

**Forbidden:** branching on `pgconn.PgError`,
`pgerrcode.UniqueViolation`, `23505`, `23503`, `23514`, or any other
constraint SQLSTATE as if it meant "already exists", "owned by someone
else", or "token reused".

## 3. The linter is the style

[`golangci-lint`](https://golangci-lint.run/) as configured in
`.golangci.yml` is how this repository is written. `make lint` is the
contract. You obey it.

A lint finding is fixed by changing the code. `make lint-fix` is the
first move; read the diff.

`//nolint` is allowed only when, at this exact site, the linter is
wrong and rewriting the code would make it worse. The directive names
the linter and the reason on the same line. It is not a way to keep a
shape you like. Do not disable a linter in `.golangci.yml` to silence
one call site.

## 4. A failing test is a diagnosis

A red test has exactly two possible causes: the test is wrong, or the
business logic is. Find which, then fix that one.

- The test encodes a business case the product still promises, and the
  code disagrees → fix the code.
- The test asserts something the product does not promise, or the
  contract changed with the code → fix the test.

**Tests are deleted only when they do not help verify business logic**
— a duplicate of another case, an assertion about an implementation
detail that is not a product promise, or coverage of a behaviour that
was removed. A failing test is never deleted to go green.

## 5. Tests are the only proof the code works

Reading the code, running the binary once, and "it looks right" are not
feedback. A test that exercises the business case is.

Every new piece of business behaviour lands with tests for the cases
that matter: the happy path, the refuse, the expiry, the cap, the
ownership, the one-time consume — whatever the change claims. A change
that cannot be shown to work by a test has not landed.

## 6. The code is the documentation

Names, types, control flow and tests say what we meant. If a reader
cannot see it on the first pass, the code is wrong — rewrite it until
they can. A comment that narrates the next line, restates a name, or
apologises for a tangle is not documentation; it is the tangle staying.

A comment is allowed only in a rare, exceptional place, and only when
it keeps a business rule obvious without forcing the code into a worse
shape. It explains *why this rule is this shape*, never *what the next
statement does*. If the comment is doing the work the names should do,
delete the comment and rename.

## 7. Only gopkg/log writes to stdout and stderr

[`github.com/truewebber/gopkg/log`](https://github.com/truewebber/gopkg)
is the only writer to the process stdout and stderr. Help, fatals,
CLI reports, debug prints — if it would appear on std, it goes through
`log.Logger`. An HTTP response or a file is not std.

The logger is a **dependency**, and nothing else. `main` constructs it
(`log.NewLogger()`), `Close`s it on shutdown, and passes `log.Logger`
in. A type that logs has that field, set at construction. Nobody else
creates a logger, looks one up, or pulls one from context.

## 8. URLs are assembled, never concatenated

A host, a path, a query, a fragment — none of them is glued with `+`
or `fmt.Sprintf`. The string form of a URL is the output of
`url.URL`, `url.Values`, `url.JoinPath` / `path.Join`, and
`net.JoinHostPort`. That is what keeps `../`, a second host, or an
unescaped value out of the request — the same class of bug as SSRF,
open redirects, XSS in a `Location`, and an IDOR that is just an id
spliced into a path.

Only the host is configured, and it is a dependency. Endpoints are
constants in the code. Parsing a base URL and `JoinPath` on every
call is assembling a host that was already injected — keep `url.URL`
(or the host) on the client, set `Path` and `RawQuery` from the
hardcoded endpoint. `filepath.Join` is for files.

## 9. The user sees only static errors

The transport is the only place an error becomes a response. It logs
the internal error and returns a static message (and kind) from a
fixed catalog. Inner layers return real Go errors so the log can say
what broke; those strings never cross the edge — not in JSON, not in
HTML, not in a `Location`.

Validation may name the field that failed. The field name and the
invariant are still static (`character_id` / `must be a positive
integer`), not `err.Error()` from a parser or the database. If a case
has no catalog entry, the user gets the generic static error and the
real one stays in the log.

## 10. A function returns one result

A function or method returns one value, or two when the second is
`error` or `bool`. That second slot is the control channel — failed,
or not found / not present — not another piece of the answer. Two
business values are one result that has not been named yet: put them
in a struct.

A signature we do not own (generated code, an SDK callback) is not
this rule.

## 11. Each layer owns its types

`service`, `usecase`, `domain` and `adapter` each speak only their
own types (SPEC §7). A public signature of a layer accepts and returns
that layer's types: the service type is the service's representation
to the outside, the usecase type is the usecase contract. Domain and
adapter operate in their own types the same way.

Crossing a boundary is a map, not a leak. `service` does not expose a
usecase or adapter type; `usecase` does not take or return a domain
or adapter type; nobody reaches through a layer to borrow its
neighbor's struct. A type that leaks is an import that already
violates `service → usecase → adapter | domain`.

A signature we do not own (generated OpenAPI, an SDK callback) lives
in `service`. `int`, `string` and other language builtins are not a
layer's types.

## 12. SQL is declared

Every query is a `const`: plain SQL or a query template. `Exec`,
`Query` and `QueryRow` receive that name. A string literal at the
call is not a query; it is an undeclared one.

A migration file is already a declared query. `$1` is a parameter,
not concatenation.

## 13. Mocks are generated

A test double for an interface is `mockgen` output
(`go.uber.org/mock`), driven by `go:generate` — ours or a
dependency's. A handwritten `Silent`, `memFoo` or stub that
implements the interface is a mock typed by hand; delete it and
generate. `internal/logtest` is that anti-pattern.

Keep generated mocks in one package where that is the natural home,
so tests import fewer packages and `go test` builds less. A real
Postgres, `httptest` or `synctest` is not a mock.

## 14. The application does not migrate

Schema migrations are an operator step or CI/CD, never a path inside
the running server. `Open` connects; it does not apply SQL. A pod
that migrates on boot is a migrator wearing a server binary.

The files still live with the schema they change (DB.md). Applying
them is `goose`, a Makefile target, a pipeline job, or a pair of
hands — not `Store.Open`.

## 15. One function, one job

Every function and method — exported or not — does one thing. That is
the Go way: short, obvious, not clever. A function that fetches,
decides, formats and writes is four functions that have not been
named yet.

Complicated business logic is those simpler functions called in
order, not a longer body. If the name needs "and", split it.
