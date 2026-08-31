# T22 — Config and edges: env names, `PUBLIC_URL`, the per-IP limit, `/readyz`

- Status: `todo`
- Size: M
- Depends on: T17
- RULES: §16 (config is env on the binary), §8 (assembled URLs),
  §7 (the logger is a dependency), §15 (one function, one job)
- SPEC: §2, §5.5, §6, §10, §12.10 — this task edits §2
- Replaces: old T20

## Goal

Everything that guards the outside of the process, none of it product
logic:

- **Env names that say what they hold** (RULES §16). `LISTEN` and
  `INTERNAL_LISTEN` name an address, which the rule forbids: *"Do not
  name an env `ENDPOINT` or `ADDRESS`. An address is scheme, host, port,
  path — each a field, combined in the name when they travel together
  (`EVE_API_HOST`, `API_HOST_PORT`)."*
- `EXTRA_REDIRECT_URIS`, validated as absolute, wildcard-free,
  fragment-free.
- `PUBLIC_URL` **required** whenever the public listener does not bind
  loopback, and HTTPS unless the host is loopback. The base URL is
  `PUBLIC_URL` or the loopback bind — never the request's `Host` header,
  which is attacker-controlled and would let a caller mint metadata
  pointing anywhere.
- The per-IP rate limit covering **every** unauthenticated public route,
  `GET /` included, at 60 requests per minute.
- The `CF-Connecting-IP` trust rule.
- `/readyz` with a Postgres ping, separate from `/healthz`.
- `0.0.0.0` binds documented for Kubernetes and the bearer-hash affinity
  annotation for above one replica.

## The env rename, decided here

`LISTEN` → `LISTEN_HOST_PORT`, `INTERNAL_LISTEN` →
`INTERNAL_LISTEN_HOST_PORT`. They carry a host and a port travelling
together, which is exactly the compliant form the rule gives.

`PUBLIC_URL` stays. RULES §16 objects to a name that hides which parts
it holds; a URL is scheme, host, port and path, and the name says so.
It also travels as one value into three places that must not disagree —
the `iss` of every token we sign, the `resource` of the PRM document,
and the EVE callback (SPEC §2).

SPEC §2's table, `.env.example`, `README.md`, the deployment note and
`docker-compose.yml` change in the same commit as the code.

## Why this is one Composer session

All of it lives in `cmd/eve-mcp/config.go`, `service/http` and a
deployment note. None of it touches a tool or a table.

## Do not

- Derive the base URL from `Host`, ever, including "just for the human
  pages".
- Trust `CF-Connecting-IP` unconditionally. It is trusted only when the
  public listener is unreachable except through the tunnel; otherwise
  the socket address is used. Behind a tunnel every request shares one
  socket address, so getting this backwards turns a per-IP limit into a
  per-household one and the first friend to open their laptop locks out
  the rest.
- Make `/healthz` touch Postgres. Liveness answers "is the process
  serving"; a database outage is not something a restart fixes.
- Add env for anything SPEC §2 lists as a constant. The limiter's 60/min
  and the readiness timeout are constants in the package that owns them
  (RULES §16).
- Build a URL by concatenation while you are in here (RULES §8, T16).
- Let `loadConfig` grow into "read, parse, validate, derive". Each is a
  function (RULES §15); `validate` is already doing three jobs.
- Reach for a logger inside the limiter. It takes `log.Logger` at
  construction like everything else (RULES §7).

## Context

`cmd/eve-mcp/config.go` is `package main` and is never imported by inner
layers; it maps into per-module `Options`. Validation is fatal at boot.

`internal/service/http/listen.go` owns both listeners; `/healthz` is
there today with a static body.

The public routes needing the limit: `/oauth/register`,
`/oauth/authorize`, `/oauth/token`, `/auth/callback`, `/auth/login`,
both `/.well-known/*` documents and `GET /`. Two of them write rows,
which is why the limit exists; `GET /` is on the list because it reports
ESI reachability and is therefore the one unauthenticated route that can
cause ESI traffic — traffic that belongs to no character, so neither the
allowance nor the error budget can meter it.

The limiter is per pod, which is fine for what it defends: it bounds row
growth, and T21's sweeps are what keep the tables finite.

The client-IP helper is shared with `sessions.ip` from T17, which took
the socket address unconditionally pending this task.

## Work

1. Config: rename the two listener envs; parse `EXTRA_REDIRECT_URIS`
   and validate each entry; `PUBLIC_URL` required-off-loopback plus
   scheme check; `HMAC_KEY` length (T12 made it required); one
   `BaseURL()` that never consults a request. Split `validate` into the
   checks it performs.
2. Per-IP limiter in `service/http` across the listed routes, `429` with
   `Retry-After`, constructed with its logger.
3. Client IP resolution helper shared by the limiter and `sessions.ip`,
   implementing the tunnel rule.
4. `/readyz`: liveness plus a pool ping under a short timeout, `503` on
   failure. `/healthz` stays dependency-free.
5. SPEC §2, `.env.example`, `README.md`, `docker-compose.yml` and the
   deployment note updated together: `0.0.0.0` binds, `PUBLIC_URL`,
   probe split, `upstream-hash-by: "$http_authorization"` with the
   hour-scale caveat, pool sizing (`pool_max_conns × replicas` inside
   `max_connections`).
6. Tests: boot refuses a non-loopback bind with empty `PUBLIC_URL`;
   refuses `http://` on a public host; refuses a wildcard redirect URI;
   refuses a short `HMAC_KEY`; the limiter returns `429` with
   `Retry-After` on the 61st request in a minute; `/readyz` is `503`
   with the database down while `/healthz` is `200`.

## Files

- Edit: `cmd/eve-mcp/config.go`, `cmd/eve-mcp/main.go`,
  `internal/service/http/listen.go`, `internal/service/http/handler.go`,
  `internal/usecase/oauth/*.go` (redirect allowlist), `docs/SPEC.md` §2,
  `.env.example`, `README.md`, `docker-compose.yml`

## Acceptance

- [ ] No env is named for an address; the listener envs carry
      `_HOST_PORT`, and SPEC §2 says the same
- [ ] Boot validation covers `PUBLIC_URL`, its scheme, the redirect URIs
      and `HMAC_KEY`, each with a test
- [ ] The base URL never comes from `Host`
- [ ] Every unauthenticated public route is rate-limited, `GET /`
      included
- [ ] `CF-Connecting-IP` is trusted only under the documented condition,
      and the same helper fills `sessions.ip`
- [ ] `/readyz` pings Postgres; `/healthz` does not
- [ ] No new env for a value SPEC §2 calls a constant
- [ ] `go test ./...` and `make lint` pass

## Verify

```bash
go test ./cmd/eve-mcp ./internal/service/http -count=1
rg -n 'LISTEN\b|INTERNAL_LISTEN\b' --glob '!docs/plan/**'
LISTEN_HOST_PORT=0.0.0.0:8765 ./eve-mcp     # must refuse without PUBLIC_URL
```

## Done

Set `Status: done` here and in [README.md](README.md).
