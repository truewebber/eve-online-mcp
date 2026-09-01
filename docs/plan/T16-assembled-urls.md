# T16 — URLs are assembled, never concatenated

- Status: `done`
- Size: S
- Depends on: T15
- RULES: §8 (URLs are assembled), §16 (only the host is configured)
- SPEC: §3.1, §3.2, §9; AUTH.md
- New in the 2026-08-31 audit; no §12 item

## Goal

RULES §8: a host, a path, a query and a fragment are never glued with
`+` or `fmt.Sprintf`. The string form of a URL is the output of
`url.URL`, `url.Values`, `url.JoinPath` / `path.Join` and
`net.JoinHostPort`. That is what keeps `../`, a second host and an
unescaped value out of a request — the same class of bug as SSRF, an
open redirect, an XSS in a `Location`, and an IDOR that is only an id
spliced into a path.

And the second half of the rule, which is the part this tree gets wrong:
*"Parsing a base URL and `JoinPath` on every call is assembling a host
that was already injected — keep `url.URL` (or the host) on the client,
set `Path` and `RawQuery` from the hardcoded endpoint."*

## Why this is one Composer session

Four call sites, one shape, no behaviour change if it is done right —
and every URL the service emits or requests goes through one of them.

It sits between T15 and T17 on purpose. T15 moves files inside
`adapter/esi` and this task rewrites `adapter/esi/http/client.go`, so
running them in parallel is a merge conflict for no gain. And T17
rewrites the SSO client's token and revoke calls, which should be
rewriting code that is already in shape.

## Do not

- Configure an endpoint. Only the host is a dependency; paths are
  constants in the code that calls them (RULES §16). `esi.evetech.net`
  and `login.eveonline.com` are hosts; `/v2/oauth/token` is a constant.
- Re-parse a base URL on every request. Parse once, at construction,
  into a `url.URL` the client holds.
- Build a query with `fmt.Sprintf` or `strings.Join`. `url.Values`.
- Use `filepath.Join` on anything that is not a file.
- "Fix" it by escaping the pieces before concatenating them. The point
  is not escaping, it is that assembly cannot produce a second host.
- Change the callback URL's bytes. It must stay exactly what is
  registered at CCP (SPEC §3.2), so the test that pins it is part of
  this task, not an afterthought.

## Context

Where it is wrong today:

- `internal/adapter/esi/http/client.go` builds request URLs from a base
  plus `internal/adapter/esi/path.go`'s `path.Join` on each call, with
  parameters folded in separately.
- `internal/adapter/sso/http/client.go` reaches four
  `login.eveonline.com` endpoints (authorize, token, revoke, JWKS).
- `internal/usecase/oauth` builds the PRM and AS-metadata documents, the
  authorize redirect to EVE, and the redirect back to the MCP client,
  from an `oauth.Host` that carries strings.
- `cmd/eve-mcp/config.go` parses `PUBLIC_URL` and calls `JoinPath` to
  derive `CallbackURL`, and falls back to a `url.URL` literal otherwise.
  The second half is the shape the first half wants.

`url_test.go` already exists in `adapter/esi/http` and in
`usecase/oauth`; extend those rather than starting new files.

The base URL never comes from the request's `Host` header — that rule is
SPEC §2 and T22 owns enforcing it. Here, just do not introduce a path
that could.

## Work

1. `adapter/esi/http`: the client holds one `url.URL` (scheme + host)
   built at construction. Each call sets `Path` from an endpoint
   constant with `JoinPath` for id segments only, and `RawQuery` from
   `url.Values`.
2. `adapter/sso/http`: the same, with the four endpoints as constants.
3. `usecase/oauth`: `Host` holds one parsed `url.URL`; the resource URL,
   the AS metadata document, the EVE authorize redirect and the redirect
   back to the client are all derived from it. No `fmt.Sprintf` with a
   host or a path in it.
4. `cmd/eve-mcp/config.go`: parse once into a `url.URL`; derive the
   callback from it; the loopback default is built the same way.
5. Tests: an id containing `../` or `//host` cannot leave the intended
   path; a query value containing `&`, `=` or a space is escaped; the
   callback URL is byte-identical to the registered one for both the
   `PUBLIC_URL` and the loopback case; the redirect back to the MCP
   client preserves the client's own query and adds only `code` and
   `state`.

## Files

- Edit: `internal/adapter/esi/http/client.go`,
  `internal/adapter/esi/path.go`, `internal/adapter/sso/http/client.go`,
  `internal/usecase/oauth/oauth.go`, `cmd/eve-mcp/config.go`
- Edit tests: `internal/adapter/esi/http/url_test.go`,
  `internal/usecase/oauth/url_test.go`, `cmd/eve-mcp/config_test.go`

## Acceptance

- [x] No URL in the tree is produced by `+` or `fmt.Sprintf`
- [x] Each HTTP client holds one parsed `url.URL`; no base URL is parsed
      per request
- [x] Every endpoint path is a constant in the code, not config
- [x] `filepath.Join` appears only on file paths
- [x] Path-traversal and escaping tests exist for both clients
- [x] The callback URL is unchanged, asserted by a test
- [x] `go test ./...` and `make lint` pass

## Verify

```bash
rg -n 'fmt\.Sprintf\("https?://|"https?://" *\+|filepath\.Join' --glob '*.go'
go test ./internal/adapter/esi/http ./internal/adapter/sso/http ./internal/usecase/oauth ./cmd/eve-mcp -count=1
```

## Done

Set `Status: done` here and in [README.md](README.md).
