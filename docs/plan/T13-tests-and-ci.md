# T13 — `tests/` for everything that is not a unit test, and a pipeline

- Status: `todo`
- Size: M
- Depends on: T12
- RULES: §5 (tests are the only proof), §3 (the linter is the style),
  §13 (mocks are generated), §14 (the application does not migrate)
- SPEC: §4.3, §6, §9 — this task edits §7 and §12.13
- New in the 2026-08-31 audit; no §12 item

## Goal

Two holes and a rescue.

**`evals/` is already gone.** It was 508 lines of `package main` with its
own JSON-RPC client, flag parsing, bearer plumbing and exit codes —
reimplementing `go test` badly. The audit deleted it, along with every
reference in SPEC, TOOLS.md, ESI.md, the Makefile and `README.md`, so
nothing in the tree claims it exists. **What it checked has not been
reimplemented anywhere, and that is this task's first job:** recover
those checks from git history and land them as ordinary Go tests.

**Nothing runs anything automatically.** There is no `.github`, no
pipeline of any kind — while RULES §14, DB.md and SPEC §12.4 all say
"CI/CD" as though it exists. `make lint` and `go test ./...` are
green because somebody remembers to type them. RULES §5 says a test is
the only proof the code works; a suite nobody runs is not proof either.

**Nothing tests the binary end to end.** The old `smoke` called every
read tool, but against live Tranquility with a real authorized character
— which no pipeline can do.

Target: `tests/` at the repository root holds everything that is not a
unit test — integration and smoke — as normal `go test` packages,
running against a real Postgres and T11's recorded ESI, gated in CI.

## Why this is one Composer session

Deleting the runner and having somewhere for its checks to live is one
move; splitting it leaves a window with no coverage at all. The pipeline
is forty lines nobody would give a session to on its own, and it is the
whole reason the other two are worth doing.

It lands before the identity and session rewrites, because those two are
where a mistake looks like a connection that signs itself out — and the
test that catches that is exactly "sign in, call a tool", which does not
exist yet.

## Do not

- Restore any part of `evals/`. Not the binary, not the flag set, not
  the `--token` handling, not `make smoke` / `make eval`. A Go test gets
  its fixtures from `testdata` and its wiring from the harness.
- Reinvent the checks from the description below when the code is one
  `git show` away. Reinvented thresholds are how 6 000 quietly becomes
  8 000.
- Write a `TestMain` that shells out to `go run`. `tests/` builds the
  server in-process.
- Call real ESI or real CCP from `tests/` or from CI. Fixtures and a
  local Postgres, or it is not a pipeline step.
- Add a CI step that needs a credential nobody has. The pipeline runs on
  a clone with no secrets; anything that cannot is not in it.
- Mint a bearer by driving a browser. Sign one with the test
  `HMAC_KEY` against a seeded row, so the test enters through the same
  `ProtectMCP` path a real client does. That is why T12 comes first: it
  moves the signing key out of `app_secrets` into config, so the
  harness sets an env var instead of reaching into a table.
- Put `tests/` under `internal/`, or import it from anything. It is
  leaf.
- Let CI apply migrations to anything but its own throwaway database
  (RULES §14 — a pipeline that can reach a real one is a worse problem
  than a missing pipeline).
- Rewrite the checks while moving them. The read-tool table, the default
  arguments, the 6 000-character budget and the tool-definition rules
  exist; they change shape, not content.
- Hand-write a double to stand in for ESI. The fixture transport from
  T11 is the seam (RULES §13).

## Context

**The deleted code is the specification.** `git show
HEAD:evals/main.go` in the audit commit's parent has all of it: the
JSON-RPC client, the read-tool table with its default arguments, the
`noResponseFormatNeeded` exception list, `minDescriptionChars = 120`
and `maxDefaultResponseChars = 6000`. Read it before writing anything;
every constant and every exception in it was put there for a reason
somebody has already argued about.

What `evals` did, and where each half goes:

| Was | Becomes |
|---|---|
| `lint` — every tool namespaced `eve_`, every parameter described in JSON Schema, description ≥ 120 chars, no leaked indentation, bounds declared, list tools expose `response_format` | a table-driven test over `tools/list` in `tests/` |
| `smoke` — call every read tool with defaults, valid JSON, no `error`, default response ≤ 6 000 characters | an integration test in `tests/`, against fixtures instead of Tranquility |
| `record` (T11) — write fixtures from real ESI | a `-update` flag on the fixture test, the standard Go golden-file shape |

The 6 000-character rule is the only place PRD's "answers fit a
conversation" is enforced anywhere. It survives the move or the move is
a regression.

`tests/` cannot import `cmd/eve-mcp` (it is `package main`), so it wires
the server from the same `Options` structs that `main` fills — roughly
forty lines, explicit and readable. Do not invent an `internal/app` for
it here; if T28 extracts `main.start` into named composition steps, this
wiring can shrink then.

The bearer helper will change twice by design: T14 makes `sub` a
character id, T17 adds `sid` and a `sessions` row to seed. Each of those
tasks updates it, which is the point — an end-to-end test that has to be
updated is an end-to-end test that noticed.

**The compatibility-date re-verification survives the deletion.** SPEC §9
requires every parsed response shape to be re-checked when the pinned
date moves. That is `go test ./tests -run TestFixtures -update` on the
new date plus a review of the `testdata` diff — better than a pass/fail
run, because it names what CCP changed. Write it into §9, which today
only states the requirement and not the mechanism.

The catalogue check — TOOLS.md and ESI.md diffed against the running
surface — is T25, in this same package. It is not part of this task; it
needs the tools T24 adds.

## Work

1. Read `evals/main.go` out of git history. It is the source for
   steps 3 and 4; nothing about those two is a fresh design.
2. `tests/` at the repository root: a harness that takes a throwaway
   database from `internal/adapter/store/storetest`, applies the
   migrations the way CI does, wires the server with T11's fixture
   transport, and hands back an addressable server and a bearer signed
   with the `HMAC_KEY` T12 just made config. T15 renames that helper to
   `internal/postgres/pgtest` and updates the import.
3. Port the smoke checks: every read tool returns valid JSON with no
   `error`; no default response exceeds 6 000 characters.
4. Port the tool-definition checks as a table-driven test over
   `tools/list`.
5. Protocol cases: `initialize` and `tools/list` answer; `/mcp` without
   a bearer is `401` with the `WWW-Authenticate` header SPEC §3.1
   specifies.
6. `.github/workflows/ci.yml` on push and pull request: `make lint`,
   `go test ./...`, then a Postgres service with `make migrate`,
   `make test-store` and `go test ./tests/...`. Any non-zero exit fails
   the build. `make ci` runs the same sequence locally.
7. SPEC §7 already names `tests/` in the layout, and §4, §4.3, §12.13,
   TOOLS.md, ESI.md and `README.md` already point at "the catalogue
   check in `tests/`". Confirm the package they describe is the package
   that now exists, and fix whichever side is wrong.

## Files

- Add: `tests/*`, `.github/workflows/ci.yml`
- Edit: `Makefile` (a `ci` target), `docs/SPEC.md` §9 (the `-update`
  re-verification)

## Acceptance

- [ ] CI runs on push and pull request, and a failing lint, unit test,
      store test or `tests/` test fails the build
- [ ] No CI step needs a credential, a live character, or the network
      beyond module download and the Postgres service
- [ ] `tests/` is at the repository root, is `go test`-native, and
      nothing imports it
- [ ] Every read tool is exercised end to end through a bearer, and a
      default response over 6 000 characters fails the build
- [ ] Every tool-definition rule the old `lint` enforced still fails on
      violation, covered by a test per rule
- [ ] `-update` re-records fixtures against real ESI, and is the only
      thing in the tree that talks to CCP
- [ ] `make ci` reproduces the pipeline locally
- [ ] `go test ./...` and `make lint` pass

## Verify

```bash
make ci
ls tests .github/workflows
rg -n 'evals|EVE_MCP_TOKEN' . --glob '!.git' --glob '!docs/plan/T0*'
```

## Done

Set `Status: done` here and in [README.md](README.md).
