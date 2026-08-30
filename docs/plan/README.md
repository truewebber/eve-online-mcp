# Implementation plan

> **Superseded — being re-sliced.** This board was written against an
> earlier SPEC. The current remaining work is [SPEC.md](../SPEC.md) §12,
> which supersedes T11 and T12 and adds items this board never had
> (sessions, audit log, re-authentication, per-character error budget).
> T01–T10 are done and stay done. Do not pick work from the table below
> until it has been rebuilt from §12.

Composer-sized slices that take the current Go binary to the target in
[SPEC.md](../SPEC.md). Product intent is [PRD.md](../PRD.md). Tool and
ESI contracts are [TOOLS.md](../TOOLS.md) and [ESI.md](../ESI.md).

This folder is the **status board**. Each task file has its own `Status`
line; keep that line and the table below in sync.

## How to pick work

1. Take the first row whose status is `todo`.
2. Open that task file. It is the whole prompt for one Composer session.
3. Do **not** start the next task in the same session.
4. When acceptance is met, set `Status: done` in the task file **and**
   in the table here.

Status values: `todo` · `done` · `later`.

## Composer rules (every task)

- Code, comments, docs, commit messages: **English only**.
- Follow [SPEC.md](../SPEC.md) and this task file. `docs/` is normative;
  `README.md` describes the repo, not the contract.
- Import direction stays `service → usecase → adapter|domain`.
- Process config lives only in `cmd/eve-mcp/config.go` (`package main`).
- Do not add typed MCP output schemas.
- All ESI traffic stays on `adapter/esi.Client`.
- Do not invent migrations from the old `DATA_DIR` layout — players
  re-authenticate once (SPEC §8).
- Do not recreate a git remote. Do not commit `.env`.
- After the task: `go build ./cmd/eve-mcp` and `go test ./...` pass.

## Board

| ID | Title | Status | Size | Depends | SPEC |
|---|---|---|---|---|---|
| [T01](T01-evals-go.md) | Rewrite evals in Go | done | M | — | §4 |
| [T02](T02-python-out-local-postgres.md) | Remove Python; local Postgres | done | M | T01 | §10 |
| [T03](T03-adapter-store.md) | `adapter/store` (pgx + migrations) | done | L | T02 | §8, §12.0 |
| [T04](T04-cache-postgres.md) | ESI cache + names on Postgres | done | M | T03 | §5.1, §8 |
| [T05](T05-users-characters.md) | Users, characters, refresh `FOR UPDATE` | done | L | T03 | §3.3, §8 |
| [T06](T06-oauth-postgres.md) | OAuth handshake + JWT key in Postgres | done | L | T05 | §3.1, §8 |
| [T07](T07-guard-drop-datadir.md) | Confirm/mail in Postgres; drop `DATA_DIR` | done | M | T04 T06 | §4.1, §8, §12.0 |
| [T08](T08-write-policy-constants.md) | Always-on writes; drop budget + audit | done | M | T07 | §2, §12.1–2 |
| [T09](T09-user-esi-bucket.md) | Per-user ESI token bucket | done | M | T07 | §5.3, §12.3 |
| [T10](T10-character-ownership.md) | Alt-add ownership refuse | done | S | T05 T06 | §3.3, §12.3a |
| [T11](T11-docs-catchup.md) | README, CLAUDE, env, OpenAPI, TOOLS | todo | M | T08 T09 T10 | §12.4 |
| [T12](T12-metrics.md) | Prometheus `/metrics` | later | M | T11 | §11, §12.5 |

## Current vs target (snapshot, 2026-08-30)

The running server is already Go: `cmd/eve-mcp`, `internal/{adapter,domain,usecase,service}`. MCP tools live in `internal/usecase/eve`. That part is **not** being rewritten.

Still true today, and the plan removes it:

| Now | Target |
|---|---|
| Durable state in PostgreSQL (`DATABASE_URL`) | — (done T03–T07) |
| Write/corp tools always registered; confirm + mail cap 5/h | — (done T08) |
| Per-user ESI token bucket 400 / 2 rps, `UserRateLimited` | — (done T09) |
| Unique `character_id`; alt-add of another user's character is refused | — (done T10) |

## Local loop (the end state of T02 + T07)

```bash
cp .env.example .env          # set CLIENT_ID and DATABASE_URL
make postgres                 # docker compose up -d postgres
make run                      # make postgres + go build && ./eve-mcp
curl -s http://127.0.0.1:8766/healthz
make lint                     # go run ./evals lint   (needs a bearer token)
```

Until T07 landed, the binary still used a data directory; `make postgres` was for store tests (T03+) and for the cutover. After T07, `DATABASE_URL` is required and durable state is Postgres only.

Smoke (`make smoke`) needs an authorized character. Lint needs a bearer token because `/mcp` is an OAuth resource. Boot-without-login check is `GET /healthz`.

## Waves

- **0 — T01 T02.** Python gone. Postgres in Compose. Eval harness in Go. You can build and boot the current file-backed server with no Python toolchain.
- **1 — T03–T07.** Postgres becomes the only store. After T07 the full local loop matches SPEC §10.
- **2 — T08–T10.** Product policy in SPEC §12.1–3a.
- **3 — T11.** Docs match the code. T12 is explicitly later.
