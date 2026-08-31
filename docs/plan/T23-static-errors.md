# T23 — The user sees only static errors

- Status: `todo`
- Size: M
- Depends on: T18, T22
- RULES: §9 (the user sees only static errors), §7 (only gopkg/log
  writes to std), §5 (tests)
- SPEC: §4 (error kinds), §6 (HTTP API); AUTH.md
- New in the 2026-08-31 audit; no §12 item

## Goal

RULES §9: *"The transport is the only place an error becomes a response.
It logs the internal error and returns a static message (and kind) from
a fixed catalog. Inner layers return real Go errors so the log can say
what broke; those strings never cross the edge — not in JSON, not in
HTML, not in a `Location`."*

Today the OAuth callback does the opposite. `GetAuthCallback` in
`internal/service/http/handler.go` renders `err.Error()` into the "Login
failed" page, and echoes CCP's `error_description` into the browser. The
first hands a stranger the shape of our internals — a pgx message, a
JWT parse failure, a URL we tried to reach. The second renders a string
an upstream chose, on our origin, from a route anybody can call.

## Why this is one Composer session

One catalogue, three edges — the human pages, the OAuth JSON errors and
the MCP tool results — and the same test in each: nothing the user
receives contains a substring of a Go error.

It lands after T18, which adds the missing-scopes page (the one page
with a legitimate dynamic part, and it is ours), and after T22, which
finishes moving the listener code.

## Do not

- Add a `debug` mode that puts the real error back. There is one
  behaviour.
- Log through anything but `log.Logger`, and do not construct one at the
  edge — the handler has the field it was built with (RULES §7).
- Swallow the error. Static to the user, complete to the log: the log
  line carries the real error, the state or code involved, and the
  character where there is one.
- Render a string CCP sent us. `error` and `error_description` from EVE
  SSO are logged and mapped to a catalogue entry.
- Make the catalogue a `map[error]string` keyed by wrapped errors from
  four packages. It is a small closed set of cases the transport knows
  it can produce, matched with `errors.Is` / `errors.As` on sentinels
  the inner layers export.
- Turn a validation message dynamic. RULES §9 allows naming the field
  and the invariant, both static: `character_id` /
  `must be a positive integer`, never `strconv.ParseInt: …`.
- Drop the actionable sentence from a tool error. SPEC §4 requires it to
  name the next tool; those sentences are ours and static per case.

## Context

Three edges, three shapes:

| Edge | Today | Target |
|---|---|---|
| Human pages (`GetIndex`, `GetAuthCallback`) | `err.Error()` and CCP's `error_description` inlined into HTML | catalogue entry by case; real error at warn/error in the log |
| OAuth endpoints (`/oauth/*`) | RFC 6749 codes, mostly static already | audit that no `error_description` carries an inner error |
| MCP tool results | `usecase/session/errors.go` maps to SPEC §4 kinds | the kind stays in usecase; the sentence is the transport's, and no inner string reaches it |

`internal/service/http/pages.go` renders the pages. `errors.go` in
`usecase/session` produces the kinds and the rate-limit fields, which
are data and stay.

One case to decide deliberately and record in the task output: a name
the player asked for that resolved to nothing (SPEC §4 makes it an
`Error` pointing at `eve_universe_search`). Echoing the caller's own
input back is not leaking an internal error, so it is allowed — but it
is echoed as data in a field, not spliced into the sentence.

## Work

1. A catalogue in `service/http`: one entry per reachable case — unknown
   or expired login state, login refused at CCP, PKCE or client
   mismatch, missing scopes (T18's page), database unavailable, and a
   generic entry for everything else. Each entry is a title, a static
   sentence, and a status.
2. `GetAuthCallback` and `GetIndex` resolve an error to an entry with
   `errors.Is` / `errors.As` on sentinels the inner layers export, log
   the real error with context, and render the entry.
3. Audit the OAuth JSON errors for the same leak.
4. The MCP edge: assert the mapped result carries the kind, the
   sentence and the documented extra fields, and no substring of the
   underlying error.
5. Tests: a forced database failure at the callback renders the generic
   page and logs the real error, asserted through the generated
   `log.Logger` mock; a CCP `error_description` of
   `<script>alert(1)</script>` appears in no response; every catalogue
   entry is reachable from a real code path; a tool error contains no
   substring of the error that caused it.

## Files

- Add: `internal/service/http/errors.go` (the catalogue) and its test
- Edit: `internal/service/http/handler.go`,
  `internal/service/http/pages.go`, `internal/usecase/session/errors.go`,
  `internal/usecase/oauth/*.go` (export sentinels, stop formatting
  user-facing text), `internal/usecase/eve/common.go`

## Acceptance

- [ ] No response body, header or `Location` contains `err.Error()`
- [ ] No response contains a string received from CCP
- [ ] Every user-visible message comes from the catalogue, and every
      entry is reachable
- [ ] The real error is in the log for every catalogue entry rendered
- [ ] Validation messages name a static field and a static invariant
- [ ] Tool errors keep their SPEC §4 kind, sentence and extra fields
- [ ] `go test ./...` and `make lint` pass

## Verify

```bash
rg -n 'err\.Error\(\)|%v", err|%s", err' internal/service internal/usecase/eve
go test ./internal/service/http ./internal/usecase/session -count=1
```

## Done

Set `Status: done` here and in [README.md](README.md).
