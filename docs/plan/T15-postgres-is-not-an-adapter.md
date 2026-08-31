# T15 — Postgres is not an adapter; entities and contracts only

- Status: `todo`
- Size: M
- Depends on: T14
- RULES: §11 (domain and adapter), §12 (SQL is declared), §13 (mocks),
  §10 (one result), §16 (a constant lives where it is used)
- SPEC: §7 (Go layout) — this task edits it
- New in the 2026-08-31 audit; no §12 item

## Goal

RULES §11 is explicit: *"Postgres is not an entity. It is how a domain
repository is implemented. A package named `store` that talks to every
table is every domain's repository glued together — that is not an
adapter. ESI and SSO are adapters; the database is not."* And, one
sentence earlier: a domain package *"is first an entity (the type) and
the way to talk to it (the interface)"*.

Two things fall out of that, and this task does both.

**`adapter/store` goes away.** Most of the move already happened —
`characters`, `oauth_clients`, `login_states`, `auth_codes` and
`confirm_tokens` live in `domain/*/pgx`. After T12 and T14 the package
holds a connection pool, a test helper and one table.

**`domain/` stops holding things that are not entities.** `domain/j` is
JSON helpers imported across `usecase` and `adapter` alike;
`domain/universe` is two constants read by one file. Neither is an
entity anybody talks to, so neither is a domain. `domain/write` stays:
its `Guard` is the write policy, which is business we own, and it talks
to Postgres through its own `Persist` interface rather than a
repository — that is the shape §11 describes, not a violation of it.

**`adapter/esi` stops holding an implementation.** `resolver.go` is 488
lines of batching, static blobs and prices with its own caches, and
`namecache.go` is a 50 000-entry LRU. Both sit in the package that §11
reserves for the entity and the contract, while `http/` is where an
implementation of that contract lives. They move down; the interface
they satisfy stays up.

## Why this is one Composer session

It is a package move, one new domain, and an import rewrite. No
behaviour changes and no schema changes. Doing it before T17 means the
session work writes `domain/session/pgx` into a tree that already has
the right shape, instead of adding to a package that is on its way out.

## Do not

- Create `internal/postgres/queries.go`, a `Repositories` struct, or any
  other package that knows more than one table. That is `store` with a
  new name.
- Keep a `DB` type that repositories hang methods off. A repository is a
  type in its domain's `pgx` package holding a `*pgxpool.Pool`, which is
  what the five existing ones already do.
- Let `internal/domain/*` (the entity + contract level) import the
  Postgres package. Only the nested `pgx` implementation touches pgx,
  and it takes a `*pgxpool.Pool`.
- Move `adapter/esi` or `adapter/sso` out of `adapter/`. They model
  systems we do not own; they are adapters and they stay.
- Split `Resolver` while moving it, or change what it does. It goes down
  one level as it is; T28 owns its shape.
- Untangle `adapter/sso` here. It carries a `character.Repository` and
  half a repository interface, which is a §11 problem of its own — T17
  owns it, because that is where the grant moves.
- Move `domain/write` to `usecase`. The capability catalog and the
  confirm cycle are business rules we own, and the `Guard` is how you
  talk to them. It is a domain without a table, which §11 permits: not
  every entity we own is a row.
- Turn `domain/universe`'s two constants into a new package somewhere
  else. They belong to the one file that reads them (RULES §16:
  constants live in the package that owns them).
- Leave a query as a string literal at a call site. Every one is a
  declared `const` (RULES §12) — `guard.go`'s two are the last.
- Pass a god object into `session.Options`. A constructor takes the
  repositories it uses, named.

## Context

After T12 (`app_secrets` gone) and T14 (`users` gone), the package is:

| File | What is left |
|---|---|
| `store.go` | pool open / ping / `Pool()` / `Close` |
| `errors.go`, `types.go`, `wrap.go` | package plumbing |
| `guard.go` | `mail_log`: `CountMailSince`, `InsertMail` — two inline queries |
| `testdb.go`, `storetest/` | test helpers: advisory lock, truncate, goose apply, throwaway database |

`mail_log` is the audit log wearing the wrong name: DB.md makes
`mutations` both the audit trail and the source of the mail cap. So it
becomes `domain/mutation` here, keeping the `mail_log` table for now,
and T19 swaps the table underneath it. That is not throwaway work — it
is the final home receiving its final table later.

`internal/usecase/session/persist.go` maps `write.Persist` onto the
store today. Its `GetConfirm` returns `(*write.Confirm, bool, error)` —
three values, a RULES §10 violation. Fix it while the file is open: the
`bool` collapses into the pointer or into a sentinel error.

The two non-entities have exactly one destination each.
`domain/universe` (`TheForgeRegionID`, `Jita44StationID`) is imported by
`internal/usecase/eve/market.go` and nothing else, so the constants go
there, next to the market defaults SPEC §4.2 states. `domain/j` is
imported by fifteen files across `usecase` and `adapter`, which is what
a helper package looks like — it moves to `internal/j`, above the layer
split rather than inside one.

No cross-domain import exists today: only each `x/pgx` importing its own
`x`. `depguard`'s `domain-boundaries` rule is supposed to enforce that,
but its `files` mask is `**/internal/domain/*/*.go` — one path segment,
so it covers `domain/character/character.go` and not
`domain/character/pgx/repo.go`. The rule is weaker than it reads. Widen
the mask to `**/internal/domain/**/*.go` here: nothing violates it
today, so it costs one line and closes the hole before the pgx layer
grows two more packages in T17 and T19.

## Work

1. `internal/adapter/store` → `internal/postgres`: `Open(ctx, dsn,
   logger) (*DB, error)`, `Pool()`, `Close()`, `ErrEmptyDatabaseURL`.
   Nothing table-shaped survives the move.
2. `internal/adapter/store/storetest` → `internal/postgres/pgtest`, and
   `testdb.go` with it — it is test-only and has no business in a
   production package.
3. `mail_log` → `internal/domain/mutation` (entity + `Repository`) and
   `internal/domain/mutation/pgx` (implementation, still on `mail_log`).
   Every query a declared `const`.
4. `usecase/session/persist.go` maps `write.Persist` onto the confirm
   and mutation repositories, and `GetConfirm` returns one result plus
   `error`.
5. `session.Options` and `oauth.Open` take the repositories they use;
   nothing takes a handle to "the database" in order to reach a table.
6. Generate the mock for `mutation.Repository` into `internal/mocks`,
   and regenerate `write.Persist`'s, whose `GetConfirm` just changed
   shape (RULES §13, T11's home). `make generate` leaves a clean tree.
7. `internal/domain/j` → `internal/j`; `internal/domain/universe`'s two
   constants → `internal/usecase/eve`, and the package is deleted.
8. `resolver.go` and `namecache.go` → `internal/adapter/esi/http`,
   unchanged. `adapter/esi` keeps `esi.go`, `names.go` and the interface
   the resolver satisfies.
9. Widen `depguard`'s `domain-boundaries` mask to
   `**/internal/domain/**/*.go`.
10. Update SPEC §7's layout block in the same commit — it still lists
    `adapter/store/`, `domain/universe/`, `domain/j/`, and names the
    resolver as part of the ESI contract. DB.md does not change: no
    table moves.
11. Delete `internal/adapter/store`.

## Files

- Add: `internal/postgres/*`, `internal/postgres/pgtest/*`,
  `internal/domain/mutation/*`, `internal/domain/mutation/pgx/*`,
  `internal/j/*`
- Move: `internal/adapter/esi/{resolver.go,namecache.go}` →
  `internal/adapter/esi/http/`
- Edit: `cmd/eve-mcp/main.go`, `internal/usecase/session/*.go`,
  `internal/usecase/oauth/oauth.go`, `internal/usecase/eve/market.go`,
  every file importing `domain/j`, everything importing `storetest` —
  the domain `pgx` tests and T13's `tests/` harness alike —
  `Makefile` (the `test-store` package list), `.golangci.yml`,
  `docs/SPEC.md` §7
- Delete: `internal/adapter/store/`, `internal/domain/j/`,
  `internal/domain/universe/`

## Acceptance

- [ ] `ls internal/adapter` prints exactly `esi` and `sso`
- [ ] `rg -n 'adapter/store|storetest'` finds nothing
- [ ] Every package under `internal/domain/` is an entity with a
      `Repository`, plus `write`, which is an entity with a `Guard`
- [ ] `domain/j` and `domain/universe` are gone; nothing imports them
- [ ] `adapter/esi` holds only types and interfaces; the resolver and
      the name cache are in `adapter/esi/http`
- [ ] `depguard`'s `domain-boundaries` mask covers the `pgx` layer, and
      the tree is green under it
- [ ] No package outside a `pgx` implementation imports `jackc/pgx`,
      except `internal/postgres` and `internal/postgres/pgtest`
- [ ] No `internal/domain/*/*.go` (the contract level) imports pgx
- [ ] No domain imports another domain, and `depguard` needs no new
      exception to say so
- [ ] Every SQL statement in the tree is a declared `const`
- [ ] `GetConfirm` returns one value plus `error`
- [ ] SPEC §7 describes the tree that exists
- [ ] `go test ./...`, `make test-store` and `make lint` pass

## Verify

```bash
ls internal/adapter internal/domain
rg -n 'adapter/store|storetest|domain/j"|domain/universe' --glob '*.go' --glob 'Makefile'
rg -n '(Exec|Query|QueryRow)\(ctx, [`"]' --glob '*.go' -g '!*_test.go'
go test ./... -count=1 && make test-store && make lint
```

## Done

Set `Status: done` here and in [README.md](README.md).
