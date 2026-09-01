package eve

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/domain/write"
	"github.com/truewebber/eve-online-mcp/internal/j"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const clientCaveat = "Takes effect only while the EVE client is running and logged in on this character. With the client closed the call reports success and nothing visible happens."

func recipientType(category string) string {
	switch category {
	case fCharacters:
		return fCharacter
	case fCorporations:
		return fCorporation
	case fAlliances:
		return fAlliance
	default:
		return ""
	}
}

func registerWrites(s *mcp.Server) {
	registerWaypoint(s)
	registerOpenWindow(s)
	registerWriteFittings(s)
	registerMailOrganize(s)
	registerMailSend(s)
	registerContacts(s)
	registerCalendar(s)
}

func registerWaypoint(s *mcp.Server) {
	type in struct {
		Destination         string `json:"destination"                     jsonschema:"Exact system, station or structure name."`
		ClearOtherWaypoints *bool  `json:"clear_other_waypoints,omitempty" jsonschema:"True replaces the whole existing route. Default true."`
		AddToBeginning      *bool  `json:"add_to_beginning,omitempty"      jsonschema:"Insert as the very next hop rather than the final stop."`
		ConfirmToken        string `json:"confirm_token,omitempty"         jsonschema:"Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here."`
	}
	addTool(s, &mcp.Tool{
		Name:        "eve_ui_set_waypoint",
		Description: "Set an autopilot waypoint in the running game client.\n\nThis only moves the route marker on the map. It never undocks, flies or activates autopilot. Default clear_other_waypoints=true wipes a route the player may have spent time building.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in in) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *session.Session) (any, error) {
			token, err := a.Character(ctx)
			if err != nil {
				return nil, wrap("registerWaypoint", err)
			}
			target, err := resolveDestination(ctx, a, in.Destination, token.CharacterID)
			if err != nil {
				return nil, err
			}
			if _, ok := target[fError]; ok {
				return target, nil
			}
			clearOthers := boolDef(in.ClearOtherWaypoints, true)
			add := boolDef(in.AddToBeginning, false)
			args := map[string]any{
				"destination_id": target["id"], fCharacterID: token.CharacterID,
				"clear_other_waypoints": clearOthers, "add_to_beginning": add,
			}
			pos := "final stop"
			if add {
				pos = "next hop"
			}
			preview := map[string]any{
				fAction:                 "Set an autopilot waypoint in the game client",
				fCharacter:              token.CharacterName,
				"destination":           fmt.Sprintf("%v (%v)", target[fName], target[fKind]),
				"clears_existing_route": clearOthers, "position": pos,
			}
			if amb := j.Str(target["ambiguity"]); amb != "" {
				preview["ambiguous_name"] = amb + " — this routes to the first. Cancel and use eve_universe_search if the other one was meant."
			}
			blocked, err := a.Guard.Authorize(ctx, "eve_ui_set_waypoint", "waypoint", args, preview, in.ConfirmToken, token.Scopes)
			if err != nil {
				return nil, wrap("registerWaypoint", err)
			}
			if blocked.Required != nil {
				return blocked.Required, nil
			}
			_, err = a.ESI.Post(ctx, "/ui/autopilot/waypoint", &token.CharacterID, map[string]any{
				"destination_id": target["id"], "clear_other_waypoints": clearOthers, "add_to_beginning": add,
			}, nil)
			recordWrite(ctx, a, "eve_ui_set_waypoint", "waypoint", args, err)
			if err != nil {
				return nil, wrap("registerWaypoint", err)
			}

			return map[string]any{fStatus: vDone, "waypoint_set_to": target[fName], fNote: clientCaveat}, nil
		})
	})
}

func registerOpenWindow(s *mcp.Server) {
	type in struct {
		Window       string `json:"window"                  jsonschema:"'market' opens market details for an item. 'info' opens Show Info. 'contract' opens one contract."`
		Target       string `json:"target"                  jsonschema:"For market, an exact item name. For info, an exact name of any entity. For contract, the numeric contract_id."`
		ConfirmToken string `json:"confirm_token,omitempty" jsonschema:"Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here."`
	}
	addTool(s, &mcp.Tool{
		Name:        "eve_ui_open_window",
		Description: "Open a window in the running game client.\n\nGood for handing something off to the player. Changes nothing in game and costs nothing.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in in) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *session.Session) (any, error) {
			token, err := a.Character(ctx)
			if err != nil {
				return nil, wrap("registerOpenWindow", err)
			}
			kind := strings.ToLower(strings.TrimSpace(in.Window))
			plan, err := planOpenWindow(ctx, a, kind, in.Target, token.CharacterID)
			if err != nil {
				return nil, err
			}
			if plan.refuse != nil {
				return plan.refuse, nil
			}
			path, params, label, resolved := plan.path, plan.params, plan.label, plan.resolved
			args := map[string]any{"window": kind, "params": params, fCharacterID: token.CharacterID}
			preview := map[string]any{
				fAction:    fmt.Sprintf("Open the %s window in the game client", kind),
				fCharacter: token.CharacterName, "target": label,
			}
			if resolved != nil {
				if amb := j.Str(resolved["ambiguity"]); amb != "" {
					preview["ambiguous_name"] = amb + " — this opens the first. Cancel and use eve_universe_search if the other one was meant."
				}
			}
			blocked, err := a.Guard.Authorize(ctx, "eve_ui_open_window", "openwindow", args, preview, in.ConfirmToken, token.Scopes)
			if err != nil {
				return nil, wrap("registerOpenWindow", err)
			}
			if blocked.Required != nil {
				return blocked.Required, nil
			}
			_, err = a.ESI.Post(ctx, path, &token.CharacterID, params, nil)
			recordWrite(ctx, a, "eve_ui_open_window", "openwindow", args, err)
			if err != nil {
				return nil, wrap("registerOpenWindow", err)
			}

			return map[string]any{fStatus: vDone, "opened": label, fNote: clientCaveat}, nil
		})
	})
}

type fittingModule struct {
	Name     string `json:"name"               jsonschema:"Exact module name."`
	Flag     string `json:"flag,omitempty"     jsonschema:"HiSlot0-7, MedSlot0-7, LoSlot0-7, RigSlot0-2, SubSystemSlot0-4, DroneBay, FighterBay, Cargo."`
	Quantity int    `json:"quantity,omitempty" jsonschema:"Default 1."`
}

type fittingSaveIn struct {
	Name         string          `json:"name"                    jsonschema:"Fitting name as it will appear in game."`
	Ship         string          `json:"ship"                    jsonschema:"Exact hull name, e.g. 'Rifter'."`
	Modules      []fittingModule `json:"modules"                 jsonschema:"Modules as objects with name, flag, quantity."`
	Description  string          `json:"description,omitempty"   jsonschema:"Optional note stored with the fitting."`
	ConfirmToken string          `json:"confirm_token,omitempty" jsonschema:"Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here."`
}

type fittingDeleteIn struct {
	FittingID    int    `json:"fitting_id"              jsonschema:"Fitting id from eve_fitting_list.,minimum=1"`
	ConfirmToken string `json:"confirm_token,omitempty" jsonschema:"Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here."`
}

type fittingResolved struct {
	shipID      int
	items       []map[string]any
	previewMods []string
	fail        map[string]any
}

func registerWriteFittings(s *mcp.Server) {
	addTool(s, &mcp.Tool{
		Name:        "eve_fitting_save",
		Description: "Save a ship fitting to the character's in-game fitting list.\n\nDoes not buy, move or fit anything — it stores a template. Unknown module names are rejected before anything is saved.",
	}, sessionTool(eveFittingSave))
	addTool(s, &mcp.Tool{
		Name:        "eve_fitting_delete",
		Description: "Delete a saved fitting. Permanent — there is no undo in game. The preview names the fitting so the user can confirm before the token is spent.",
	}, sessionTool(eveFittingDelete))
}

func eveFittingSave(ctx context.Context, a *session.Session, in fittingSaveIn) (any, error) {
	token, err := a.Character(ctx)
	if err != nil {
		return nil, wrap("eveFittingSave", err)
	}
	resolved, err := resolveFittingModules(ctx, a, in.Ship, in.Modules)
	if err != nil {
		return nil, err
	}
	if resolved.fail != nil {
		return resolved.fail, nil
	}
	name := in.Name
	if len(name) > fittingNameMax {
		name = name[:fittingNameMax]
	}
	desc := in.Description
	if len(desc) > fittingDescMax {
		desc = desc[:fittingDescMax]
	}
	body := map[string]any{fName: name, fDescription: desc, fShipTypeID: resolved.shipID, fItems: resolved.items}
	args := map[string]any{fName: name, fDescription: desc, fShipTypeID: resolved.shipID, fItems: resolved.items, fCharacterID: token.CharacterID}
	preview := map[string]any{
		fAction:    "Save a new fitting to the in-game fitting list",
		fCharacter: token.CharacterName, "fitting_name": name, "hull": in.Ship, fModules: resolved.previewMods,
	}
	blocked, err := a.Guard.Authorize(ctx, "eve_fitting_save", "fittings", args, preview, in.ConfirmToken, token.Scopes)
	if err != nil {
		return nil, wrap("eveFittingSave", err)
	}
	if blocked.Required != nil {
		return blocked.Required, nil
	}
	result, err := a.ESI.Post(ctx, esiPath("characters", esiID(token.CharacterID), "fittings"), &token.CharacterID, nil, body)
	recordWrite(ctx, a, "eve_fitting_save", "fittings", args, err)
	if err != nil {
		return nil, wrap("eveFittingSave", err)
	}

	return map[string]any{fStatus: vDone, fFittingID: j.Map(result)[fFittingID], fName: name}, nil
}

func resolveFittingModules(ctx context.Context, a *session.Session, ship string, modules []fittingModule) (fittingResolved, error) {
	wanted := make([]string, 0, 1+len(modules))
	wanted = append(wanted, ship)
	for _, m := range modules {
		wanted = append(wanted, m.Name)
	}
	resolutions, err := a.Resolver.ResolveNames(ctx, wanted, nil, []string{catInventoryTypes})
	if err != nil {
		return fittingResolved{}, wrap("resolveFittingModules", err)
	}
	byName := map[string]int{}
	for k, r := range resolutions {
		if r.Chosen != nil {
			byName[k] = r.Chosen.ID
		}
	}
	shipID := byName[strings.ToLower(strings.TrimSpace(ship))]
	if shipID == 0 {
		return fittingResolved{fail: map[string]any{fError: fmt.Sprintf("No hull is named exactly %q. Check the spelling with eve_universe_search.", ship)}}, nil
	}
	var items []map[string]any
	var unknown []string
	var previewMods []string
	for _, m := range modules {
		name := strings.TrimSpace(m.Name)
		tid := byName[strings.ToLower(name)]
		if tid == 0 {
			unknown = append(unknown, name)

			continue
		}
		qty := m.Quantity
		if qty == 0 {
			qty = 1
		}
		flag := m.Flag
		if flag == "" {
			flag = "Cargo"
		}
		items = append(items, map[string]any{fTypeID: tid, "flag": flag, fQuantity: qty})
		previewMods = append(previewMods, fmt.Sprintf("%s x%d [%s]", name, qty, flag))
	}
	if len(unknown) > 0 {
		return fittingResolved{fail: map[string]any{fError: fmt.Sprintf("These module names do not exist exactly as written: %v. Look each one up with eve_universe_search first.", unknown)}}, nil
	}

	return fittingResolved{shipID: shipID, items: items, previewMods: previewMods}, nil
}

func eveFittingDelete(ctx context.Context, a *session.Session, in fittingDeleteIn) (any, error) {
	token, err := a.Character(ctx)
	if err != nil {
		return nil, wrap("eveFittingDelete", err)
	}
	existing, err := a.ESI.Get(ctx, esiPath("characters", esiID(token.CharacterID), "fittings"), &token.CharacterID, nil, nil)
	if err != nil {
		return nil, wrap("eveFittingDelete", err)
	}
	var match map[string]any
	for _, f := range j.Maps(existing.Data) {
		if j.Int(f[fFittingID]) == in.FittingID {
			match = f

			break
		}
	}
	if match == nil {
		return map[string]any{fError: fmt.Sprintf("%s has no fitting with id %d. Call eve_fitting_list to see the real ids.", token.CharacterName, in.FittingID)}, nil
	}
	args := map[string]any{fFittingID: in.FittingID, fCharacterID: token.CharacterID}
	preview := map[string]any{
		fAction: "Permanently delete a saved fitting", fCharacter: token.CharacterName,
		"fitting_name": match[fName], fModules: len(j.Slice(match[fItems])),
	}
	blocked, err := a.Guard.Authorize(ctx, "eve_fitting_delete", "fittings", args, preview, in.ConfirmToken, token.Scopes)
	if err != nil {
		return nil, wrap("eveFittingDelete", err)
	}
	if blocked.Required != nil {
		return blocked.Required, nil
	}
	_, err = a.ESI.Delete(ctx, esiPath("characters", esiID(token.CharacterID), "fittings", esiID(in.FittingID)), &token.CharacterID, nil, nil)
	recordWrite(ctx, a, "eve_fitting_delete", "fittings", args, err)
	if err != nil {
		return nil, wrap("eveFittingDelete", err)
	}

	return map[string]any{fStatus: vDone, "deleted": match[fName]}, nil
}

type mailMarkIn struct {
	MailID       int    `json:"mail_id"                 jsonschema:"Mail id from eve_mail_list.,minimum=1"`
	Read         *bool  `json:"read,omitempty"          jsonschema:"True marks it read, False marks it unread. Default true."`
	ConfirmToken string `json:"confirm_token,omitempty" jsonschema:"Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here."`
}

type mailDeleteIn struct {
	MailID       int    `json:"mail_id"                 jsonschema:"Mail id from eve_mail_list.,minimum=1"`
	ConfirmToken string `json:"confirm_token,omitempty" jsonschema:"Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here."`
}

func registerMailOrganize(s *mcp.Server) {
	addTool(s, &mcp.Tool{
		Name:        "eve_mail_mark",
		Description: "Change the read flag on one mail. This does not return the mail's contents — use eve_mail_read for that. Needs a confirm_token in confirm mode.",
	}, sessionTool(eveMailMark))
	addTool(s, &mcp.Tool{
		Name:        "eve_mail_delete",
		Description: "Delete one mail. Permanent — deleted EVE mail cannot be recovered. The preview shows sender, subject and date so the user can confirm.",
	}, sessionTool(eveMailDelete))
}

func eveMailMark(ctx context.Context, a *session.Session, in mailMarkIn) (any, error) {
	token, err := a.Character(ctx)
	if err != nil {
		return nil, wrap("eveMailMark", err)
	}
	read := boolDef(in.Read, true)
	args := map[string]any{fMailID: in.MailID, fRead: read, fCharacterID: token.CharacterID}
	label := fUnread
	if read {
		label = fRead
	}
	preview := map[string]any{fAction: fmt.Sprintf("Mark mail #%d as %s", in.MailID, label), fCharacter: token.CharacterName}
	blocked, err := a.Guard.Authorize(ctx, "eve_mail_mark", "mail_organize", args, preview, in.ConfirmToken, token.Scopes)
	if err != nil {
		return nil, wrap("eveMailMark", err)
	}
	if blocked.Required != nil {
		return blocked.Required, nil
	}
	_, err = a.ESI.Put(ctx, esiPath("characters", esiID(token.CharacterID), "mail", esiID(in.MailID)), &token.CharacterID, nil, map[string]any{fRead: read})
	recordWrite(ctx, a, "eve_mail_mark", "mail_organize", args, err)
	if err != nil {
		return nil, wrap("eveMailMark", err)
	}

	return map[string]any{fStatus: vDone, fMailID: in.MailID, fRead: read}, nil
}

func eveMailDelete(ctx context.Context, a *session.Session, in mailDeleteIn) (any, error) {
	token, err := a.Character(ctx)
	if err != nil {
		return nil, wrap("eveMailDelete", err)
	}
	header, err := a.ESI.Get(ctx, esiPath("characters", esiID(token.CharacterID), "mail", esiID(in.MailID)), &token.CharacterID, nil, nil)
	if err != nil {
		return nil, wrap("eveMailDelete", err)
	}
	mail := j.Map(header.Data)
	sender, err := a.Resolver.Name(ctx, j.Int(mail[fFrom]), nil)
	if err != nil {
		return nil, wrap("eveMailDelete", err)
	}
	args := map[string]any{fMailID: in.MailID, fCharacterID: token.CharacterID}
	preview := map[string]any{
		fAction: "Permanently delete a mail", fCharacter: token.CharacterName,
		fSubject: mail[fSubject], fFrom: sender, fTimestamp: mail[fTimestamp],
	}
	blocked, err := a.Guard.Authorize(ctx, "eve_mail_delete", "mail_organize", args, preview, in.ConfirmToken, token.Scopes)
	if err != nil {
		return nil, wrap("eveMailDelete", err)
	}
	if blocked.Required != nil {
		return blocked.Required, nil
	}
	_, err = a.ESI.Delete(ctx, esiPath("characters", esiID(token.CharacterID), "mail", esiID(in.MailID)), &token.CharacterID, nil, nil)
	recordWrite(ctx, a, "eve_mail_delete", "mail_organize", args, err)
	if err != nil {
		return nil, wrap("eveMailDelete", err)
	}

	return map[string]any{fStatus: vDone, "deleted_subject": mail[fSubject]}, nil
}

type mailSendIn struct {
	To           []string `json:"to"                      jsonschema:"Exact character, corporation or alliance names."`
	Subject      string   `json:"subject"                 jsonschema:"Mail subject."`
	Body         string   `json:"body"                    jsonschema:"Mail body text."`
	ApprovedCost int      `json:"approved_cost,omitempty" jsonschema:"ISK you accept paying for CSPA charges. 0 refuses to pay.,minimum=0"`
	ConfirmToken string   `json:"confirm_token,omitempty" jsonschema:"Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here."`
}

type mailRecipients struct {
	recipients    []map[string]any
	resolvedNames []string
	fail          map[string]any
}

func registerMailSend(s *mcp.Server) {
	addTool(s, &mcp.Tool{
		Name:        "eve_mail_send",
		Description: "Send an in-game EVE mail from this character to other players.\n\nThe most consequential tool on this server. The mail cannot be recalled. Show the preview to the user word for word — including the full body — and get an explicit yes before confirming.",
	}, sessionTool(eveMailSend))
}

func eveMailSend(ctx context.Context, a *session.Session, in mailSendIn) (any, error) {
	token, err := a.Character(ctx)
	if err != nil {
		return nil, wrap("eveMailSend", err)
	}
	if len(in.To) > mailRecipientsMax {
		return map[string]any{fError: fmt.Sprintf("Refusing to mail %d recipients at once; the cap is %d. Send in smaller batches.", len(in.To), mailRecipientsMax)}, nil
	}
	resolved, err := resolveMailRecipients(ctx, a, in.To)
	if err != nil {
		return nil, err
	}
	if resolved.fail != nil {
		return resolved.fail, nil
	}
	subj, body := in.Subject, in.Body
	if len(subj) > mailSubjectMax {
		subj = subj[:mailSubjectMax]
	}
	if len(body) > mailBodyMax {
		body = body[:mailBodyMax]
	}
	payload := map[string]any{"recipients": resolved.recipients, fSubject: subj, fBody: body, fApprovedCost: in.ApprovedCost}
	args := map[string]any{"recipients": resolved.recipients, fSubject: subj, fBody: body, fApprovedCost: in.ApprovedCost, fCharacterID: token.CharacterID}
	preview := map[string]any{
		fAction: "SEND AN IN-GAME MAIL — another player will receive this and it cannot be recalled",
		fFrom:   token.CharacterName, "to": resolved.resolvedNames, fSubject: subj, fBody: body,
		"approved_cspa_cost_isk": in.ApprovedCost,
	}
	blocked, err := a.Guard.Authorize(ctx, "eve_mail_send", "mail_send", args, preview, in.ConfirmToken, token.Scopes)
	if err != nil {
		return nil, wrap("eveMailSend", err)
	}
	if blocked.Required != nil {
		return blocked.Required, nil
	}
	mailID, err := a.ESI.Post(ctx, esiPath("characters", esiID(token.CharacterID), "mail"), &token.CharacterID, nil, payload)
	recordWrite(ctx, a, write.ToolMailSend, write.CapMailSend, args, err)
	if err != nil {
		return nil, wrap("eveMailSend", err)
	}

	return map[string]any{fStatus: "sent", fMailID: mailID, "to": resolved.resolvedNames}, nil
}

func resolveMailRecipients(ctx context.Context, a *session.Session, to []string) (mailRecipients, error) {
	only := []string{fCharacters, fCorporations, fAlliances}
	resolutions, err := a.Resolver.ResolveNames(ctx, to, nil, only)
	if err != nil {
		return mailRecipients{}, wrap("resolveMailRecipients", err)
	}
	var recipients []map[string]any
	var resolvedNames []string
	var unknown []string
	var ambiguous []esi.NameResolution
	seen := map[int]struct{}{}
	for _, asked := range to {
		match := resolutions[strings.ToLower(strings.TrimSpace(asked))]
		if match.Chosen == nil {
			unknown = append(unknown, asked)
		} else if match.Ambiguous() {
			ambiguous = append(ambiguous, match)
		} else if _, ok := seen[match.Chosen.ID]; !ok {
			seen[match.Chosen.ID] = struct{}{}
			recipients = append(recipients, map[string]any{
				"recipient_id": match.Chosen.ID, "recipient_type": recipientType(match.Chosen.Category),
			})
			resolvedNames = append(resolvedNames, fmt.Sprintf("%s (%s)", match.Chosen.Name, match.Chosen.Kind))
		}
	}
	if len(unknown) > 0 {
		return mailRecipients{fail: map[string]any{fError: fmt.Sprintf("Could not resolve recipient(s): %v. Names must match exactly; check them with eve_universe_search. Nothing was sent.", unknown)}}, nil
	}
	if len(ambiguous) > 0 {
		var parts []string
		for _, m := range ambiguous {
			parts = append(parts, m.Describe())
		}

		return mailRecipients{fail: map[string]any{fError: "Refusing to send — " + strings.Join(parts, "; ") + ". EVE mail cannot be recalled, so confirm which one is meant with eve_universe_search. Nothing was sent."}}, nil
	}

	return mailRecipients{recipients: recipients, resolvedNames: resolvedNames}, nil
}

type contactsSetIn struct {
	Names        []string `json:"names"                   jsonschema:"Exact character, corporation or alliance names."`
	Standing     float64  `json:"standing"                jsonschema:"-10.0 to 10.0.,minimum=-10,maximum=10"`
	Watched      *bool    `json:"watched,omitempty"       jsonschema:"Add to the watch list. Characters only."`
	ConfirmToken string   `json:"confirm_token,omitempty" jsonschema:"Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here."`
}

type contactsDeleteIn struct {
	Names        []string `json:"names"                   jsonschema:"Exact contact names to remove."`
	ConfirmToken string   `json:"confirm_token,omitempty" jsonschema:"Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here."`
}

type contactOp struct {
	verb string
	ids  []int
	flag bool
}

type contactApplyResult struct {
	appliedU []int
	appliedA []int
	fail     map[string]any
}

func registerContacts(s *mcp.Server) {
	addTool(s, &mcp.Tool{
		Name:        "eve_contacts_set",
		Description: "Add or update contacts with a standing.\n\nA negative standing colours that player red in the overview. Treat it as a visible social act.",
	}, sessionTool(eveContactsSet))
	addTool(s, &mcp.Tool{
		Name:        "eve_contacts_delete",
		Description: "Remove contacts from this character's contact list.\n\nDeleting a contact also clears any standing set on them. That is a visible social change, so confirm the names before the second call. It does not block or report anyone.",
	}, sessionTool(eveContactsDelete))
}

func eveContactsSet(ctx context.Context, a *session.Session, in contactsSetIn) (any, error) {
	token, err := a.Character(ctx)
	if err != nil {
		return nil, wrap("eveContactsSet", err)
	}
	matches, failure, err := resolveContacts(ctx, a, in.Names)
	if err != nil {
		return nil, err
	}
	if failure != nil {
		return failure, nil
	}
	watched := boolDef(in.Watched, false)
	var contactIDs []int
	var resolved []string
	watchable := map[int]struct{}{}
	for _, m := range matches {
		contactIDs = append(contactIDs, m.ID)
		resolved = append(resolved, m.Name)
		if m.Category == fCharacters {
			watchable[m.ID] = struct{}{}
		}
	}
	existing, err := a.ESI.GetAllPages(ctx, esiPath("characters", esiID(token.CharacterID), "contacts"), &token.CharacterID, nil, pagesESI)
	if err != nil {
		return nil, wrap("eveContactsSet", err)
	}
	known := map[int]struct{}{}
	for _, c := range j.Maps(existing.Data) {
		known[j.Int(c["contact_id"])] = struct{}{}
	}
	var updating, neu []int
	for _, id := range contactIDs {
		if _, ok := known[id]; ok {
			updating = append(updating, id)
		} else {
			neu = append(neu, id)
		}
	}
	args := map[string]any{fContactIDs: contactIDs, fStanding: in.Standing, fWatched: watched, fCharacterID: token.CharacterID}
	preview := map[string]any{
		fAction:    "Set contact standings (visible in the character's overview)",
		fCharacter: token.CharacterName, fContacts: resolved, fStanding: in.Standing,
		fWatched: watched, "new_contacts": len(neu), "updated_contacts": len(updating),
	}
	if watched && len(watchable) != len(contactIDs) {
		preview["watched_note"] = fmt.Sprintf("Only %d of %d are characters; the rest are corporations or alliances, which cannot be watched.", len(watchable), len(contactIDs))
	}
	blocked, err := a.Guard.Authorize(ctx, "eve_contacts_set", fContacts, args, preview, in.ConfirmToken, token.Scopes)
	if err != nil {
		return nil, wrap("eveContactsSet", err)
	}
	if blocked.Required != nil {
		return blocked.Required, nil
	}
	applied, err := applyContactOps(ctx, a, token.CharacterID, in.Standing, buildContactOps(updating, neu, watched, watchable))
	if err != nil {
		return nil, err
	}
	if applied.fail != nil {
		return applied.fail, nil
	}

	return map[string]any{fStatus: vDone, fContacts: resolved, fStanding: in.Standing}, nil
}

func buildContactOps(updating, neu []int, watched bool, watchable map[int]struct{}) []contactOp {
	var ops []contactOp
	for _, pair := range []struct {
		verb string
		ids  []int
	}{{vUpdate, updating}, {"add", neu}} {
		if len(pair.ids) == 0 {
			continue
		}
		if !watched {
			ops = append(ops, contactOp{pair.verb, pair.ids, false})

			continue
		}
		var yes, no []int
		for _, id := range pair.ids {
			if _, ok := watchable[id]; ok {
				yes = append(yes, id)
			} else {
				no = append(no, id)
			}
		}
		if len(yes) > 0 {
			ops = append(ops, contactOp{pair.verb, yes, true})
		}
		if len(no) > 0 {
			ops = append(ops, contactOp{pair.verb, no, false})
		}
	}

	return ops
}

func applyContactOps(ctx context.Context, a *session.Session, characterID int, standing float64, ops []contactOp) (contactApplyResult, error) {
	appliedU, appliedA := []int{}, []int{}
	path := esiPath("characters", esiID(characterID), "contacts")
	for _, op := range ops {
		err := runContactOp(ctx, a, characterID, path, standing, op)
		if err != nil {
			if len(appliedU)+len(appliedA) == 0 {
				return contactApplyResult{}, err
			}
			status := 0
			if e, ok := errors.AsType[esi.Error](err); ok {
				status = e.Status
			}

			return contactApplyResult{fail: map[string]any{
				fError: fmt.Sprintf("Partially applied. Standing %v reached %d existing and %d new contact(s) before this failed: %v. Call eve_contacts_set again with the same arguments.", standing, len(appliedU), len(appliedA), err),
				fKind:  "EsiError", fStatus: status,
			}}, nil
		}
		if op.verb == vUpdate {
			appliedU = append(appliedU, op.ids...)
		} else {
			appliedA = append(appliedA, op.ids...)
		}
	}

	return contactApplyResult{appliedU: appliedU, appliedA: appliedA}, nil
}

func runContactOp(ctx context.Context, a *session.Session, characterID int, path string, standing float64, op contactOp) error {
	var call func(context.Context, string, *int, map[string]any, any) (any, error)
	if op.verb == vUpdate {
		call = a.ESI.Put
	} else {
		call = a.ESI.Post
	}
	args := map[string]any{fContactIDs: op.ids, fStanding: standing, fWatched: op.flag, fCharacterID: characterID, "phase": op.verb}
	_, err := call(ctx, path, &characterID, map[string]any{fStanding: standing, fWatched: op.flag}, op.ids)
	recordWrite(ctx, a, "eve_contacts_set", fContacts, args, err)
	if err != nil {
		return err
	}

	return nil
}

func eveContactsDelete(ctx context.Context, a *session.Session, in contactsDeleteIn) (any, error) {
	token, err := a.Character(ctx)
	if err != nil {
		return nil, wrap("eveContactsDelete", err)
	}
	matches, failure, err := resolveContacts(ctx, a, in.Names)
	if err != nil {
		return nil, err
	}
	if failure != nil {
		return failure, nil
	}
	var ids []int
	var resolved []string
	for _, m := range matches {
		ids = append(ids, m.ID)
		resolved = append(resolved, m.Name)
	}
	args := map[string]any{fContactIDs: ids, fCharacterID: token.CharacterID}
	preview := map[string]any{fAction: "Delete contacts and the standings set on them", fCharacter: token.CharacterName, fContacts: resolved}
	blocked, err := a.Guard.Authorize(ctx, "eve_contacts_delete", fContacts, args, preview, in.ConfirmToken, token.Scopes)
	if err != nil {
		return nil, wrap("eveContactsDelete", err)
	}
	if blocked.Required != nil {
		return blocked.Required, nil
	}
	_, err = a.ESI.Delete(ctx, esiPath("characters", esiID(token.CharacterID), "contacts"), &token.CharacterID, map[string]any{fContactIDs: ids}, nil)
	recordWrite(ctx, a, "eve_contacts_delete", fContacts, args, err)
	if err != nil {
		return nil, wrap("eveContactsDelete", err)
	}

	return map[string]any{fStatus: vDone, "removed": resolved}, nil
}

func registerCalendar(s *mcp.Server) {
	type in struct {
		EventID      int    `json:"event_id"                jsonschema:"Event id from the in-game calendar.,minimum=1"`
		Response     string `json:"response"                jsonschema:"accepted, declined, or tentative."`
		ConfirmToken string `json:"confirm_token,omitempty" jsonschema:"Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here."`
	}
	addTool(s, &mcp.Tool{
		Name:        "eve_calendar_respond",
		Description: "Respond to a calendar event invitation on this character.\n\nThe organiser and other invitees see accepted, declined or tentative in-game. This only RSVPs; it does not create, edit or delete events. Confirm before sending an answer the player will have to live with.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in in) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *session.Session) (any, error) {
			token, err := a.Character(ctx)
			if err != nil {
				return nil, wrap("registerCalendar", err)
			}
			detail, err := a.ESI.Get(ctx, esiPath("characters", esiID(token.CharacterID), "calendar", esiID(in.EventID)), &token.CharacterID, nil, nil)
			if err != nil {
				return nil, wrap("registerCalendar", err)
			}
			event := j.Map(detail.Data)
			args := map[string]any{"event_id": in.EventID, fResponse: in.Response, fCharacterID: token.CharacterID}
			preview := map[string]any{
				fAction:    "Respond to a calendar invitation — the organiser is notified",
				fCharacter: token.CharacterName, "event": event[fTitle], fDate: event[fDate],
				"owner": event["owner_name"], fResponse: in.Response,
			}
			blocked, err := a.Guard.Authorize(ctx, "eve_calendar_respond", "calendar", args, preview, in.ConfirmToken, token.Scopes)
			if err != nil {
				return nil, wrap("registerCalendar", err)
			}
			if blocked.Required != nil {
				return blocked.Required, nil
			}
			_, err = a.ESI.Put(ctx, esiPath("characters", esiID(token.CharacterID), "calendar", esiID(in.EventID)), &token.CharacterID, nil, map[string]any{fResponse: in.Response})
			recordWrite(ctx, a, "eve_calendar_respond", "calendar", args, err)
			if err != nil {
				return nil, wrap("registerCalendar", err)
			}

			return map[string]any{fStatus: vDone, "event": event[fTitle], fResponse: in.Response}, nil
		})
	})
}

func resolveContacts(ctx context.Context, a *session.Session, namesIn []string) ([]esi.NameMatch, map[string]any, error) {
	only := []string{fCharacters, fCorporations, fAlliances}
	resolutions, err := a.Resolver.ResolveNames(ctx, namesIn, nil, only)
	if err != nil {
		return nil, nil, wrap("resolveContacts", err)
	}
	var matches []esi.NameMatch
	var unknown []string
	var ambiguous []esi.NameResolution
	seen := map[int]struct{}{}
	for _, asked := range namesIn {
		match := resolutions[strings.ToLower(strings.TrimSpace(asked))]
		if match.Chosen == nil {
			unknown = append(unknown, asked)
		} else if match.Ambiguous() {
			ambiguous = append(ambiguous, match)
		} else if _, ok := seen[match.Chosen.ID]; !ok {
			seen[match.Chosen.ID] = struct{}{}
			matches = append(matches, *match.Chosen)
		}
	}
	if len(unknown) > 0 {
		return nil, map[string]any{fError: fmt.Sprintf("Could not resolve: %v. Names must be exact — check them with eve_universe_search. Nothing was changed.", unknown)}, nil
	}
	if len(ambiguous) > 0 {
		var parts []string
		for _, m := range ambiguous {
			parts = append(parts, m.Describe())
		}

		return nil, map[string]any{fError: "Refusing to act — " + strings.Join(parts, "; ") + ". Confirm which one is meant with eve_universe_search. Nothing was changed."}, nil
	}

	return matches, nil, nil
}

func resolveDestination(ctx context.Context, a *session.Session, name string, characterID int) (map[string]any, error) {
	order := []string{fStations, fSystems}
	resolved, err := a.Resolver.ResolveNames(ctx, []string{name}, order, order)
	if err != nil {
		return nil, wrap("resolveDestination", err)
	}
	match := resolved[strings.ToLower(strings.TrimSpace(name))]
	if match.Chosen != nil {
		out := map[string]any{"id": match.Chosen.ID, fName: match.Chosen.Name, fKind: match.Chosen.Kind}
		if match.Ambiguous() {
			out["ambiguity"] = match.Describe()
		}

		return out, nil
	}
	search, err := a.ESI.Get(ctx, esiPath("characters", esiID(characterID), "search"), &characterID, map[string]any{
		"categories": []string{fStructure}, "search": name, fStrict: false,
	}, nil)
	if err != nil {
		return nil, wrap("resolveDestination", err)
	}
	structures := j.Slice(j.Map(search.Data)[fStructure])
	if len(structures) > 0 {
		sid := j.Int(structures[0])
		sname, err := a.Resolver.Name(ctx, sid, &characterID)
		if err != nil {
			return nil, wrap("resolveDestination", err)
		}

		return map[string]any{"id": sid, fName: sname, fKind: fStructure}, nil
	}

	return map[string]any{fError: fmt.Sprintf("No system, station or visible structure is named exactly %q. Check the spelling with eve_universe_search.", name)}, nil
}

func resolveEntity(ctx context.Context, a *session.Session, name string, characterID int, kind string) (map[string]any, error) {
	if _, err := strconv.Atoi(strings.TrimSpace(name)); err == nil {
		id := j.Int(name)
		n, err := a.Resolver.Name(ctx, id, &characterID)
		if err != nil {
			return nil, wrap("resolveEntity", err)
		}

		return map[string]any{"id": id, fName: n, fKind: "id"}, nil
	}
	var prefer, only []string
	if kind == "market" {
		prefer = []string{catInventoryTypes}
		only = prefer
	} else {
		prefer = []string{fCharacters, fCorporations, fAlliances, catInventoryTypes, fSystems, fStations}
	}
	resolved, err := a.Resolver.ResolveNames(ctx, []string{name}, prefer, only)
	if err != nil {
		return nil, wrap("resolveEntity", err)
	}
	match := resolved[strings.ToLower(strings.TrimSpace(name))]
	if match.Chosen != nil {
		out := map[string]any{"id": match.Chosen.ID, fName: match.Chosen.Name, fKind: match.Chosen.Kind}
		if match.Ambiguous() {
			out["ambiguity"] = match.Describe()
		}

		return out, nil
	}

	return map[string]any{fError: fmt.Sprintf("Could not resolve %q for the %s window. Check the exact name with eve_universe_search.", name, kind)}, nil
}

type windowPlan struct {
	path     string
	params   map[string]any
	label    string
	resolved map[string]any
	refuse   map[string]any
}

func planOpenWindow(ctx context.Context, a *session.Session, kind, target string, characterID int) (windowPlan, error) {
	if kind == "contract" {
		id, ok := parseContractID(target)
		if !ok {
			return windowPlan{refuse: map[string]any{fError: "For window='contract', `target` must be the numeric contract_id from eve_market_contracts (run it with response_format='detailed' to get the id)."}}, nil
		}

		return windowPlan{
			path:   "/ui/openwindow/contract",
			params: map[string]any{"contract_id": id},
			label:  "contract #" + strings.TrimSpace(target),
		}, nil
	}
	resolved, err := resolveEntity(ctx, a, target, characterID, kind)
	if err != nil {
		return windowPlan{}, err
	}
	if _, ok := resolved[fError]; ok {
		return windowPlan{refuse: resolved}, nil
	}
	plan := windowPlan{resolved: resolved, label: j.Str(resolved[fName])}
	if kind == "market" {
		plan.path = "/ui/openwindow/marketdetails"
		plan.params = map[string]any{fTypeID: resolved["id"]}
	} else {
		plan.path = "/ui/openwindow/information"
		plan.params = map[string]any{"target_id": resolved["id"]}
	}
	if k := j.Str(resolved[fKind]); k != "" && k != "id" {
		plan.label = fmt.Sprintf("%s (%s)", plan.label, k)
	}

	return plan, nil
}

func parseContractID(s string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, false
	}

	return n, true
}
