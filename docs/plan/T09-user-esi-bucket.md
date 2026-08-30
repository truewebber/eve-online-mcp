# T09 — Per-user ESI token bucket

- Status: `done`
- Size: M
- Depends on: T07 (per-user ESI client already exists)
- SPEC: §5.3, §12.3, error kinds in §4

## Goal

A looping assistant cannot burn the shared CCP error-limit for the
whole instance. Network GETs/POSTs to ESI consume a per-user token
bucket (capacity **400**, refill **2/s**). Cache hits and 304s that
are served from cache are **free**. Exhaustion returns
`kind: UserRateLimited` with `retry_at` / `retry_after_seconds`. The
tool call must not sleep.

## Why this is one Composer session

Local to `adapter/esi` + `session.MapError` + one paragraph in
`eve.Instructions`. Tests are fake clocks / unit tests, no CCP.

## Do not

- Put the bucket in Postgres (SPEC: per pod memory; N× replicas is
  accepted).
- Count cache hits or 304-from-cache.
- Change CCP error-limit handling (`EsiRateLimited` stays).
- Add a general write budget.

## Context

`esi.Client` is already constructed per user in `session.ForUser` and
has a concurrency semaphore. Put the bucket on that client.

Count **outbound** HTTP to ESI (the request that actually leaves the
process). If `cachedGet` returns from `CacheGet` without a request,
do not Take a token.

New error type, e.g. `esi.UserLimited`, mapped in
`usecase/session/errors.go` to:

```
kind: UserRateLimited
retry_after_seconds
retry_at   (RFC3339)
hint       wait, do not loop
```

Constants: capacity 400, refill 2 per second — in `adapter/esi`, not
env.

## Work

1. Token bucket on `esi.Client` (mutex + float tokens + last refill
   time, or an equivalent).
2. Take 1 token just before a real HTTP round-trip. If not enough,
   return `UserLimited` computed from the deficit (`retry_after =
   deficit / 2`).
3. MapError + `eve.Instructions` line next to `EsiRateLimited`.
4. Tests: 400 takes succeed; 401st fails; wait/refill allows more;
   a helper that fakes a cache hit does not decrement.

## Files

- Edit: `adapter/esi/esi.go`, `adapter/esi/*_test.go`,
  `usecase/session/errors.go`, `usecase/eve/register.go` (instructions)

## Acceptance

- [x] Tests cover empty bucket and cache-hit exemption
- [x] Mapped JSON kind is exactly `UserRateLimited`
- [x] Instructions tell the model to wait, not retry in a loop
- [x] No new env vars

## Verify

```bash
go test ./internal/adapter/esi ./internal/usecase/session -count=1
```

## Done

Set `Status: done` here and in [README.md](README.md).
