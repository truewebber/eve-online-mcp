# T01 — Rewrite evals in Go

- Status: `todo`
- Size: M
- Depends on: —
- SPEC: §4 (tool contract the harness enforces)

## Goal

Replace `evals/run.py` with a Go program so lint/smoke need no Python.
Keep the same gates and the same CLI shape.

## Why this is one Composer session

`evals/run.py` is ~220 lines of stdlib HTTP. Port it. Do not change MCP
tools. Do not delete the Python server tree yet (T02).

## Do not

- Delete `eve_mcp/`, `pyproject.toml`, or `requirements.txt`.
- Call ESI or talk to Postgres.
- Add a new MCP transport or auth bypass so lint can skip the bearer
  token. `/mcp` stays protected (SPEC §3.1).
- Translate `evals/tasks.yaml` into code; it stays a manual agentic list.

## Context

- Current harness: `evals/run.py` (read it; it is the spec of the gates).
- Human notes (today in Russian): `evals/README.md` — rewrite in English.
- Makefile `lint` target currently runs Python.

Gates to preserve:

| Gate | What | Fail |
|---|---|---|
| `lint` | `tools/list`; `eve_` prefix; param `description`; description 120–2000 chars; no docstring indent; list tools have `response_format` unless in the exception set | missing description / bad name / short description |
| `smoke` | call every read tool with defaults / `SMOKE_ARGS`; JSON; no `error` key; body ≤ 6000 chars | any of those |

Keep `NO_RESPONSE_FORMAT_NEEDED`, `SKIP_IN_SMOKE`, `SMOKE_ARGS` as named
sets in Go, same members as the Python file.

## Work

1. Add `evals/` as a Go program (`package main`). Suggested layout:
   `evals/main.go` (CLI: `lint` / `smoke` / `all`, `--url`, `--token`).
   Default URL `http://127.0.0.1:8765/mcp`. Token from `--token` or
   `EVE_MCP_TOKEN`.
2. Speak MCP Streamable HTTP the same way the Python client does:
   JSON-RPC POST, `Accept: application/json, text/event-stream`, parse a
   `data: ` SSE line if present.
3. Point Makefile `lint` at `go run ./evals lint`. Add `smoke` and
   `eval` (`all`) targets the same way.
4. Rewrite `evals/README.md` in English (three levels: lint, smoke,
   tasks.yaml). Drop the Russian text.
5. Leave `evals/run.py` in place until T02 so a leftover docs link does
   not 404 mid-wave; Makefile must already use Go.

## Files

- Create: `evals/main.go` (split files in `evals/` if it stays readable)
- Rewrite: `evals/README.md`
- Edit: `Makefile`
- Do not edit: `evals/tasks.yaml` except if a comment still says `run.py`

## Acceptance

- [ ] `go run ./evals lint --help` works; `lint` / `smoke` / `all` exist
- [ ] Makefile `lint` does not invoke `python3`
- [ ] Exception sets match `evals/run.py` today
- [ ] `evals/README.md` is English and documents `--token` / `EVE_MCP_TOKEN`
- [ ] `go build ./evals` and `go build ./cmd/eve-mcp` succeed

## Verify

Against a running server with a bearer token (existing file-backed
binary is fine):

```bash
go run ./evals lint --url http://127.0.0.1:8765/mcp --token "$EVE_MCP_TOKEN"
```

If no token is available, still compile and confirm a missing server
prints a clear "cannot reach" error (same as Python).

## Done

Set `Status: done` here and in [README.md](README.md).
