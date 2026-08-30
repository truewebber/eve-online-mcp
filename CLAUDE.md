# eve_online

A one-binary MCP server that exposes EVE Online accounts to LLM clients
through CCP's ESI API, plus guarded write access back into the game. The
instance owns one EVE application; each player signs in with EVE in the
browser.

**Target vs current.** Product target is `docs/SPEC.md` (plus `TOOLS.md`
and `ESI.md`). Remaining work is sliced in `docs/plan/README.md` — pick
the first `todo` task and follow that file. Where this file disagrees
with the spec (write-capability gates, audit log), the spec and the
task file win.

## Two modes

This directory is used for two different things. Work out which one is being
asked for before reaching for tools:

- **Playing** — questions about the user's own characters, ISK, assets, market,
  routes. Answer with the `eve` MCP tools. Game mechanics, formulas and
  multi-step routines live in the `eve-online` skill; load it rather than
  reasoning about EVE economics from memory.
- **Developing** — changing the server itself. The rest of this file is about
  that.

Do not restate tool behaviour here. Each tool documents itself, and the server
sends cross-cutting call rules in its `instructions`. This file is only for
things that change how you *edit the repo*.

## Running it

The server is a Go binary on the host. Postgres is Compose-only
(`make postgres`); do not put the app in Compose. No `.env` on the MCP client.

```bash
go build -o eve-mcp ./cmd/eve-mcp
./eve-mcp                         # foreground
./eve-mcp install                 # user service (launchd / systemd --user)
go run ./evals all                # lint + smoke against http://127.0.0.1:8765/mcp
```

Config is env only (`CLIENT_ID` and `DATABASE_URL` are required; see
`.env.example`), read from the process environment or a `.env` in the
working directory. The installed service uses the OS user config dir as
its working directory so `.env` lives at
`~/Library/Application Support/eve-mcp/.env` on macOS. Durable state is
Postgres. There is no config file and nothing is written back to config
at runtime.

The process does not reload code or config in place. Rebuild, then restart
(`launchctl kickstart -k gui/$(id -u)/eve-mcp` after `install`).

Do not recreate a git remote unless the user asks. The repo is local-only.

**Do not run `docker compose down -v`.** That deletes the `eve-mcp-pg`
volume. `make down` is enough to stop Postgres.

## Invariants

Break these and the server regresses in ways tests will not obviously catch.

**Tool definitions.** `go run ./evals lint` talks to the running server and
enforces most of this.

- Every parameter needs a `jsonschema` tag — that is the description the
  model reads. The whole tag is the description (jsonschema-go does not parse
  `minimum=` from tags).
- Any tool returning a list needs a small default `limit` and a
  `response_format` of `"concise" | "detailed"`. Concise is the default.
- Error strings say what to do next, naming the tool that fixes it. Never
  return a bare status code.
- Names follow `eve_<domain>_<action>`. Renaming a tool is a breaking change
  for anyone who wired it up.

**Never add typed output schemas.** Tools return JSON as `TextContent` and
`nil` structured output. A typed `Out` on `mcp.AddTool` would drop
undeclared keys. Response shape is documented in each tool's `Returns:` line.

**Every mutating tool goes through `session.Guard.Authorize()`** before it
touches ESI, and `Record()` after. That is what enforces the capability gate,
the confirm-token cycle, the hourly budget and the audit log. A write path
that skips it bypasses all four. Write tools are also registered only when
their capability is enabled, so the tool list is itself a security boundary.

**All ESI traffic goes through `adapter/esi.Client`.** It carries the
identifying User-Agent, the pinned compatibility date, ETag caching and
error-limit headers. On `420` / a spent `X-Esi-Error-Limit-Remain` the
client must **not** sleep the tool call — return `esi.RateLimited` so the
model sees `retry_at`. Never call ESI with a bare `http.Client` request.

`/mcp` is an OAuth resource (RFC 9728). Unauthenticated calls get `401` +
`WWW-Authenticate`. `/oauth/authorize` redirects straight to EVE SSO with the
instance application; the finished character attaches to the server-side user
in the MCP token's `sub` (an existing user if the character is already known).
Do not put CCP credentials in client `mcp.json`.

`/healthz` (and Prometheus metrics when they land) are served on
`INTERNAL_LISTEN`, a second HTTP server that must never be routed publicly.

## Things that will surprise you

- **ESI search matches on prefix, not fuzzily.** `Tritanum` finds nothing.
  `eve_universe_search` compensates by shortening the prefix and retrying.
- **`/universe/names` resolves ids in one shared space.** Group ids are not in
  it — resolving `group_id` there returns whatever inventory type shares the
  number. Use `Resolver.GroupName`.
- **The compatibility date is pinned** to `2026-08-18` in
  `cmd/eve-mcp/config.go` (`defaultCompatDate`). ESI moved from `/v1/` URLs to
  the `X-Compatibility-Date` header. Response shapes for everything used here
  match the 2020-01-01 baseline; the newer date only adds routes. Moving the
  pin means re-checking every response shape.
- **`/route/` is POST on recent compatibility dates**, with `preference` in the
  body, not a `flag` query parameter.

## Layout

```
api/                  OpenAPI source (http.yaml) + oapi-codegen config
cmd/eve-mcp/          binary: main.go, config.go (env-tagged, package main),
                      launchd/systemd install
internal/
  adapter/            external systems (ESI, EVE SSO, SQLite, user files)
  domain/             internal model (character, user, write policy, j)
  usecase/            business logic (session, oauth, eve tools)
  service/            interaction: HTTP (generated OpenAPI) and MCP
evals/                lint and smoke gates, agentic task definitions
```

Import direction: `service → usecase → adapter|domain`. Process config lives
only in `cmd/eve-mcp/config.go` (package main, no `EVE_` prefix on env names).
Map it to `Options` / `New(...)` in `main`. Regenerate HTTP types with
`make gen`; output is `internal/service/http/api.gen.go`.
