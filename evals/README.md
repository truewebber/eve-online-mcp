# Evals

Three levels, cheapest first.

## 1. `lint` — tool definition quality

```bash
go run ./evals lint
```

Deterministic; no model. Catches the things that are easy to break
when editing a tool and impossible to notice by eye:

- every tool is namespaced under `eve_`;
- **every parameter has a `description` in JSON Schema**, not only in prose;
- the tool description is at least 120 characters (otherwise it says
  what it is, not when to call it);
- docstring indentation has not leaked into the description;
- numeric tunables declare bounds;
- list tools expose `response_format`.

Exceptions live in `noResponseFormatNeeded` in `evals/main.go`, with a
comment explaining why.

Needs a running server. `/mcp` is an OAuth resource, so pass a bearer
token with `--token` or `EVE_MCP_TOKEN`.

```bash
go run ./evals lint --url http://127.0.0.1:8765/mcp --token "$EVE_MCP_TOKEN"
```

`go build -o eve-eval ./evals` compiles a binary (plain `go build ./evals` cannot: the output name collides with this directory).

## 2. `smoke` — health and token cost

```bash
go run ./evals smoke --token "$EVE_MCP_TOKEN"
```

Calls every read tool with its default arguments, checks the answer is
valid JSON without `error`, and prints the cost in characters / tokens.
Fails if a default response exceeds 6 000 characters: a default that
dumps a sheet is a bug even when the data is right.

Mutating tools are not in smoke.

## 3. `tasks.yaml` — agentic tasks

This is what the two cheaper gates cannot catch: the model picked the
wrong tool, or presented a number as live when it is an hour old.

There is no automatic runner — a model has to be in the loop. Connect
the server (`claude mcp add --transport http eve http://localhost:8765/mcp`),
give it the task `prompt`, and grade against `expect`.

Key tasks:

| id | What it checks |
|---|---|
| `misspelled` | A typo in a name — the model searches instead of giving up |
| `write_consent` | Preview shown and confirmation asked **before** the action |
| `write_refusal` | A disabled capability is not invented |
| `alpha_cap` | The trained/active level gap is noticed |
| `stale_awareness` | Stale cache is not presented as real time |
| `corp_hangar` | Corp hangar and wallets, not the personal tools |

### What evals have found

- `eve_social_killmails` returned 8 fields per row with no concise mode — `lint`.
- **ESI search is prefix, not fuzzy.** "Tritanum" did not find "Tritanium"
  because the typo is in the middle. Task `misspelled` failed. Fixed by
  shortening the prefix automatically and tagging `matched_on_prefix`.

The second bug would not have been caught by any automatic gate — only
a real task.
