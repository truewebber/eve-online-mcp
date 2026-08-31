# T20 — Per-character ESI error budget

- Status: `todo`
- Size: M
- Depends on: T17
- RULES: §1 (time is not a test seam), §15 (one function, one job),
  §5 (tests), §10 (one result)
- SPEC: §5.3, §12.8; error kinds in §4
- Replaces: old T18

## Goal

CCP's scarce resource is not requests, it is errors: 100 responses ≥ 400
per 60 s **for the whole IP**. Without attribution, one player whose
assistant hammers an endpoint their roles do not allow spends everyone's
budget and the global fail-fast (§5.1) shuts the instance down for the
household — exactly what PRD §5 promises will not happen.

Every ESI response with status ≥ 400 is charged to the character whose
tool call produced it. Budget: **the lesser of 20 and one fifth of the
shared remainder** last reported by `X-Esi-Error-Limit-Remain`, per
rolling 60 s window per character. Over budget → `UserRateLimited` with
`retry_at`, and no request leaves the pod.

## Why this is one Composer session

It is one limiter next to the token bucket T09 already built, plus one
error mapping. The tests are `synctest` bubbles and recorded 4xx
responses, both of which T11 provided.

## Do not

- **Inject a clock.** RULES §1 is explicit and this is the task most
  likely to break it: a rolling window wants a `now func() time.Time`
  and must not get one. The counter calls `time.Now()` itself, and the
  test runs it inside `synctest.Test`, where `time.Now`, `time.Sleep`
  and timers are virtual. `synctest.Wait` is how the test knows the
  bubble is durably blocked.
- Put the counter in Postgres. Per-pod memory is deliberate, and the
  rule survives N pods because the clamp reads the shared remainder off
  each pod's own traffic: three pods serving one greedy character all
  tighten together instead of each granting a private 20.
- Use a flat 20. The ⅕-of-remainder term is what makes it self-scaling;
  drop it and the rule stops working above one replica.
- Exempt `420` or `429`. They mean the budget is already gone.
- Charge a cache hit or a 304. Only responses that came back from CCP
  count.
- Remove the global fail-fast at remain < 15. It stays as the backstop
  for errors this attribution cannot see — CCP's own, for instance.
- Reuse the request bucket's numbers or its window. Different resource,
  different rule.
- Add a fourth branch to `request`. `client.go` is 830 lines and
  `request` is 70; the budget is its own type in its own file, consulted
  once before the call and charged once after (RULES §15).
- Return `(allowed, retryAt, error)`. One decision struct (RULES §10).

## Context

`internal/adapter/esi/http/bucket.go` holds the per-character token
bucket from T09, and its test already runs under `testing/synctest` —
copy that shape. `esi.UserLimited` is already mapped in
`usecase/session/errors.go` to `kind: UserRateLimited` with `retry_at`
and `retry_after_seconds`.

Error-limit state (`X-Esi-Error-Limit-Remain` / `-Reset`) is already
read off every response for the global fail-fast; the remainder it
tracks is the input to the clamp. T11's fixtures include a 403 and a 420
carrying those headers.

A rolling 60 s window per character: a ring of timestamps, or counters
per second — anything that does not need a background goroutine.

Note which errors are *expected*: a line member's `eve_corp_*` call
answers 403 every time, and an NPC-corp character's answers 403 for all
twelve tools. Twenty a minute is enough for a model to discover that and
stop, which is the intended teaching signal, so the budget is a limit
and not an alarm.

## Work

1. Rolling-window error counter per character in `adapter/esi/http`, its
   own file and type, charged from the response path where the
   error-limit headers are already read.
2. Budget = `min(20, sharedRemainder/5)`, evaluated at charge time
   against the last-seen remainder.
3. Refuse before the request leaves the pod when the window is spent;
   `retry_at` is the end of the window.
4. Map to `UserRateLimited`, reusing T09's mapping, and make the two
   causes distinguishable in the message ("request allowance" vs "error
   budget") so the model's next sentence to the user is accurate.
5. Ship the error-budget and allowance rejection counters (SPEC §11) in
   this commit.
6. Tests under `synctest`, with T11's fixtures: 20 charged errors in a
   window then a refusal; a fake remainder of 50 clamps the budget to
   10; `420` and `429` are charged; a 200 and a 304 are not; the window
   rolls and allows more; one character's spent budget does not touch
   another's.

## Files

- Add: `internal/adapter/esi/http/budget.go` and its test
- Edit: `internal/adapter/esi/http/client.go` (consult and charge, no
  new branch in `request`), `internal/adapter/esi/esi.go` (the error),
  `internal/usecase/session/errors.go`

## Acceptance

- [ ] Every ≥ 400 response is charged to the calling character
- [ ] The clamp against the shared remainder is implemented and tested
- [ ] Over budget refuses before any network call, with `retry_at`
- [ ] Cache hits, 304s and 2xx are never charged
- [ ] Characters are isolated from each other, covered by a test
- [ ] Both rejection counters exist
- [ ] The global fail-fast at remain < 15 still works
- [ ] `rg -n 'Clock|now func\(\)|WithClock|timeNow'` finds nothing, and
      the window tests run under `synctest`
- [ ] `go test ./...` and `make lint` pass

## Verify

```bash
go test ./internal/adapter/esi/... ./internal/usecase/session -count=1
rg -n 'Clock|now func\(\)|WithClock' --glob '*.go'
```

## Done

Set `Status: done` here and in [README.md](README.md).
