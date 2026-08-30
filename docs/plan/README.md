# Implementation plan

Composer-sized slices that take the current Go binary to the target in
[SPEC.md](../SPEC.md). Product intent is [PRD.md](../PRD.md). Tool and
ESI contracts are [TOOLS.md](../TOOLS.md) and [ESI.md](../ESI.md);
credentials are [AUTH.md](../AUTH.md) and the schema is [DB.md](../DB.md).

This folder is the **status board**. Each task file has its own `Status`
line; keep that line and the table below in sync.

T01–T10 are done and are the current state of the repo. T11–T26 are the
whole of the remaining scope: they are [SPEC.md](../SPEC.md) §12 items
0–15, re-sliced after the documentation audit of 2026-08-30. The two
tasks this board carried before that audit (docs catch-up, metrics) were
deleted rather than updated — their scope reappears in T20, T25 and T26.

## How to pick work

1. Take the first row whose status is `todo` and whose dependencies are
   `done`.
2. Open that task file. It is the whole prompt for one Composer session.
3. Do **not** start the next task in the same session.
4. When acceptance is met, set `Status: done` in the task file **and**
   in the table here.

Status values: `todo` · `done` · `later`.

## Composer rules (every task)

- Code, comments, docs, commit messages: **English only**.
- Follow [SPEC.md](../SPEC.md) and this task file. `docs/` is normative
  and the code follows it, never the other way round; `README.md`
  describes the repo, not the contract.
- A change to behaviour lands in the same commit as the change to the
  document that owns it.
- Import direction stays `service → usecase → adapter|domain`.
- Process config lives only in `cmd/eve-mcp/config.go` (`package main`).
- Do not add typed MCP output schemas.
- All ESI traffic stays on `adapter/esi.Client`.
- Do not write migrations that carry the `users`-era database forward.
  That database is dropped once, by hand; players re-authenticate
  (DB.md "Migrations").
- Do not commit `.env`.
- After the task: `go build ./cmd/eve-mcp` and `go test ./...` pass.

## Board

| ID | Title | Status | Size | Depends | SPEC |
|---|---|---|---|---|---|
| [T01](T01-evals-go.md) | Rewrite evals in Go | done | M | — | §4 |
| [T02](T02-python-out-local-postgres.md) | Remove Python; local Postgres | done | M | T01 | §10 |
| [T03](T03-adapter-store.md) | `adapter/store` (pgx + migrations) | done | L | T02 | §8 |
| [T04](T04-cache-postgres.md) | ESI cache + names on Postgres | done | M | T03 | §5.1, §8 |
| [T05](T05-users-characters.md) | Users, characters, refresh `FOR UPDATE` | done | L | T03 | §3.2, §8 |
| [T06](T06-oauth-postgres.md) | OAuth handshake + JWT key in Postgres | done | L | T05 | §3.1, §8 |
| [T07](T07-guard-drop-datadir.md) | Confirm/mail in Postgres; drop `DATA_DIR` | done | M | T04 T06 | §4.1, §8 |
| [T08](T08-write-policy-constants.md) | Always-on writes; drop budget + audit | done | M | T07 | §2, §4 |
| [T09](T09-user-esi-bucket.md) | Per-user ESI token bucket | done | M | T07 | §5.2 |
| [T10](T10-character-ownership.md) | Alt-add ownership refuse | done | S | T05 T06 | §3.3 |
| [T11](T11-test-foundation.md) | ESI fixtures and a test database | todo | M | — | §12.0 |
| [T12](T12-goose-and-hmac-key.md) | goose under an advisory lock; `HMAC_KEY` out of the DB | todo | M | T11 | §2, §12.4 |
| [T13](T13-in-memory-caches.md) | In-memory caches; drop the cache tables | todo | M | T12 | §5.1, §12.5 |
| [T14](T14-character-is-the-user.md) | The character is the user; drop `users` | todo | L | T12 | §3.3, §12.1 |
| [T15](T15-sessions-own-the-grant.md) | Sessions own the EVE grant; runtime by `sid` | todo | L | T14 | §3.1–3.4, §12.2–3 |
| [T16](T16-scope-checks.md) | Both scope checks | todo | M | T15 | §3.2, §3.5, §12.6 |
| [T17](T17-audit-log.md) | `mutations` audit log; mail cap counts from it | todo | M | T15 | §4.1, §5.4, §8, §12.7 |
| [T18](T18-error-budget.md) | Per-character ESI error budget | todo | M | T15 | §5.3, §12.8 |
| [T19](T19-sweeps.md) | Sweeps: expiry, client purge, abandoned grants | todo | M | T15 T17 | §12.9, DB.md |
| [T20](T20-config-and-edges.md) | Config and edges; `/readyz` | todo | M | T15 | §2, §5.5, §6, §10, §12.10 |
| [T21](T21-new-tools-and-previews.md) | Calendar, compose, CSPA, enums, NPC corp | todo | L | T14 T17 | §4, §4.1, §4.2, §12.11 |
| [T22](T22-catalog-conformance.md) | Catalog conformance and server instructions | todo | M | T21 | §4, §4.3, §12.11 |
| [T23](T23-pagination.md) | Pagination across the list tools | todo | L | T22 | §4, §12.12 |
| [T24](T24-evals-lint-catalog.md) | `evals lint` against the catalog | todo | M | T22 T23 | §4.3, §12.13 |
| [T25](T25-env-and-openapi.md) | `.env.example` + `api/http.yaml` | todo | S | T20 | §2, §6, §12.14 |
| [T26](T26-metrics.md) | Prometheus `/metrics` | later | M | T24 | §11, §12.15 |

Every §12 item maps to exactly one task except **§12.4, the schema**,
which is deliberately spread: T12 brings goose and the advisory lock,
T13 drops the cache tables, T14 drops `users`, T15 adds `sessions`, T17
adds `mutations`. A schema change rides with the code that needs it, so
§12.4 is only finished when T17 is.

The done rows cite stable SPEC sections only. Their old `§12.x` pointers
were dropped when §12 was renumbered by the audit — §12 is the remaining
work by definition, and what T03–T10 delivered is recorded in its "Done
and holding" paragraph.

## Waves

- **0 — T01 T02.** Python gone, Postgres in Compose, eval harness in Go.
- **1 — T03–T07.** Postgres becomes the only store.
- **2 — T08–T10.** Product policy: always-on writes, per-user bucket,
  ownership refuse.
- **3 — T11–T13.** Ground to stand on. Fixtures and a test database, the
  migrator swapped for goose, caches out of Postgres. Nothing about auth
  or the tool surface moves, and the binary keeps working throughout.
- **4 — T14 T15.** The identity rewrite, sequentially and with nothing
  else in flight. T14 makes a connection single-character; T15 gives the
  sign-in its own row and its own grant. This is the risky wave: it is
  where a mistake looks like a connection that signs itself out.
- **5 — T16–T20.** The invariants and edges that sit on top of sessions.
  Independent of each other once T15 lands, so they can go in any order
  or in parallel.
- **6 — T21–T24.** The tool surface: the missing tools and previews, then
  conformance, then pagination, then the lint that keeps all three from
  drifting again.
- **7 — T25.** Generated artefacts last, when the env set has stopped
  moving. T26 is explicitly later.

## Dependency shape

```
T11 ─ T12 ─┬─ T13
           └─ T14 ─ T15 ─┬─ T16
                         ├─ T17 ─┬─ T19
                         │       └─ T21 ─ T22 ─ T23 ─ T24 ─ T26
                         ├─ T18
                         └─ T20 ─ T25
```

T21 also needs T14 (no `character` parameter) and T17 (the audit row a
mutation writes).

## Current vs target (snapshot, 2026-08-30)

The running server is Go: `cmd/eve-mcp`,
`internal/{adapter,domain,usecase,service}`, MCP tools in
`internal/usecase/eve`. That structure is **not** being rewritten.

What is still true today and what the remaining tasks change:

| Now | Target | Task |
|---|---|---|
| Hand-rolled migrator, JWT key in `app_secrets` | goose under an advisory lock, `HMAC_KEY` env | T12 |
| ESI cache, names and prices in Postgres | bounded pod memory, ETag revalidation | T13 |
| `users` table; a connection can hold several characters | the character **is** the identity, one per connection | T14 |
| EVE grant on the character row | grant on the session row, one live session per character | T15 |
| A short scope grant produces a working-looking session | refused at the callback, named | T16 |
| `mail_log`; nothing records other mutations | `mutations` audit log, cap counts from it | T17 |
| Only a request bucket | plus a per-character error budget | T18 |
| Nothing sweeps | six rules under a `try` advisory lock | T19 |
| `PUBLIC_URL` optional, no per-IP limit, one probe | validated config, limit on every anonymous route, `/readyz` | T20 |
| 50 tools, calendar unreachable, CSPA guessed, `window` falls through | 52 tools, priced CSPA, validated enums | T21 T22 |
| Nothing past the first `limit` is reachable | pagination mirroring ESI, per class | T23 |
| Nothing checks TOOLS.md or ESI.md | `evals lint` diffs both, and the instructions | T24 |

## Local loop

```bash
cp .env.example .env          # set CLIENT_ID, DATABASE_URL, HMAC_KEY
make postgres                 # docker compose up -d postgres
make run                      # make postgres + go build && ./eve-mcp
curl -s http://127.0.0.1:8766/healthz
make lint                     # go run ./evals lint   (needs a bearer token)
```

Smoke (`make smoke`) needs an authorized character. Lint needs a bearer
token because `/mcp` is an OAuth resource. Boot-without-login check is
`GET /healthz`, and after T20 readiness is `GET /readyz`.

Do not run `docker compose down -v`: it deletes the `eve-mcp-pg` volume.
`make down` stops Postgres and keeps it.

From T11 onward, `go test ./...` runs offline against recorded ESI
fixtures; store and migration tests need `DATABASE_URL` and skip with a
message without it.
