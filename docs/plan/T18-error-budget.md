# T18 — Per-character ESI error budget

- Status: `todo`
- Size: M
- Depends on: T15
- SPEC: §5.3, §12.8; error kinds in §4

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

It is `adapter/esi` plus one error mapping, next to the token bucket T09
already built. Tests are `testing/synctest` bubbles and recorded 4xx
responses — no injected clock (RULES.md §1).

## Do not

- Put the counter in Postgres. Per-pod memory is deliberate, and the rule
  survives N pods because the clamp reads the shared remainder off each
  pod's own traffic: three pods serving one greedy character all tighten
  together instead of each granting a private 20.
- Use a flat 20. The ⅕-of-remainder term is what makes it self-scaling;
  drop it and the rule stops working above one replica.
- Exempt `420` or `429`. They mean the budget is already gone.
- Charge a cache hit or a 304. Only responses that came back from CCP
  count.
- Remove the global fail-fast at remain < 15. It stays as the backstop
  for errors this attribution cannot see — CCP's own, for instance.
- Reuse the request bucket's numbers or its window. Different resource,
  different rule.

## Context

`internal/adapter/esi/bucket.go` holds the per-character token bucket
from T09 and `esi.UserLimited` is already mapped in
`usecase/session/errors.go` to `kind: UserRateLimited` with `retry_at`
and `retry_after_seconds`. This task adds a second, differently-shaped
limiter next to it and reuses that error path.

Error-limit state (`X-Esi-Error-Limit-Remain` / `-Reset`) is already read
off every response for the global fail-fast; the remainder it tracks is
the input to the clamp.

A rolling 60 s window per character: a ring of timestamps, or counters
per second — anything that does not need a background goroutine.

Note which errors are *expected*: a line member's `eve_corp_*` call
answers 403 every time, and an NPC-corp character's answers 403 for all
twelve tools. 20 a minute is enough for a model to discover that and stop,
which is the intended teaching signal, so the budget is a limit and not
an alarm.

## Work

1. Rolling-window error counter per character in `adapter/esi`, charged
   from the response path where the error-limit headers are already read.
2. Budget = `min(20, sharedRemainder/5)`, evaluated at charge time
   against the last-seen remainder.
3. Refuse before the request leaves the pod when the window is spent;
   `retry_at` is the end of the window.
4. Map to `UserRateLimited`, reusing T09's mapping, and make the two
   causes distinguishable in the message ("request allowance" vs "error
   budget") so the model's next sentence to the user is accurate.
5. Ship the error-budget and allowance rejection counters (SPEC §11) in
   this commit.
6. Tests, with T11's fixtures: 20 charged errors in a window then a
   refusal; a fake remainder of 50 clamps the budget to 10; `420` and
   `429` are charged; a 200 and a 304 are not; the window rolls and
   allows more; one character's spent budget does not touch another's.

## Files

- Edit: `internal/adapter/esi/bucket.go`, `internal/adapter/esi/esi.go`,
  `internal/adapter/esi/*_test.go`, `internal/usecase/session/errors.go`

## Acceptance

- [ ] Every ≥ 400 response is charged to the calling character
- [ ] The clamp against the shared remainder is implemented and tested
- [ ] Over budget refuses before any network call, with `retry_at`
- [ ] Cache hits, 304s and 2xx are never charged
- [ ] Characters are isolated from each other, covered by a test
- [ ] Both rejection counters exist
- [ ] The global fail-fast at remain < 15 still works
- [ ] `go test ./...` passes

## Verify

```bash
go test ./internal/adapter/esi ./internal/usecase/session -count=1
```

## Done

Set `Status: done` here and in [README.md](README.md).
