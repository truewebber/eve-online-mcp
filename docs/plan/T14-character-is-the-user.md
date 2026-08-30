# T14 — The character is the user; drop `users`

- Status: `todo`
- Size: L
- Depends on: T12
- SPEC: §3.3, §4 (no `character` parameter), §12.1

## Goal

There is no user. The identity is CCP's `character_id`, JWT `sub`
carries it, and a connection reads and acts as exactly one character.
That kills, in one change: the `users` table, `domain/user`, the alt-add
flow, tool-started EVE logins, and the `character` parameter on every
tool.

After this task a connection is single-character by construction, so
"which character did you mean" stops being a concept the code can
express — which is the point. The EVE grant still lives where it lives
today; moving it onto sessions is T15.

## Why this is one Composer session

It is one idea with a wide mechanical tail. Splitting it leaves the tree
in a state where some tools take a `character` and some do not, and the
JWT means different things in different files — worse than one large
session.

## Do not

- Add a `sessions` table, a `sid` claim or a one-live-session rule.
  That is T15 and it depends on this landing first.
- Keep `eve_auth_login_url`, `StartAltLogin`, `login_states.kind` or
  `SSOForState` "for later". No tool may mint a login URL (SPEC §3.5);
  the browser flow is the only path in.
- Leave a `character` parameter anywhere, including optional and
  ignored. TOOLS.md's Conventions make its absence a contract.
- Keep `CharacterNotFound` as an error kind. Nothing takes a character,
  so nothing can fail to find one; an unresolvable *name* is `Error`
  pointing at `eve_universe_search` (SPEC §4).
- Write a migration that carries `users` rows forward. Players
  re-authenticate once.

## Context

Today `sub` is an invented user id, `characters.user_id` groups a pile of
alts under it, `store.OwnerOf` answers who owns a character, and
`session.ForUser(userID)` clones a session that can then pick a
character with `ResolveCharacter(spec)`. Tools take a `character`
argument and pass it through that resolver. `store.CountConfirmTokens`
and the mail cap are keyed by user.

Target: `sub` is the character id; `session.ForCharacter(characterID)`
resolves exactly one token; `ResolveCharacter` and every call site of it
disappear. `characters` keeps identity only — `character_id`, `name`,
`owner_hash`, `created_at`, `deleted_at` (DB.md).

`owner_hash` keeps its meaning: a change at login means the character was
sold, previous access is revoked and the row is re-owned. Whoever can log
into the EVE account is the owner.

## Work

1. Migration: drop `users`; drop `characters.user_id` and its index; add
   `deleted_at` if it is not there yet.
2. Delete `internal/domain/user`, `internal/adapter/store/users.go`, and
   the userID parameter from every `characters.go` / `guard.go` method.
3. `usecase/oauth`: `sub` = character id at both grants; delete
   `ownerOf`; the callback upserts the character and parks the grant
   against `character_id`.
4. `usecase/session`: `ForUser` → `ForCharacter`; delete
   `StartAltLogin`, `ResolveCharacter`, and the `spec` plumbing in
   `ResolveCorporation` if it takes one.
5. Every `addTool` in `usecase/eve/*.go`: remove the `Character` input
   field and the `ResolveCharacter` call; `eve_auth_status` reports the
   one character; `eve_auth_logout` takes no arguments; delete
   `eve_auth_login_url` and the T10 refuse path.
6. `login_states`: drop `kind` and the alt-login branch.
7. Delete `CharacterNotFound` from `usecase/session/errors.go` and remap
   any name-resolution failure to `Error`.
8. Tests: JWT `sub` round-trips a character id; a tool called with an
   extra `character` argument does not silently use it; logout
   soft-deletes and a later login revives the row with the same `sub`;
   an `owner_hash` change at login revokes prior access.

## Files

- Edit: `internal/usecase/oauth/oauth.go`, `internal/usecase/session/*.go`,
  `internal/usecase/eve/*.go`, `internal/adapter/store/characters.go`,
  `internal/adapter/store/guard.go`, `internal/adapter/store/oauth.go`,
  `internal/adapter/sso/*.go`, `cmd/eve-mcp/main.go`
- Delete: `internal/domain/user/`, `internal/adapter/store/users.go`
- Add: one migration

## Acceptance

- [ ] `rg -n 'user_id|domain/user|ResolveCharacter|StartAltLogin|eve_auth_login_url'`
      finds nothing in `internal/` or `cmd/`
- [ ] No tool input struct has a character field, and no `character`
      parameter appears in `tools/list`. The count there is **50** after
      this task: 51 today minus `eve_auth_login_url`. TOOLS.md's 52 is
      reached in T21, which adds the two new tools
- [ ] `sub` is the character id in access and refresh tokens
- [ ] `eve_auth_status` describes one character; `eve_auth_logout` takes
      no arguments
- [ ] `CharacterNotFound` is gone
- [ ] `go test ./...` passes; a fresh database boots and one browser
      sign-in yields a working connection

## Verify

```bash
rg -n 'user_id|ResolveCharacter|StartAltLogin|CharacterNotFound' --glob '*.go'
go test ./... -count=1
go run ./evals lint
```

## Done

Set `Status: done` here and in [README.md](README.md).
