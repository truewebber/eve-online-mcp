# T10 — Character ownership on alt-add

- Status: `done`
- Size: S
- Depends on: T05, T06
- SPEC: §3.3, §12.3a

## Goal

`eve_auth_login_url` must not attach a character that already belongs
to a **different** user. Re-adding your own character refreshes the
token. MCP authorize (no existing session) still resolves to the
owning user via `character_id` (already T05/T06).

The error is an actionable sentence: log the character out on the
other user, or sign in from the client as that character.

## Why this is one Composer session

One callback path. Unique PK from T05 is the safety net; this task is
the user-visible refuse **before** a failed insert.

## Do not

- Merge users automatically.
- Change MCP-first login dedupe (`OwnerOf` → existing user) except to
  share the same helper.
- Store the character on the new user “anyway” if the DB would reject.

## Context

Today `CompleteLogin` upserts into whichever `sso.Client` owned the
pending state **before** `FinishEVE` runs (`handler.go`). Alt flow:
`FinishEVE` returns `""` because the state is not MCP pending, and the
token is already in the current user’s store. That is the bug.

T06 stores `login_states.kind` + `user_id` for alt. Use that:

1. Complete EVE SSO (code → character token) **without** writing the
   character, or write only after the ownership check.
2. If `kind=alt` and `OwnerOf(id)` is another user → do not upsert;
   HTML error + do not leave a half-row.
3. If `kind=alt` and owner is this user or nobody → upsert (refresh or
   add).
4. If `kind=mcp` → keep existing attach/dedupe.

Check **before** upsert. Unique PK is backup, not the UX.

## Work

1. Split “exchange code” from “persist character” in SSO or oauth.
2. Ownership check on alt completion; tests with a fake store or
   `adapter/store` + `DATABASE_URL`.
3. Error text names `eve_auth_logout` on the other session / signing
   in as that character from the client.

## Files

- Edit: `usecase/oauth/oauth.go`, `service/http/handler.go`,
  `adapter/sso/sso.go` as needed
- Test: `usecase/oauth/` or `adapter/store` ownership cases

## Acceptance

- [x] Test: character on user A, alt-add from user B → error, A still
      owns the row
- [x] Test: alt-add of own character → token updated, still one row
- [x] MCP login with a known character still maps to that user (`sub`)
- [x] No silent duplicate even if the application check is skipped
      (PK)

## Verify

```bash
go test ./internal/usecase/oauth ./internal/adapter/store -count=1
```

## Done

Set `Status: done` here and in [README.md](README.md).
