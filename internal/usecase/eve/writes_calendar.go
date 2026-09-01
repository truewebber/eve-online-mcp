package eve

import (
	"context"

	"github.com/truewebber/eve-online-mcp/internal/domain/write"
	"github.com/truewebber/eve-online-mcp/internal/j"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type calendarRespondIn struct {
	EventID      int    `json:"event_id"                jsonschema:"Event id from eve_calendar_list."`
	Response     string `json:"response"                jsonschema:"accepted, declined, or tentative."`
	ConfirmToken string `json:"confirm_token,omitempty" jsonschema:"Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here."`
}

func registerCalendar(s *mcp.Server) {
	addTool(s, &mcp.Tool{
		Name:        write.ToolCalendarRespond,
		Description: "Respond to a calendar event invitation on this character.\n\nThe organiser and other invitees see accepted, declined or tentative in-game. This only RSVPs; it does not create, edit or delete events. Confirm before sending an answer the player will have to live with.",
	}, sessionTool(eveCalendarRespond))
}

func eveCalendarRespond(ctx context.Context, a *session.Session, in calendarRespondIn) (any, error) {
	response, err := requireEnum(fResponse, in.Response, vAccepted, vDeclined, vTentative)
	if err != nil {
		return nil, err
	}
	token, err := a.Character(ctx)
	if err != nil {
		return nil, wrap("eveCalendarRespond", err)
	}
	detail, err := a.ESI.Get(ctx, esiPath("characters", esiID(token.CharacterID), "calendar", esiID(in.EventID)), &token.CharacterID, nil, nil)
	if err != nil {
		return nil, wrap("eveCalendarRespond", err)
	}
	event := j.Map(detail.Data)
	args := map[string]any{fEventID: in.EventID, fResponse: response, fCharacterID: token.CharacterID}
	preview := map[string]any{
		fAction:    "Respond to a calendar invitation — the organiser is notified",
		fCharacter: token.CharacterName, "event": event[fTitle], fDate: event[fDate],
		"owner": event["owner_name"], fResponse: response,
	}
	blocked, err := a.Guard.Authorize(ctx, write.Authz{
		Tool: write.ToolCalendarRespond, Capability: write.CapCalendar,
		Args: args, Preview: preview, Token: in.ConfirmToken, Scopes: token.Scopes,
	})
	if err != nil {
		return nil, wrap("eveCalendarRespond", err)
	}
	if blocked.Required != nil {
		return blocked.Required, nil
	}
	_, err = a.ESI.Put(ctx, esiPath("characters", esiID(token.CharacterID), "calendar", esiID(in.EventID)), &token.CharacterID, nil, map[string]any{fResponse: response})
	recordWrite(ctx, a, writeLog{tool: write.ToolCalendarRespond, capability: write.CapCalendar, args: args, err: err})
	if err != nil {
		return nil, wrap("eveCalendarRespond", err)
	}

	return map[string]any{fStatus: vDone, "event": event[fTitle], fResponse: response}, nil
}
