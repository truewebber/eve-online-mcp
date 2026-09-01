# T30 — Prometheus `/metrics`

- Status: `done`
- Size: M
- Depends on: T28
- RULES: §7 (the logger and every dependency is injected), §15 (one
  function, one job), §16 (no env for a constant)
- SPEC: §11, §12.15
- Replaces: old T26

## Goal

RED on the internal `/metrics`: the ESI client and the public HTTP
listener. Request count and duration by method, status, and a path
template. Nothing else.

## Do not

- Expose `/metrics` on the public listener. Internal only, never routed
  publicly (SPEC §1, §10).
- Put character names, character ids, client names, or raw URL paths in
  labels. ESI numeric segments become `{id}`; public paths are the mux
  pattern, or `other`.
- Count the internal listener (healthz / readyz / metrics).
- Publish cache, tool, mutation, session, sweep, or rejection series.
  The three limiter atomics stay next to the refusal; they are not
  Prometheus series.
- Reach for a package-level registry from inside a type. The collector
  is a dependency, set at construction, like the logger (RULES §7).
- Instrument by wrapping a function in a function that also does the
  work. Measurement is its own layer (RULES §15).

## Work

1. Prometheus client, registry, `/metrics` on the internal listener.
2. ESI RED around the outbound `Do`: method, status (`0` if no
   response), templated path, duration.
3. Public HTTP RED around the public mux: method, status, mux pattern
   or `other`, duration.
4. A short note that series are per pod.

## Acceptance

- [x] `/metrics` on the internal listener only
- [x] No label carries a character id, character name, client name, or
      a raw path parameter
- [x] Only ESI and public HTTP RED series
- [x] No new env
- [x] `go test ./...` and `make lint` pass

## Verify

```bash
curl -s http://127.0.0.1:8766/metrics | head -40
curl -s http://127.0.0.1:8765/metrics    # must not exist
```

## Done

Set `Status: done` here and in [README.md](README.md).
