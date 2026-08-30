# T21 — New tools and previews: calendar, compose, CSPA, enums, NPC corp

- Status: `todo`
- Size: L
- Depends on: T14, T17
- SPEC: §4, §4.1, §4.2, §12.11; TOOLS.md; ESI.md

## Goal

Five behaviour changes that close gaps between what PRD promises and what
the tool surface can do. Each one is small; they are together because
they all land in `usecase/eve` and all four ESI rows they need are
already written down.

1. **`eve_calendar_list`** — `GET /characters/{id}/calendar` with the
   `from_event` cursor, plus `detail` and `attendees` flags. Without it
   `eve_calendar_respond` is unreachable: it needs an `event_id` and
   there is nowhere in the conversation to get one.
2. **`eve_mail_compose`** — `POST /ui/openwindow/newmail`. Fills the
   client's compose window and leaves Send to the player: no CSPA charge,
   no hourly cap, nothing irreversible. It is the safe half of mail and it
   takes pressure off the most dangerous tool on the server.
3. **CSPA pricing in `eve_mail_send`'s preview** —
   `POST /characters/{id}/cspa` prices the charge for these exact
   recipients, so the confirmation names what CCP will bill instead of
   echoing what the model guessed.
4. **Enum validation** — `eve_ui_open_window` with an unrecognised
   `window` currently falls through to `information` and silently opens
   Show Info. That is a mutation doing something its own preview never
   described.
5. **NPC corporations** — `eve_corp_overview` says so and returns an
   empty `available_tools`, which is the only thing that stops a model
   trying the other twelve and spending 403s from the character's error
   budget.

## Why this is one Composer session

One package, one contract file, and the CSPA change only makes sense
alongside the compose tool it competes with — a reviewer wants to see both
descriptions at once.

## Do not

- Add `newmail` as a fourth `window` value on `eve_ui_open_window`. Its
  body needs recipients, a subject and a body, none of which fit
  `target`; it gets its own tool with its own preview (TOOLS.md).
- Fetch `detail` or `attendees` by default in `eve_calendar_list`. Each
  is one request per event.
- Let `eve_mail_send` proceed when the CSPA charge cannot be priced. No
  price, no preview, no token (SPEC §4.1) — assuming zero puts the
  player's ISK behind a confirmation that never mentioned it.
- Assume a CSPA charge of zero when the endpoint answers an empty body.
  Read the documented `201` shape.
- Skip the confirm cycle for `eve_mail_compose`. It changes the player's
  client, so it is a mutation like the rest, recorded in `mutations`
  under the `openwindow` capability.
- Promise delivery from `eve_mail_compose`. Report that the window was
  requested; whether a client is logged in is unknowable from here.
- Invent scopes. All four endpoints are covered by scopes already
  requested: calendar read, `read_contacts` for CSPA, `open_window` for
  compose.

## Context

ESI.md already carries the four rows, with the CSPA one deliberately in
the character-reads section because it changes nothing in game.

`eve_mail_compose` recipients: ESI splits them — `recipients` is an array
of character ids, and one mailing group goes in `to_corp_or_alliance_id`
or `to_mailing_list_id`. Hence the tool's `to` list plus a single
`to_group`, which is what TOOLS.md documents.

CSPA takes up to 100 recipient ids and is a POST with a read scope, so it
runs inside the preview with no confirm cycle of its own.

`eve_calendar_list` returns up to 50 events per call, newest-first from
now, and the cursor is `from_event`.

`internal/usecase/eve/writes.go` holds `resolveEntity`, which is where
the silent `information` fallback lives.

## Work

1. `eve_calendar_list` in `usecase/eve/social.go`, per TOOLS.md:
   `from_event`, `unanswered_only`, `detail`, `attendees`, `limit`,
   `response_format`; `next_cursor` in the result.
2. `eve_mail_compose` in `writes.go`: resolve names to ids, split
   characters from the single mailing group, confirm cycle, audit row
   under `openwindow`.
3. CSPA in `eve_mail_send`'s preview: price after resolving recipients,
   state the charge, refuse before consent when it exceeds
   `approved_cost`, fail the preview when it cannot be priced.
4. Validate `window` against its three values; refuse with the list.
   Audit the other enumerated parameters in the package while here
   (`kind`, `preference`, `response`, `response_format`) and give each
   the same treatment.
5. `eve_corp_overview`: detect the NPC corporation and answer with an
   empty `available_tools` and a plain sentence.
6. Tests against T11's fixtures: the calendar cursor round-trips;
   `attendees` costs one request per event and only when asked; compose
   sends the right body shape and never a mail; a priced charge above
   `approved_cost` refuses with no token minted; a failed CSPA read
   mints no token; `window="newmail"` is an error naming the three
   values; an NPC-corp overview lists no tools.

## Files

- Edit: `internal/usecase/eve/social.go`, `internal/usecase/eve/writes.go`,
  `internal/usecase/eve/corp.go`, `internal/usecase/eve/universe.go`,
  `internal/domain/write/policy.go` (capability summary wording)

## Acceptance

- [ ] `tools/list` has 52 tools, including `eve_calendar_list` and
      `eve_mail_compose`
- [ ] `eve_calendar_respond` is reachable end to end from a list call
- [ ] `eve_mail_send`'s preview states a priced charge and refuses above
      `approved_cost` before consent
- [ ] A preview that cannot price mints no token
- [ ] Every enumerated parameter refuses unknown values; nothing falls
      through to a default branch
- [ ] `eve_corp_overview` answers NPC corporations without inviting
      twelve 403s
- [ ] `eve_mail_compose` is recorded in `mutations` and never sends
- [ ] `go test ./...` passes

## Verify

```bash
go test ./internal/usecase/eve -count=1
go run ./evals lint
rg -n 'openwindow/newmail|/cspa|/calendar' internal/usecase/eve
```

## Done

Set `Status: done` here and in [README.md](README.md).
