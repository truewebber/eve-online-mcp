# T12 — Prometheus `/metrics` (later)

- Status: `later`
- Size: M
- Depends on: T11
- SPEC: §11, §12.5

Not part of the current wave. Do not start this while any `todo` in
T01–T11 is open.

## Goal

Internal listener (`INTERNAL_LISTEN`) serves Prometheus `/metrics`
**in addition to** `/healthz`. Never route it publicly.

## Suggested series (when this opens)

- ESI requests and errors by status
- Error-limit remain (gauge)
- Cache hit ratio
- Per-tool call count + latency
- Per-user bucket rejections (`UserRateLimited`)
- Mail-cap rejections
- Active users (approx)

## Do not (when it runs)

- Put metrics on the public mux
- Block tool calls on scrape
- Require a metrics env flag — if the internal server is up, metrics
  are up

## Done

Only when implemented: set `Status: done` here and in
[README.md](README.md).
