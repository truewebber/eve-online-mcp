# eve_online

A one-binary MCP server that exposes one player's EVE Online account to an
LLM through CCP's ESI API, plus guarded write access back into the game.

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

The server is a Go binary. No Docker, no `.env` on the client.

```bash
go build -o eve-mcp ./cmd/eve-mcp
./eve-mcp                         # foreground
./eve-mcp install                 # user service (launchd / systemd --user)
python3 evals/run.py all          # lint + smoke against http://127.0.0.1:8765/mcp
```

Config, refresh tokens, HTTP cache and the audit log live in the OS user
config directory (`~/Library/Application Support/eve-mcp` on macOS). A
repo-local `.env` is imported once if `client_id` is still empty.

The process does not reload code or config in place. Rebuild, then restart
(`launchctl kickstart -k gui/$(id -u)/eve-mcp` after `install`).

Do not recreate a git remote unless the user asks. The repo is local-only.

**Do not run `docker compose down -v`.** The leftover `eve-mcp-data` volume
still holds the previous Python install's SSO refresh tokens.

## Invariants

Break these and the server regresses in ways tests will not obviously catch.

**Tool definitions.** `evals/run.py lint` talks to the running server and
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

**Every mutating tool goes through `a.Guard.Authorize()`** before it touches
ESI, and `a.Guard.Record()` after. That is what enforces the capability gate,
the confirm-token cycle, the hourly budget and the audit log. A write path
that skips it bypasses all four. Write tools are also registered only when
their capability is enabled, so the tool list is itself a security boundary.

**All ESI traffic goes through `esi.Client`.** It carries the identifying
User-Agent, the pinned compatibility date, ETag caching and error-limit
headers. On `420` / a spent `X-Esi-Error-Limit-Remain` the client must
**not** sleep the tool call — return `esi.RateLimited` so the model sees
`retry_at`. Never call ESI with a bare `http.Client` request.

`/mcp` is an OAuth resource (RFC 9728). Unauthenticated calls get `401` +
`WWW-Authenticate`. Players bring their own EVE `client_id` on
`/oauth/authorize`. Do not put CCP credentials in client `mcp.json`.

## Things that will surprise you

- **ESI search matches on prefix, not fuzzily.** `Tritanum` finds nothing.
  `eve_universe_search` compensates by shortening the prefix and retrying.
- **`/universe/names` resolves ids in one shared space.** Group ids are not in
  it — resolving `group_id` there returns whatever inventory type shares the
  number. Use `Resolver.GroupName`.
- **The compatibility date is pinned** to `2026-08-18` in `config.go`. ESI
  moved from `/v1/` URLs to the `X-Compatibility-Date` header. Response shapes
  for everything used here match the 2020-01-01 baseline; the newer date only
  adds routes. Moving the pin means re-checking every response shape.
- **`/route/` is POST on recent compatibility dates**, with `preference` in the
  body, not a `flag` query parameter.

## Layout

```
cmd/eve-mcp/          binary: flags, launchd/systemd install
internal/
  config/             scopes, write-capability model, config.toml
  auth/               SSO PKCE, token storage and refresh
  esi/                HTTP client: caching, error limit, pagination
  cache/              SQLite: HTTP cache, names, blobs
  names/              id -> name resolution, reference prices
  safety/             the three write-guard layers
  app/                shared context, character/corp resolution
  server/             HTTP: /mcp, /auth, /setup, /health
  tools/              account, character, assets, wallet, industry,
                      market, social, universe, corp, writes
evals/                lint and smoke gates, agentic task definitions
eve_mcp/              previous Python implementation (reference only)
```
