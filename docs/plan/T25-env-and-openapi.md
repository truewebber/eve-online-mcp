# T25 — `.env.example` and `api/http.yaml`

- Status: `todo`
- Size: S
- Depends on: T20
- SPEC: §2, §6, §12.14
- Replaces: part of the deleted docs catch-up task; T20 has the rest

## Goal

The two generated-adjacent artefacts that describe the service to a human
and to a code generator, brought in step with the env set and topology the
rest of the work established. `README.md` and `docs/` are already there.

## Why this is one Composer session

Two files, no logic, and it wants to land after T20 fixes what the env set
actually is.

## Do not

- Add `/mcp` to `api/http.yaml`. It is mounted manually next to the
  generated mux so oapi-codegen cannot steal the route (SPEC §6).
- Put a real secret in `.env.example`. `HMAC_KEY` gets the command that
  generates one, not a value.
- Describe env that SPEC §2 lists as a constant.
- Regenerate `api.gen.go` without running `make gen` and committing the
  result.

## Context

`.env.example` predates the current env set: `HMAC_KEY` is required now,
`DATABASE_URL` is the only store, `PUBLIC_URL` is required off loopback,
`EXTRA_REDIRECT_URIS` exists, and nothing about a data directory survives.

`api/http.yaml` is the source of truth for the generated server. Routes
per SPEC §6: `/`, `/auth/login`, `/auth/callback`, both `/.well-known/*`,
`/oauth/register`, `/oauth/authorize`, `/oauth/token`. The internal
listener now has `/healthz` **and** `/readyz` — decide whether the YAML
covers the internal listener at all and be consistent, since it is
cluster-only and never routed publicly.

The description block in the YAML is what a reader meets first, so it
should say what the service is, that one connection is one character, and
that `/mcp` lives outside this document on purpose.

## Work

1. Rewrite `.env.example`: every variable from SPEC §2 with its default
   and one line of why, required ones marked, `HMAC_KEY` with
   `openssl rand -hex 32`.
2. Update `api/http.yaml`: description, routes, `/readyz`, and any
   response shape that changed (the refusal page from T16 is HTML, not
   JSON — make sure the spec does not claim otherwise).
3. `make gen`, commit the regenerated `api.gen.go`.
4. Check the human pages still match what the YAML says they return.

## Files

- Edit: `.env.example`, `api/http.yaml`, `internal/service/http/api.gen.go`

## Acceptance

- [ ] Every env in SPEC §2 appears in `.env.example` with the same
      default and required-ness, and nothing else does
- [ ] `api/http.yaml` matches SPEC §6's route table
- [ ] `/mcp` is still absent from the YAML
- [ ] `make gen` produces no diff after the commit
- [ ] `go build ./cmd/eve-mcp` and `go test ./...` pass

## Verify

```bash
make gen && git diff --exit-code
go build ./cmd/eve-mcp
```

## Done

Set `Status: done` here and in [README.md](README.md).
