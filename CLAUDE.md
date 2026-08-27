# eve_online

A containerised MCP server that exposes one player's EVE Online account to an
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

Nothing runs on the host — the host Python is 3.9 and has no dependencies
installed. Everything goes through Docker:

```bash
docker compose up -d --build     # rebuild and restart after any code change
docker compose logs -f           # logs
python3 evals/run.py all         # quality gates (host Python is fine for this)
```

The server must be rebuilt for changes to take effect; there is no reload.

**`docker compose down -v` destroys the `eve-data` volume, which holds the
user's SSO refresh tokens.** They would have to re-authorize every character in
a browser. Use plain `down` unless the user explicitly asks to wipe auth.

## Invariants

Break these and the server regresses in ways tests will not obviously catch.

**Tool definitions.** `evals/run.py lint` enforces most of this; run it after
touching anything in `eve_mcp/tools/`.

- Every parameter needs `Annotated[T, Field(description=...)]`. Docstring
  `Args:` blocks do not reach the JSON Schema, which is what the model reads.
- Numeric tunables need `ge`/`le` bounds. Game ids do not — they are opaque.
- Any tool returning a list needs a small default `limit` and a
  `response_format` of `"concise" | "detailed"`. Concise is the default.
- Error strings say what to do next, naming the tool that fixes it. Never
  return a bare status code.
- Names follow `eve_<domain>_<action>`. Renaming a tool is a breaking change
  for anyone who wired it up.

**Never add typed output schemas.** `TypedDict` returns look tempting but the
MCP SDK silently drops undeclared keys from the response and hard-fails on a
type mismatch. Verified, not theoretical. Response shape is documented in each
docstring's `Returns:` line instead.

**Every mutating tool goes through `ctx.guard.authorize()`** before it touches
ESI, and `ctx.guard.record()` after. That is what enforces the capability gate,
the confirm-token cycle, the hourly budget and the audit log. A write path that
skips it bypasses all four. Write tools are also registered only when their
capability is enabled, so the tool list is itself a security boundary.

**All ESI traffic goes through `EsiClient`.** It carries the identifying
User-Agent, the pinned compatibility date, ETag caching and error-limit
backoff. CCP bans clients that bypass caching or ignore the error limit. Never
call ESI with a bare `httpx` request.

## Things that will surprise you

- **ESI search matches on prefix, not fuzzily.** `Tritanum` finds nothing.
  `eve_universe_search` compensates by shortening the prefix and retrying.
- **`/universe/names` resolves ids in one shared space.** Group ids are not in
  it — resolving `group_id` there returns whatever inventory type shares the
  number. Use `Resolver.group_name`.
- **The compatibility date is pinned** to `2026-08-18` in `config.py`. ESI
  moved from `/v1/` URLs to the `X-Compatibility-Date` header. Response shapes
  for everything used here match the 2020-01-01 baseline; the newer date only
  adds routes. Moving the pin means re-checking every response shape.
- **`/route/` is POST on recent compatibility dates**, with `preference` in the
  body, not a `flag` query parameter.

## Layout

```
eve_mcp/
  config.py   scopes, write-capability model, settings
  schema.py   shared parameter annotations
  auth.py     SSO PKCE, token storage and refresh
  esi.py      HTTP client: caching, error limit, pagination
  cache.py    SQLite: HTTP cache, names, blobs
  names.py    id -> name resolution, reference prices
  safety.py   the three write-guard layers
  server.py   MCP server plus the login web routes
  tools/      account, character, assets, wallet, industry,
              market, social, universe, writes
evals/        lint and smoke gates, agentic task definitions
```
