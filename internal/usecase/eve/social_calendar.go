package eve

import (
	"context"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/j"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"
)

type calendarListIn struct {
	FromEvent      int    `json:"from_event,omitempty"      jsonschema:"Continue after this event id — pass back the next_cursor from a previous call. Empty starts from now."`
	UnansweredOnly *bool  `json:"unanswered_only,omitempty" jsonschema:"Only events this character has not responded to yet."`
	Detail         *bool  `json:"detail,omitempty"          jsonschema:"Fetch each listed event's full record: organiser, duration and description text. One extra request per event, so use it for the one event the user asked about, not for a whole month."`
	Attendees      *bool  `json:"attendees,omitempty"       jsonschema:"Also fetch who accepted, declined or has not answered. One extra request per event, same warning as detail."`
	Limit          int    `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

func eveCalendarList(ctx context.Context, a *session.Session, in calendarListIn) (any, error) {
	if err := rejectUnknownFormat(in.ResponseFormat); err != nil {
		return nil, err
	}
	token, err := a.Character(ctx)
	if err != nil {
		return nil, wrap("eveCalendarList", err)
	}
	if err := a.RequireScope(token, "esi-calendar.read_calendar_events.v1", "calendar"); err != nil {
		return nil, wrap("eveCalendarList", err)
	}
	listed, err := fetchCalendarPage(ctx, a, token.CharacterID, in.FromEvent)
	if err != nil {
		return nil, err
	}
	events := listed.events
	if boolDef(in.UnansweredOnly, false) {
		events = unansweredCalendar(events)
	}
	ages := []float64{listed.age}
	if boolDef(in.Detail, false) {
		attached, err := attachCalendarDetail(ctx, a, calendarAttach{characterID: token.CharacterID, events: events, ages: ages})
		if err != nil {
			return nil, err
		}
		events, ages = attached.events, attached.ages
	}
	if boolDef(in.Attendees, false) {
		attached, err := attachCalendarAttendees(ctx, a, calendarAttach{characterID: token.CharacterID, events: events, ages: ages})
		if err != nil {
			return nil, err
		}
		events, ages = attached.events, attached.ages
	}
	paged := pageByCursor(cursorPageIn{Shown: events, Limit: limitOr(in.Limit, limitDefault), Key: fEventID, Hint: "Pass next_cursor as from_event.", ESI: listed.events})
	keep := []string{fEventID, fTitle, fEventDate, fResponse, "importance"}

	return merge(map[string]any{
		fCharacter: token.CharacterName, fDataAge: esi.Result{AgeSeconds: oldestAge(ages)}.StaleNote(),
		fEvents: project(paged.Rows, keep, concise(in.ResponseFormat)),
	}, paged.fields), nil
}

type calendarPage struct {
	events []map[string]any
	age    float64
}

func fetchCalendarPage(ctx context.Context, a *session.Session, characterID, fromEvent int) (calendarPage, error) {
	params := map[string]any{}
	if fromEvent > 0 {
		params[fFromEvent] = fromEvent
	}
	result, err := a.ESI.Get(ctx, esiPath("characters", esiID(characterID), "calendar"), &characterID, params, nil)
	if err != nil {
		return calendarPage{}, wrap("fetchCalendarPage", err)
	}
	var events []map[string]any
	for _, raw := range j.Maps(result.Data) {
		events = append(events, map[string]any{
			fEventID: raw[fEventID], fTitle: raw[fTitle], fEventDate: raw[fEventDate],
			fResponse: raw["event_response"], "importance": raw["importance"],
		})
	}

	return calendarPage{events: events, age: result.AgeSeconds}, nil
}

func unansweredCalendar(events []map[string]any) []map[string]any {
	var out []map[string]any
	for _, e := range events {
		if j.Str(e[fResponse]) == vNotResponded {
			out = append(out, e)
		}
	}

	return out
}

type calendarAttach struct {
	characterID int
	events      []map[string]any
	ages        []float64
}

type calendarEvents struct {
	events []map[string]any
	ages   []float64
}

func attachCalendarDetail(ctx context.Context, a *session.Session, in calendarAttach) (calendarEvents, error) {
	events, ages := in.events, in.ages
	for i, e := range events {
		id := j.Int(e[fEventID])
		result, err := a.ESI.Get(ctx, esiPath("characters", esiID(in.characterID), "calendar", esiID(id)), &in.characterID, nil, nil)
		if err != nil {
			return calendarEvents{}, wrap("attachCalendarDetail", err)
		}
		ages = append(ages, result.AgeSeconds)
		detail := j.Map(result.Data)
		e["organiser"] = detail["owner_name"]
		e["duration"] = detail["duration"]
		e[fDescription] = stripMarkup(j.Str(detail["text"]))
		events[i] = e
	}

	return calendarEvents{events: events, ages: ages}, nil
}

func attachCalendarAttendees(ctx context.Context, a *session.Session, in calendarAttach) (calendarEvents, error) {
	events, ages := in.events, in.ages
	for i, e := range events {
		id := j.Int(e[fEventID])
		result, err := a.ESI.Get(ctx, esiPath("characters", esiID(in.characterID), "calendar", esiID(id), "attendees"), &in.characterID, nil, nil)
		if err != nil {
			return calendarEvents{}, wrap("attachCalendarAttendees", err)
		}
		ages = append(ages, result.AgeSeconds)
		rows := j.Maps(result.Data)
		ids := map[int]struct{}{}
		for _, row := range rows {
			ids[j.Int(row[fCharacterID])] = struct{}{}
		}
		names, err := a.Resolver.Names(ctx, setToList(ids), nil)
		if err != nil {
			return calendarEvents{}, wrap("attachCalendarAttendees", err)
		}
		var attendees []map[string]any
		for _, row := range rows {
			cid := j.Int(row[fCharacterID])
			attendees = append(attendees, map[string]any{
				fName: nameOr(names, cid), fResponse: row["event_response"],
			})
		}
		e["attendees"] = attendees
		events[i] = e
	}

	return calendarEvents{events: events, ages: ages}, nil
}
