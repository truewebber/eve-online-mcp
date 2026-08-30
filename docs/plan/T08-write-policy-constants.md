# T08 — Always-on writes; drop budget + audit

- Status: `done`
- Size: M
- Depends on: T07
- SPEC: §2 (constants), §4, §12.1, §12.2

## Goal

The host is not a policy layer. All write tools and corp tools are
always registered. Login always requests the full read + corp + write
scope set. Confirm mode is the only mode (300 s). Mail cap stays 5/h.
No general write budget. No `audit.jsonl` and no `audit_log` in tool
output.

## Why this is one Composer session

One policy surface: `domain/write`, `register.go`, `writes.go`,
`account.go`, `config.go`, TOOLS.md copy. Mechanical but easy to miss
a field on `eve_auth_status`.

## Do not

- Weaken the confirm cycle (no `WRITE_MODE=on` execute-immediately).
- Make mail cap configurable.
- Skip `TOOLS.md` — it is normative; this commit must update it
  (banner at the top + `eve_auth_status` / `eve_auth_login_url`).
- Implement the ESI user bucket (T09).

## Env to delete

`WRITE_MODE`, `WRITE_ALLOW`, `WRITE_BUDGET_PER_HOUR`,
`MAIL_BUDGET_PER_HOUR`, `CONFIRM_TTL`, `CORP_SCOPES`, `COMPAT_DATE`.

Pin compatibility date as `defaultCompatDate` in `cmd/eve-mcp/config.go`
only (already there). Mail cap `5`, confirm TTL `300` as constants in
`domain/write` (not env).

## Work

1. `write.Options`: drop Mode/Allow/WriteBudget/AuditFile (or ignore
   them until deleted). `RequestedScopes` always returns
   `ReadScopes + CorpReadScopes + all write scopes`.
2. `registerWrites`: always register every mutating tool.
3. `eve.Register`: always `registerCorp`.
4. `Guard.CheckCapability`: unknown name only — no “not enabled on this
   server”.
5. `Guard`: delete write-budget slice; delete `auditLocked` / `AuditFile`.
   Keep mail cap + confirm.
6. `eve_auth_status`: drop `write_mode`, `disabled_capabilities`,
   `budgets.write_budget*`, `audit_log`. Keep characters, capability
   list (all of them), mail remaining, confirm explanation.
7. `service/mcp.Instructions` / `eve.Instructions`: remove “registered
   only when the operator enabled”; keep confirm ritual; mention mail
   cap; do not mention host allow-lists.
8. `cmd/eve-mcp`: delete `writeAllow()`, env fields, `Host.WriteMode` on
   the HTML status page (or replace with a fixed “writes: confirm”).
9. `evals/tasks.yaml`: `write_refusal` (disabled capability) is obsolete
   — rewrite or delete that task. Keep `write_consent`.
10. `.env.example` + `api/http.yaml` description: reduced env set (T11
    will polish README; still do `.env.example` here so nobody sets
    dead vars).

## Files

- Edit: `domain/write/policy.go`, `domain/write/guard.go`,
  `usecase/eve/register.go`, `usecase/eve/writes.go`,
  `usecase/eve/account.go`, `service/mcp/register.go`,
  `service/http/handler.go`, `service/http/listen.go`,
  `cmd/eve-mcp/*`, `docs/TOOLS.md`, `evals/tasks.yaml`,
  `.env.example`, `api/http.yaml` (info description only;
  `make gen` if you change YAML)

## Acceptance

- [x] Those env names are gone from `config.go` and `.env.example`
- [x] All mutating tools register even if someone has an old `.env`
      with `WRITE_ALLOW`
- [x] `eve_auth_status` description in TOOLS.md matches the new Returns
- [x] No `audit_log` in tool results
- [x] Confirm-without-token still previews; mail cap still 5
- [x] `go test ./...` and `go run ./evals lint` (with token) pass

## Verify

```bash
rg -n 'WRITE_MODE|WRITE_ALLOW|WRITE_BUDGET|audit_log|CapabilityEnabled' --glob '!docs/plan/**' --glob '!.git/**'
go test ./...
```

## Done

Set `Status: done` here and in [README.md](README.md).
