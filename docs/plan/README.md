# Implementation plan

Composer-sized slices that take the current Go binary to the target in
[SPEC.md](../SPEC.md). Product intent is [PRD.md](../PRD.md). Tool and
ESI contracts are [TOOLS.md](../TOOLS.md) and [ESI.md](../ESI.md);
credentials are [AUTH.md](../AUTH.md) and the schema is [DB.md](../DB.md).
**How the code is written is [RULES.md](../RULES.md), and it is not
discussed.**

This folder is the **status board**. Each task file has its own `Status`
line; keep that line and the table below in sync.

T01–T10 are done and are history. T11–T30 are the remaining scope,
re-sliced by the RULES audit of 2026-08-31 (see "What the audit
changed"). Status values: `todo` · `done` · `later`.

## How to pick work

1. Take the first row whose status is `todo` and whose dependencies are
   `done`.
2. Open that task file. It is the whole prompt for one Composer session.
3. Do **not** start the next task in the same session.
4. When acceptance is met, set `Status: done` in the task file **and**
   in the table here.

## Rules in force, every task

[RULES.md](../RULES.md) is normative. A task file, a PR or a review note
that disagrees with it is wrong. Each task names the rules that bite in
it; these bite in all of them:

- **§1** No clock is a dependency. No `Clock`, `now func() time.Time`,
  `WithClock`, `timeNow`. A function that needs "now" calls `time.Now()`;
  an instant that is part of the business question is an argument.
  Time-dependent and concurrent behaviour is tested with
  `testing/synctest` — and a test that talks to Postgres or a socket
  never goes inside a bubble.
- **§2** Postgres constraints are not control flow. No branching on
  `pgconn.PgError`, `pgerrcode.*` or a SQLSTATE. The statement wins the
  race (`ON CONFLICT`, a predicated `WHERE`, `FOR UPDATE`, an advisory
  lock); `RowsAffected() == 0` is a result.
- **§3** `make lint` is the contract. `make lint-fix` first, read the
  diff. `//nolint` only where the linter is wrong at that exact site,
  named and explained on the line. Never disable a linter to silence a
  call site.
- **§4/§5** A red test is a diagnosis, never a deletion. Every piece of
  business behaviour lands with tests for the cases it claims: happy
  path, refuse, expiry, cap, ownership, one-time consume.
- **§6** The code is the documentation. Comments are rare and say *why
  this rule is this shape*, never what the next line does.
- **§7** Only `gopkg/log` writes to std. The logger is a dependency:
  `main` builds it, `Close`s it, passes `log.Logger` in.
- **§8** URLs are assembled from `url.URL` / `url.Values` /
  `url.JoinPath`, never `+` or `fmt.Sprintf`. The host is injected once
  and lives on the client; endpoints are constants.
- **§9** The transport is the only place an error becomes a response,
  and what crosses is a static catalog entry. Inner layers return real
  errors so the log can say what broke.
- **§10** A function returns one value, or two when the second is
  `error` or `bool`. Two business values are a struct nobody has named.
- **§11** Domain and adapter are an entity plus its contract, with the
  implementation nested (`domain/x` + `domain/x/pgx`, `adapter/esi` +
  `adapter/esi/http`). Postgres is not an adapter and not an entity.
- **§12** Every query is a declared `const`. A string literal at the
  call site is an undeclared query.
- **§13** Test doubles are `mockgen` output (`go.uber.org/mock`), kept
  in one package. A hand-written `Silent`, `memFoo` or stub is deleted
  and generated. A real Postgres, `httptest` or `synctest` is not a mock.
- **§14** The application does not migrate. `sql/` at the root, no
  `Down`, applied by goose from CI/CD or by hand.
- **§15** One function, one job. If the name needs "and", split it.
- **§16** Config is env, in `package main`, and only where the value
  changes between environments. Everything else is a constant in the
  package that owns it.

Also every task: English only in code, comments, docs and commit
messages; import direction `service → usecase → adapter|domain`; no
typed MCP output schemas; all ESI traffic on `adapter/esi.Client`; no
migration carries the `users`-era database forward (it is dropped once,
by hand); do not commit `.env`; a behaviour change lands in the same
commit as the change to the document that owns it; after the task
`go build ./cmd/eve-mcp`, `go test ./...` and `make lint` pass.

## Board

| ID | Title | Status | Size | Depends | RULES | SPEC |
|---|---|---|---|---|---|---|
| [T01](T01-evals-go.md) | Rewrite evals in Go | done | M | — | — | §4 |
| [T02](T02-python-out-local-postgres.md) | Remove Python; local Postgres | done | M | T01 | — | §10 |
| [T03](T03-adapter-store.md) | `adapter/store` (pgx + migrations) | done | L | T02 | — | §8 |
| [T04](T04-cache-postgres.md) | ESI cache + names on Postgres | done | M | T03 | — | §5.1, §8 |
| [T05](T05-users-characters.md) | Users, characters, refresh `FOR UPDATE` | done | L | T03 | — | §3.2, §8 |
| [T06](T06-oauth-postgres.md) | OAuth handshake + JWT key in Postgres | done | L | T05 | — | §3.1, §8 |
| [T07](T07-guard-drop-datadir.md) | Confirm/mail in Postgres; drop `DATA_DIR` | done | M | T04 T06 | — | §4.1, §8 |
| [T08](T08-write-policy-constants.md) | Always-on writes; drop budget + audit | done | M | T07 | — | §2, §4 |
| [T09](T09-user-esi-bucket.md) | Per-user ESI token bucket | done | M | T07 | — | §5.2 |
| [T10](T10-character-ownership.md) | Alt-add ownership refuse | done | S | T05 T06 | — | §3.3 |
| [T11](T11-test-foundation.md) | Generated mocks and recorded ESI | done | L | — | §13 §5 §1 | §12.0 |
| [T12](T12-hmac-key.md) | `HMAC_KEY` env; drop `app_secrets` | done | S | T11 | §14 §16 | §2, §12.4 |
| [T13](T13-tests-and-ci.md) | `tests/` end to end, and a pipeline | done | M | T12 | §5 §3 §14 | §4.3, §6, §9 |
| [T14](T14-character-is-the-user.md) | The character is the user; drop `users` | done | L | T13 | §11 §5 | §3.3, §12.1 |
| [T15](T15-postgres-is-not-an-adapter.md) | Retire `adapter/store`; entities and contracts only | done | M | T14 | §11 §12 §16 | §7 |
| [T16](T16-assembled-urls.md) | URLs are assembled, never concatenated | done | S | T15 | §8 §16 | §3.1, §9 |
| [T17](T17-sessions-own-the-grant.md) | Sessions own the grant; runtime by `sid` | done | L | T16 | §2 §11 §12 | §3.1–3.4, §12.2–3 |
| [T18](T18-scope-checks.md) | Both scope checks | todo | M | T17 | §5 §9 | §3.2, §3.5, §12.6 |
| [T19](T19-audit-log.md) | `mutations` audit log; mail cap counts from it | todo | M | T17 | §2 §11 §12 | §4.1, §5.4, §8, §12.7 |
| [T20](T20-error-budget.md) | Per-character ESI error budget | todo | M | T17 | §1 §15 | §5.3, §12.8 |
| [T21](T21-sweeps.md) | Sweeps: expiry, client purge, abandoned grants | todo | M | T17 T19 | §1 §2 §12 | §12.9, DB.md |
| [T22](T22-config-and-edges.md) | Config and edges; `/readyz`; env names | todo | M | T17 | §16 §8 | §2, §5.5, §6, §10, §12.10 |
| [T23](T23-static-errors.md) | The user sees only static errors | todo | M | T18 T22 | §9 | §4, §6 |
| [T24](T24-new-tools-and-previews.md) | Calendar, compose, CSPA, enums, NPC corp | todo | L | T14 T19 | §15 §5 | §4, §4.1, §4.2, §12.11 |
| [T25](T25-catalog-check.md) | The catalogue check: TOOLS.md and ESI.md as tests | todo | M | T24 | §5 §10 | §4.3, §12.13 |
| [T26](T26-catalog-conformance.md) | Catalog conformance and server instructions | todo | M | T25 | §5 §6 | §4, §4.3, §12.11 |
| [T27](T27-pagination.md) | Pagination across the list tools | todo | L | T26 | §10 §15 | §4, §12.12 |
| [T28](T28-one-function-one-job.md) | One function, one job; one result | todo | L | T27 | §15 §10 §3 | §7 |
| [T29](T29-env-and-openapi.md) | `.env.example` + `api/http.yaml` | todo | S | T22 | §16 | §2, §6, §12.14 |
| [T30](T30-metrics.md) | Prometheus `/metrics` | later | M | T28 | §7 §15 | §11, §12.15 |

## What the audit of 2026-08-31 changed

The previous board sliced [SPEC.md](../SPEC.md) §12 and nothing else.
[RULES.md](../RULES.md) now owns how the code is written, so the board
was re-cut against both. Four things came out of it.

**Five tasks answer to RULES.md and to no §12 item at all.** T13 (a
pipeline, because §5's "tests are the only proof" is not true of a suite
nobody runs), T15 (the database is not an adapter, and a package holds
either an entity or a contract, §11), T16 (assembled URLs, §8), T23
(static errors at the edge, §9) and T28 (one function, one job, §15 +
§10) close debt the product spec cannot see because it is not about
behaviour. Two more §11 findings ride with the tasks that already move
that code: the SSO adapter stops carrying a repository (T17), and the
ESI resolver moves out of the contract package (T15).

**Two tasks were half-landed and were re-cut to what is left.** The old
T11 asked for a test database *and* ESI fixtures; the database half is
in the tree (`storetest`, goose apply, `migrate_test.go`), the fixtures
are not. The old T12 asked for goose *and* `HMAC_KEY`; goose is in the
tree, the key is still in `app_secrets`. The new T11 is the fixture half
plus the mock infrastructure RULES §13 requires and the old plan never
mentioned; the new T12 is the key, and it is an S.

**Test doubles became a task instead of a footnote.** RULES §13 names
`internal/logtest` as the anti-pattern by path, and `domain/write` has a
hand-written `memPersist` next to it. Nothing can be tested honestly
until `mockgen` exists, so it is the first thing T11 does.

**Every task now names the rules that bite in it.** The `Do not` blocks
carry the specific trap: no clock seam in the error-budget window, no
`23505` branch in the sign-in exchange, no god-object repository in the
session work, no `err.Error()` on a human page.

**The catalogue check moved in front of the work it checks.** The old
board built it last, after a human had already reconciled 52 tools by
hand. It now runs first: T25 writes the check and publishes its
findings, T26 empties the non-pagination half of that list, T27 empties
the rest. A conformance pass with a machine oracle is a list that
reaches zero; without one it is a claim nobody can audit.

**`evals/` is deleted; Go already has a test runner.** It was 508 lines
of `package main` with its own JSON-RPC client, flag set, bearer
plumbing and exit codes — a worse `go test`. Its `tasks.yaml` was
fourteen hand-graded agentic tasks with no runner at all, in step with
nothing; it had found a real bug once (ESI search is prefix, not fuzzy)
but a checklist nobody executes is not coverage, and RULES §5 does not
count it as proof. That gap — the model choosing the wrong tool, or
reading a stale number as live — is now uncovered, and saying so is
more useful than a file nobody opens.

Everything else it did survives as ordinary tests in `tests/`, which T13
creates: the tool-definition rules, the read-tool smoke and its
6 000-character budget, and later the catalogue check (T25). Fixture
recording becomes a `-update` flag, the standard Go golden-file shape.

Old → new mapping, from the pre-audit board: T14→T14, T15→T17, T16→T18,
T17→T19, T18→T20, T19→T21, T20→T22, T21→T24, T22→T26, T23→T27, T24→T25,
T25→T29, T26→T30. Old T13 (in-memory caches) is done and is recorded
below.

## Done and holding

PostgreSQL as the durable store (T03–T07). Writes and corp tools always
registered, confirm 300 s and mail cap 5/h as constants, no write budget
(T08). Per-character ESI token bucket 400 / 2 per s with
`UserRateLimited` (T09). The alt-add ownership refuse (T10) is
superseded by T14 and goes away with it.

Landed since, ahead of the board: the ESI response cache, id→name cache
and reference prices are bounded pod memory and `http_cache`, `names`
and `blobs` are dropped (old T13, SPEC §12.5); goose applies `sql/` from
`make migrate` and `Store.Open` runs no SQL (RULES §14); the domain
repositories for `characters`, `oauth_clients`, `login_states`,
`auth_codes` and `confirm_tokens` moved to `domain/*/pgx` and the ESI
and SSO clients to `adapter/*/http` (RULES §11, most of the way).

## Waves

- **3 — T11 T12 T13.** Ground to stand on: a test can be made of a
  generated mock and a recorded response, the signing key is config,
  and something runs all of it on every push.
- **4 — T14.** The identity rewrite. On its own, with nothing else in
  flight: it is where a mistake looks like a connection reading the
  wrong character.
- **5 — T15 then T16.** Structure, once `users` is gone: the database
  stops being an adapter, a package holds either an entity or a
  contract, and every URL is assembled. Both are mechanical, both remove
  friction from the wave that follows, and both edit `adapter/esi` — so
  they go in order, not in parallel.
- **6 — T17.** Sessions. The table, the `sid` claim, the exchange
  transaction and the runtime keying are one invariant; landing a subset
  gives a build that authenticates but cannot say which grant it holds.
- **7 — T18–T23.** The invariants and edges that sit on sessions.
  Independent of each other once T17 lands, except T23, which wants
  T18's page and T22's listener work to already exist.
- **8 — T24 T25 T26 T27.** The tool surface: the missing tools and
  previews, then the linter that reads TOOLS.md, then conformance and
  pagination working its findings to zero.
- **9 — T28.** Shape, once the surface stops moving: split what grew and
  turn the complexity linters on, so §15 stops being a review opinion.
- **10 — T29.** Generated artefacts last, when the env set has stopped
  moving. T30 is explicitly later.

## Dependency shape

```
T11 ─ T12 ─ T13 ─ T14 ─ T15 ─ T16 ─ T17 ─┬─ T18 ······┐
                                         │            ├─ T23
                                         ├─ T22 ······┘
                                         │   └─ T29
                                         ├─ T19 ─┬─ T21
                                         │       └─ T24 ─ T25 ─ T26 ─ T27 ─ T28 ─ T30
                                         └─ T20
```

Everything up to T17 is a chain, deliberately. T15 and T16 both edit
`adapter/esi`, so running them side by side buys a merge conflict and
nothing else.

T23 is the only task with two parents that are not on one path: it needs
T18's refusal page (the one it must not bypass) and T22's listener and
IP rules (what it renders errors on). Every other second dependency is
already an ancestor — T24 needs T14 through T19, T21 needs T17 through
T19.

## §12 coverage

Every §12 item maps to exactly one task except **§12.4, the schema**,
which is deliberately spread: T12 takes `HMAC_KEY` out of the database,
T14 drops `users`, T17 adds `sessions`, T19 adds `mutations`. A schema
change rides with the code that needs it, so §12.4 is finished when T19
is. §12.5 is done. The done rows cite stable SPEC sections only.

## Current vs target (snapshot, 2026-08-31)

The running server is Go: `cmd/eve-mcp`,
`internal/{adapter,domain,usecase,service}`, MCP tools in
`internal/usecase/eve`. That structure is **not** being rewritten; T15
finishes moving it to the shape RULES §11 describes.

| Now | Target | Task |
|---|---|---|
| Hand-written `logtest.Silent` and `memPersist`; ESI tested against inline `httptest` handlers | generated mocks in one package, recorded ESI at the pinned date | T11 |
| JWT key in `app_secrets` | `HMAC_KEY` env, min 32 bytes | T12 |
| No CI of any kind, though three documents say "CI/CD"; nothing tests the binary end to end | a pipeline on every push, and `tests/` that signs in and calls every read tool | T13 |
| `users` table; a connection can hold several characters | the character **is** the identity, one per connection | T14 |
| `adapter/store` holds a pool, a test helper and `mail_log`; `domain/` also holds JSON helpers and two constants; `adapter/esi` holds a 488-line resolver | `internal/postgres` for the pool, a domain per table, `domain/` is entities and `adapter/esi` is a contract | T15 |
| `adapter/sso` carries a `character.Repository` and half a repository interface | the SSO client speaks only to CCP; `usecase/session` owns the grant | T17 |
| Base URLs parsed and joined per call | one `url.URL` on the client, endpoints as constants | T16 |
| EVE grant on the character row | grant on the session row, one live session per character | T17 |
| A short scope grant produces a working-looking session | refused at the callback, named | T18 |
| `mail_log`; nothing records other mutations | `mutations` audit log, cap counts from it | T19 |
| Only a request bucket | plus a per-character error budget | T20 |
| Nothing sweeps | six rules under a `try` advisory lock | T21 |
| `PUBLIC_URL` optional, no per-IP limit, one probe, `LISTEN` names an address | validated config, limit on every anonymous route, `/readyz`, host and port named | T22 |
| `err.Error()` and CCP's `error_description` rendered into a page | a static catalog at the edge, the real error in the log | T23 |
| Nothing checks TOOLS.md or ESI.md | a test diffs both, and the instructions | T25 |
| 50 tools, calendar unreachable, CSPA guessed, `window` falls through | 52 tools, priced CSPA, validated enums | T24 T26 |
| Nothing past the first `limit` is reachable | pagination mirroring ESI, per class | T27 |
| 1400-line files, nine-parameter helpers, complexity linters off | one function one job, enforced by `make lint` | T28 |

## Local loop

```bash
cp .env.example .env          # set CLIENT_ID, DATABASE_URL, HMAC_KEY
make postgres                 # docker compose up -d postgres
make migrate                  # goose applies sql/ — never the binary
make run                      # make postgres + go build && ./eve-mcp
curl -s http://127.0.0.1:8766/healthz
make test                     # offline: fixtures, no database needed
make test-store               # everything that needs DATABASE_URL
make lint
make ci                       # what the pipeline runs, locally
```

From T13 everything that is not a unit test lives in `tests/` as plain
`go test` packages: the tool-definition rules, the read-tool smoke, the
catalogue check. They need Postgres and the recorded fixtures, never a
live character, which is what lets CI gate a merge on them. The one
thing that does talk to CCP is `go test ./tests -run TestFixtures
-update`, and it is an operator step.
Boot-without-login check is `GET /healthz`, and after T22 readiness is
`GET /readyz`.

Do not run `docker compose down -v`: it deletes the `eve-mcp-pg` volume.
`make down` stops Postgres and keeps it.

From T11 onward, `go test ./...` runs offline against recorded ESI
fixtures; store and migration tests need `DATABASE_URL` and skip with a
message without it.
