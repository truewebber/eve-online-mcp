# T20 — Config and edges: `PUBLIC_URL`, the per-IP limit, `/readyz`

- Status: `todo`
- Size: M
- Depends on: T15
- SPEC: §2, §5.5, §6, §10, §12.10
- Replaces: part of the deleted docs catch-up task; T25 has the rest

## Goal

Everything that guards the outside of the process, none of it product
logic:

- `EXTRA_REDIRECT_URIS`, validated as absolute, wildcard-free, fragment-free.
- `PUBLIC_URL` **required** whenever `LISTEN` does not bind loopback, and
  HTTPS unless the host is loopback. The base URL is `PUBLIC_URL` or
  `http://{LISTEN}` — never the request's `Host` header, which is
  attacker-controlled and would let a caller mint metadata pointing
  anywhere.
- `HMAC_KEY` length check (T12 made it required; this makes it validated
  if it is not already).
- The per-IP rate limit covering **every** unauthenticated public route,
  `GET /` included, at 60 requests per minute.
- The `CF-Connecting-IP` trust rule.
- `/readyz` with a Postgres ping, separate from `/healthz`.
- `0.0.0.0` binds documented for Kubernetes and the bearer-hash affinity
  annotation for above one replica.

## Why this is one Composer session

All of it lives in `cmd/eve-mcp/config.go`, `service/http` and a
deployment note. None of it touches a tool or a table.

## Do not

- Derive the base URL from `Host`, ever, including "just for the human
  pages".
- Trust `CF-Connecting-IP` unconditionally. It is trusted only when the
  public listener is unreachable except through the tunnel; otherwise the
  socket address is used. Behind a tunnel every request shares one socket
  address, so getting this backwards turns a per-IP limit into a
  per-household one and the first friend to open their laptop locks out
  the rest.
- Make `/healthz` touch Postgres. Liveness answers "is the process
  serving"; a database outage is not something a restart fixes.
- Claim the bearer-hash affinity makes the per-character counters exact.
  The access token lives an hour, so it buys an hour of stickiness
  (SPEC §1) — say that in the deployment note.
- Add env for anything SPEC §2 lists as a constant.

## Context

`cmd/eve-mcp/config.go` is `package main` and is never imported by inner
layers; it maps into per-module `Options`. Validation is fatal at boot.

`internal/service/http/listen.go` owns both listeners; `/healthz` is
there today with a static body.

The public routes needing the limit: `/oauth/register`,
`/oauth/authorize`, `/oauth/token`, `/auth/callback`, `/auth/login`, both
`/.well-known/*` documents and `GET /`. Two of them write rows, which is
why the limit exists; `GET /` is on the list because it reports ESI
reachability and is therefore the one unauthenticated route that can
cause ESI traffic — traffic that belongs to no character, so neither the
allowance nor the error budget can meter it.

The limiter is per pod, which is fine for what it defends: it bounds row
growth, and T19's sweeps are what keep the tables finite.

## Work

1. Config: `EXTRA_REDIRECT_URIS` parsing and validation; `PUBLIC_URL`
   required-off-loopback plus scheme check; `HMAC_KEY` length; a single
   `BaseURL()` that never consults a request.
2. Per-IP limiter in `service/http` across the listed routes, `429` with
   `Retry-After`.
3. Client IP resolution helper shared by the limiter and
   `sessions.ip`, implementing the tunnel rule.
4. `/readyz`: liveness plus `db.Ping` under a short timeout, `503` on
   failure. `/healthz` stays dependency-free.
5. Deployment note in the repo where deployment lives today: `0.0.0.0`
   binds, `PUBLIC_URL`, probe split, `upstream-hash-by:
   "$http_authorization"` with the hour-scale caveat, pool sizing
   (`pool_max_conns × replicas` inside `max_connections`).
6. Tests: boot refuses a non-loopback `LISTEN` with empty `PUBLIC_URL`;
   refuses `http://` on a public host; refuses a wildcard redirect URI;
   the limiter returns `429` with `Retry-After` on the 61st request in a
   minute; `/readyz` is `503` with the database down while `/healthz` is
   `200`.

## Files

- Edit: `cmd/eve-mcp/config.go`, `cmd/eve-mcp/main.go`,
  `internal/service/http/listen.go`, `internal/service/http/handler.go`,
  `internal/usecase/oauth/oauth.go` (redirect allowlist)

## Acceptance

- [ ] Boot validation covers all four rules, each with a test
- [ ] The base URL never comes from `Host`
- [ ] Every unauthenticated public route is rate-limited, `GET /`
      included
- [ ] `CF-Connecting-IP` is trusted only under the documented condition
- [ ] `/readyz` pings Postgres; `/healthz` does not
- [ ] `go test ./...` passes

## Verify

```bash
go test ./cmd/eve-mcp ./internal/service/http -count=1
LISTEN=0.0.0.0:8765 ./eve-mcp     # must refuse without PUBLIC_URL
```

## Done

Set `Status: done` here and in [README.md](README.md).
