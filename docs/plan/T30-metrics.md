# T30 — Prometheus `/metrics` (later)

- Status: `later`
- Size: M
- Depends on: T28
- RULES: §7 (the logger and every dependency is injected), §15 (one
  function, one job), §16 (no env for a constant)
- SPEC: §11, §12.15
- Replaces: old T26

## Goal

The rest of `/metrics` on the internal listener, after everything else
has landed. The three rejection counters are **not** part of this task:
they ship with the limiters that produce them, in T19 and T20, because a
counter incremented in the same function as the refusal is a line of
code while coming back for it later means re-reading §5.

Everything else per SPEC §11: ESI requests and errors by status, the
error-limit remain gauge, cache hit ratio and bytes held, per-tool call
count and latency, mutations by tool and outcome, active sessions, sweep
run age.

## Why this is later

Nothing depends on it, and every series it exposes describes code the
earlier tasks are still moving. Building it first means rewriting it.

## Do not

- Expose `/metrics` on the public listener. Internal only, never routed
  publicly (SPEC §1, §10).
- Put character names, character ids or client names in labels. A metric
  cardinality problem and a privacy problem at once; per-character
  detail belongs in the audit log, not in a time series.
- Average a per-pod ratio across pods without weighting. A cache hit
  ratio averaged that way is a lie when replicas roll — expose hits and
  misses as counters and let the query do the division.
- Re-implement the three rejection counters. Read them where T19 and T20
  left them.
- Reach for a package-level registry from inside a type. The collector
  is a dependency, set at construction, like the logger (RULES §7).
- Add env for the scrape interval or the collection interval. They are
  constants in the package that owns them (RULES §16).
- Instrument by wrapping a function in a function that also does the
  work. Measurement is its own layer (RULES §15).

## Context

The internal listener already exists with `/healthz` and `/readyz`
(T22). Everything except the database-derived series is per pod and has
to be summed across them, which is a documentation problem as much as a
code one — say it next to the endpoint.

Database-derived series (active sessions, sweep run age) are a query,
not a counter, so decide the collection interval deliberately rather
than scraping Postgres on every request.

## Work

1. Prometheus client, registry, `/metrics` on the internal listener.
2. ESI instrumentation: request counter by method and status,
   error-limit remain gauge, cache hits/misses/bytes/entries.
3. Tool instrumentation: call counter and latency histogram by tool name
   and outcome.
4. Mutation counter by tool and outcome, read from the same place the
   audit row is written.
5. Database-derived gauges on an interval: active sessions, last
   successful sweep age.
6. A short note on which series are per pod.

## Files

- Edit: `internal/service/http/listen.go`, `internal/adapter/esi/**`,
  `internal/usecase/eve/common.go`, `internal/domain/write/guard.go`,
  `internal/usecase/sweep/*`, `cmd/eve-mcp/main.go`, `go.mod`

## Acceptance

- [ ] `/metrics` on the internal listener only
- [ ] No label carries a character id, character name or client name
- [ ] Cache hits and misses are separate counters, not a ratio
- [ ] The three rejection counters are reused, not duplicated
- [ ] Database-derived gauges are collected on an interval
- [ ] No new env
- [ ] `go test ./...` and `make lint` pass

## Verify

```bash
curl -s http://127.0.0.1:8766/metrics | head -40
curl -s http://127.0.0.1:8765/metrics    # must not exist
```

## Done

Set `Status: done` here and in [README.md](README.md).
