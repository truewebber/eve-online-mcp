# T02 — Remove Python; local Postgres

- Status: `todo`
- Size: M
- Depends on: T01
- SPEC: §10 (local dev = reachable Postgres + `./eve-mcp`)

## Goal

No Python in the repo. Local setup is Compose Postgres + the Go binary
on the host. A developer can `make postgres` and `make run` without a
venv.

## Why this is one Composer session

Delete the retired tree, replace the Python container files, wire
Makefile. Do not migrate durable state yet — the binary still uses
`DATA_DIR` until T07. Compose exists so T03 tests have a database.

## Do not

- Wire `DATABASE_URL` into `cmd/eve-mcp` (T07).
- Change MCP tools, OAuth, or ESI.
- Name the Compose volume `eve-mcp-data` (that was the Python token
  volume). Use a new name, e.g. `eve-mcp-pg`.
- Put the Go app in Compose by default (OAuth callback and `CLIENT_ID`
  are easier on the host). A Go `Dockerfile` is fine for later k8s;
  `docker compose up` should start **Postgres only**.

## Context

Delete all of this (it is the old Python server):

- `eve_mcp/`
- `pyproject.toml`
- `requirements.txt`
- current `Dockerfile` (`FROM python:3.12-slim`, `python -m eve_mcp`)
- current `docker-compose.yml` (Python app + `eve-mcp-data`)
- `evals/run.py` and `evals/__pycache__/`

Keep `evals/tasks.yaml` and the Go evals from T01.

## Work

1. Delete the Python tree and packaging listed above.
2. Write `docker-compose.yml` with one service: `postgres:16` (or 16-alpine).
   - User/password/db: `eve` / `eve` / `eve_mcp`
   - Bind `127.0.0.1:5432:5432` (loopback only)
   - Healthcheck: `pg_isready`
   - Named volume for PGDATA (not `eve-mcp-data`)
3. Optional: a multi-stage **Go** `Dockerfile` that builds `./cmd/eve-mcp`
   (match `go` version in `go.mod`). Not used by default Compose.
4. Makefile targets:
   - `postgres` — `docker compose up -d postgres` and wait until healthy
   - `down` — `docker compose down` (**without** `-v`)
   - `run` — `postgres` is **not** required until T07; keep `run` as
     build + `./eve-mcp` so today’s file-backed server still boots
   - `lint` / `smoke` / `eval` from T01
5. `.gitignore`: drop Python noise that is now pointless (`*.egg-info`,
   `venv/`) or leave harmlessly; do **not** gitignore `docs/plan/`.
6. `.env.example`: add a **commented** `DATABASE_URL` pointing at the
   Compose DSN (`postgres://eve:eve@127.0.0.1:5432/eve_mcp?sslmode=disable`)
   with a one-line note that the binary starts requiring it in T07.
7. Strip leftover Python mentions from `README.md` / `CLAUDE.md` that
   would tell someone to `pip install` or `python3 evals/run.py`. Full
   doc rewrite is T11; this task only removes instructions that are now
   false.

## Files

- Delete: `eve_mcp/**`, `pyproject.toml`, `requirements.txt`, `evals/run.py`
- Replace: `Dockerfile`, `docker-compose.yml`
- Edit: `Makefile`, `.env.example`, `.gitignore`, `README.md`, `CLAUDE.md`

## Acceptance

- [ ] `find . -name '*.py'` is empty (ignore `.git`)
- [ ] `go.mod` / `go.sum` unchanged unless the Go Dockerfile needs nothing
      from them at runtime
- [ ] `docker compose config` validates; `make postgres` brings up a
      reachable `5432` on loopback
- [ ] `make run` still starts the **current** file-backed server when
      `CLIENT_ID` is set (no `DATABASE_URL` required yet)
- [ ] `make down` does not use `-v`
- [ ] No `python3` in Makefile

## Verify

```bash
rg -l --glob '!.git/*' 'python' Makefile Dockerfile docker-compose.yml README.md CLAUDE.md || true
make postgres
pg_isready -h 127.0.0.1 -p 5432 -U eve || docker compose exec postgres pg_isready -U eve
go build -o eve-mcp ./cmd/eve-mcp
```

## Done

Set `Status: done` here and in [README.md](README.md).
