# T11 — Docs catch-up

- Status: `todo`
- Size: M
- Depends on: T08, T09, T10
- SPEC: §12.4

## Goal

Human docs match the server that actually runs: env set, local loop,
no Python, no host write-policy, no `DATA_DIR`. A new laptop can
follow README and get `/healthz` without reading the plan folder.

## Why this is one Composer session

Copy only. Do not change behaviour. If you find a leftover env or
Python path, fix the doc or file a gap — do not sneak feature work.

## Do not

- Rephrase PRD/SPEC product intent.
- Leave TOOLS.md mentioning operator capabilities, write budget, or
  `audit_log` (should already be gone in T08 — verify).
- Translate plan files; they stay English.

## Work

Align these with SPEC §2, §6, §10 and the code:

| File | Fix |
|---|---|
| `README.md` | Quick start: Compose Postgres, `.env` with `CLIENT_ID` + `DATABASE_URL` + `CONTACT`, `make run`, client URL. Delete WRITE_* / CORP_SCOPES / DATA_DIR tables. Writes = always confirm + mail cap. Corp tools always present, gated by in-game roles. |
| `CLAUDE.md` | Running: `make postgres && make run`; `go run ./evals lint`. Invariants: no capability gate, no audit log, Guard is confirm + mail only. Layout: `adapter/store`, no `eve_mcp/`. Drop “do not docker compose down -v” Python-volume warning; replace with “do not `down -v` if you want to keep the Postgres volume”. |
| `.env.example` | Only live env vars; `DATABASE_URL` required, commented DSN for Compose. |
| `api/http.yaml` | Info description: no host policy story. `make gen` if YAML changes. |
| `docs/TOOLS.md` | Remove the “Pending SPEC §12” banner if T08 is done; `eve_auth_status` Returns line honest. |
| `docs/ESI.md` | Paths still true (`adapter/esi`, Postgres cache). |
| `evals/README.md` | Go commands only. |
| `docs/SPEC.md` §12 | Mark items 0–4 done in a short note, or replace the checklist with “implemented; see git”. Keep §12.5 (metrics) as open. |

README must include:

```bash
cp .env.example .env   # CLIENT_ID, DATABASE_URL, CONTACT
make postgres
make run
curl -s http://127.0.0.1:8766/healthz
```

and the Cursor `mcp.json` URL snippet (unchanged idea).

## Acceptance

- [ ] `rg -n 'python3|WRITE_ALLOW|DATA_DIR|eve_mcp/' README.md CLAUDE.md .env.example` is empty or only historical SPEC/plan
- [ ] README does not mention pip, venv, or `CORP_SCOPES`
- [ ] CLAUDE.md evals line is `go run ./evals`
- [ ] TOOLS.md has no pending-§12 banner
- [ ] `make gen` is clean if YAML changed

## Verify

Read README as a stranger: can you boot without the plan folder?
`go build ./cmd/eve-mcp` still succeeds (docs-only).

## Done

Set `Status: done` here and in [README.md](README.md).
