package eve

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"eve-mcp/internal/adapter/esi"
	"eve-mcp/internal/adapter/names"
	"eve-mcp/internal/domain/j"
	"eve-mcp/internal/usecase/session"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const clientCaveat = "Takes effect only while the EVE client is running and logged in on this character. With the client closed the call reports success and nothing visible happens."

var recipientKeys = map[string]string{
	"characters": "character", "corporations": "corporation", "alliances": "alliance",
}

func registerWrites(s *mcp.Server, a *session.Session) {
	if a.Opts.Write.CapabilityEnabled("waypoint") {
		registerWaypoint(s, a)
	}
	if a.Opts.Write.CapabilityEnabled("openwindow") {
		registerOpenWindow(s, a)
	}
	if a.Opts.Write.CapabilityEnabled("fittings") {
		registerWriteFittings(s, a)
	}
	if a.Opts.Write.CapabilityEnabled("mail_organize") {
		registerMailOrganize(s, a)
	}
	if a.Opts.Write.CapabilityEnabled("mail_send") {
		registerMailSend(s, a)
	}
	if a.Opts.Write.CapabilityEnabled("contacts") {
		registerContacts(s, a)
	}
	if a.Opts.Write.CapabilityEnabled("calendar") {
		registerCalendar(s, a)
	}
}

func registerWaypoint(s *mcp.Server, a *session.Session) {
	type in struct {
		Destination         string `json:"destination" jsonschema:"Exact system, station or structure name."`
		Character           string `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
		ClearOtherWaypoints *bool  `json:"clear_other_waypoints,omitempty" jsonschema:"True replaces the whole existing route. Default true."`
		AddToBeginning      *bool  `json:"add_to_beginning,omitempty" jsonschema:"Insert as the very next hop rather than the final stop."`
		ConfirmToken        string `json:"confirm_token,omitempty" jsonschema:"Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here."`
	}
	addTool(s, &mcp.Tool{
		Name:        "eve_ui_set_waypoint",
		Description: "Set an autopilot waypoint in the running game client.\n\nThis only moves the route marker on the map. It never undocks, flies or activates autopilot. Default clear_other_waypoints=true wipes a route the player may have spent time building.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in in) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *session.Session) (any, error) {
			token, err := a.ResolveCharacter(in.Character)
			if err != nil {
				return nil, err
			}
			target, err := resolveDestination(a, in.Destination, token.CharacterID)
			if err != nil {
				return nil, err
			}
			if _, ok := target["error"]; ok {
				return target, nil
			}
			clear := boolDef(in.ClearOtherWaypoints, true)
			add := boolDef(in.AddToBeginning, false)
			args := map[string]any{
				"destination_id": target["id"], "character_id": token.CharacterID,
				"clear_other_waypoints": clear, "add_to_beginning": add,
			}
			pos := "final stop"
			if add {
				pos = "next hop"
			}
			preview := map[string]any{
				"action":                "Set an autopilot waypoint in the game client",
				"character":             token.CharacterName,
				"destination":           fmt.Sprintf("%v (%v)", target["name"], target["kind"]),
				"clears_existing_route": clear, "position": pos,
			}
			if amb := j.Str(target["ambiguity"]); amb != "" {
				preview["ambiguous_name"] = amb + " — this routes to the first. Cancel and use eve_universe_search if the other one was meant."
			}
			blocked, err := a.Guard.Authorize(ctx, "eve_ui_set_waypoint", "waypoint", args, preview, in.ConfirmToken, token.Scopes)
			if err != nil {
				return nil, err
			}
			if blocked != nil {
				return blocked, nil
			}
			if _, err := a.ESI.Post("/ui/autopilot/waypoint", &token.CharacterID, map[string]any{
				"destination_id": target["id"], "clear_other_waypoints": clear, "add_to_beginning": add,
			}, nil); err != nil {
				return nil, err
			}
			a.Guard.Record(ctx, "eve_ui_set_waypoint", "waypoint", args, "ok")
			return map[string]any{"status": "done", "waypoint_set_to": target["name"], "note": clientCaveat}, nil
		})
	})
}

func registerOpenWindow(s *mcp.Server, a *session.Session) {
	type in struct {
		Window       string `json:"window" jsonschema:"'market' opens market details for an item. 'info' opens Show Info. 'contract' opens one contract."`
		Target       string `json:"target" jsonschema:"For market, an exact item name. For info, an exact name of any entity. For contract, the numeric contract_id."`
		Character    string `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
		ConfirmToken string `json:"confirm_token,omitempty" jsonschema:"Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here."`
	}
	addTool(s, &mcp.Tool{
		Name:        "eve_ui_open_window",
		Description: "Open a window in the running game client.\n\nGood for handing something off to the player. Changes nothing in game and costs nothing.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in in) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *session.Session) (any, error) {
			token, err := a.ResolveCharacter(in.Character)
			if err != nil {
				return nil, err
			}
			kind := strings.ToLower(strings.TrimSpace(in.Window))
			var path string
			var params map[string]any
			var label string
			var resolved map[string]any
			if kind == "contract" {
				if _, err := strconv.Atoi(strings.TrimSpace(in.Target)); err != nil {
					return map[string]any{"error": "For window='contract', `target` must be the numeric contract_id from eve_market_contracts (run it with response_format='detailed' to get the id)."}, nil
				}
				path, params = "/ui/openwindow/contract", map[string]any{"contract_id": j.Int(in.Target)}
				label = "contract #" + strings.TrimSpace(in.Target)
			} else {
				resolved, err = resolveEntity(a, in.Target, token.CharacterID, kind)
				if err != nil {
					return nil, err
				}
				if _, ok := resolved["error"]; ok {
					return resolved, nil
				}
				if kind == "market" {
					path, params = "/ui/openwindow/marketdetails", map[string]any{"type_id": resolved["id"]}
				} else {
					path, params = "/ui/openwindow/information", map[string]any{"target_id": resolved["id"]}
				}
				label = j.Str(resolved["name"])
				if k := j.Str(resolved["kind"]); k != "" && k != "id" {
					label = fmt.Sprintf("%s (%s)", label, k)
				}
			}
			args := map[string]any{"window": kind, "params": params, "character_id": token.CharacterID}
			preview := map[string]any{
				"action":    fmt.Sprintf("Open the %s window in the game client", kind),
				"character": token.CharacterName, "target": label,
			}
			if resolved != nil {
				if amb := j.Str(resolved["ambiguity"]); amb != "" {
					preview["ambiguous_name"] = amb + " — this opens the first. Cancel and use eve_universe_search if the other one was meant."
				}
			}
			blocked, err := a.Guard.Authorize(ctx, "eve_ui_open_window", "openwindow", args, preview, in.ConfirmToken, token.Scopes)
			if err != nil {
				return nil, err
			}
			if blocked != nil {
				return blocked, nil
			}
			if _, err := a.ESI.Post(path, &token.CharacterID, params, nil); err != nil {
				return nil, err
			}
			a.Guard.Record(ctx, "eve_ui_open_window", "openwindow", args, "ok")
			return map[string]any{"status": "done", "opened": label, "note": clientCaveat}, nil
		})
	})
}

type fittingModule struct {
	Name     string `json:"name" jsonschema:"Exact module name."`
	Flag     string `json:"flag,omitempty" jsonschema:"HiSlot0-7, MedSlot0-7, LoSlot0-7, RigSlot0-2, SubSystemSlot0-4, DroneBay, FighterBay, Cargo."`
	Quantity int    `json:"quantity,omitempty" jsonschema:"Default 1."`
}

func registerWriteFittings(s *mcp.Server, a *session.Session) {
	type saveIn struct {
		Name         string          `json:"name" jsonschema:"Fitting name as it will appear in game."`
		Ship         string          `json:"ship" jsonschema:"Exact hull name, e.g. 'Rifter'."`
		Modules      []fittingModule `json:"modules" jsonschema:"Modules as objects with name, flag, quantity."`
		Description  string          `json:"description,omitempty" jsonschema:"Optional note stored with the fitting."`
		Character    string          `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
		ConfirmToken string          `json:"confirm_token,omitempty" jsonschema:"Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here."`
	}
	addTool(s, &mcp.Tool{
		Name:        "eve_fitting_save",
		Description: "Save a ship fitting to the character's in-game fitting list.\n\nDoes not buy, move or fit anything — it stores a template. Unknown module names are rejected before anything is saved.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in saveIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *session.Session) (any, error) {
			token, err := a.ResolveCharacter(in.Character)
			if err != nil {
				return nil, err
			}
			wanted := []string{in.Ship}
			for _, m := range in.Modules {
				wanted = append(wanted, m.Name)
			}
			resolutions, err := a.Resolver.ResolveNames(wanted, nil, []string{"inventory_types"})
			if err != nil {
				return nil, err
			}
			byName := map[string]int{}
			for k, r := range resolutions {
				if r.Chosen != nil {
					byName[k] = r.Chosen.ID
				}
			}
			shipID := byName[strings.ToLower(strings.TrimSpace(in.Ship))]
			if shipID == 0 {
				return map[string]any{"error": fmt.Sprintf("No hull is named exactly %q. Check the spelling with eve_universe_search.", in.Ship)}, nil
			}
			var items []map[string]any
			var unknown []string
			var previewMods []string
			for _, m := range in.Modules {
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
				items = append(items, map[string]any{"type_id": tid, "flag": flag, "quantity": qty})
				previewMods = append(previewMods, fmt.Sprintf("%s x%d [%s]", name, qty, flag))
			}
			if len(unknown) > 0 {
				return map[string]any{"error": fmt.Sprintf("These module names do not exist exactly as written: %v. Look each one up with eve_universe_search first.", unknown)}, nil
			}
			name := in.Name
			if len(name) > 50 {
				name = name[:50]
			}
			desc := in.Description
			if len(desc) > 500 {
				desc = desc[:500]
			}
			body := map[string]any{"name": name, "description": desc, "ship_type_id": shipID, "items": items}
			args := map[string]any{"name": name, "description": desc, "ship_type_id": shipID, "items": items, "character_id": token.CharacterID}
			preview := map[string]any{
				"action":    "Save a new fitting to the in-game fitting list",
				"character": token.CharacterName, "fitting_name": name, "hull": in.Ship, "modules": previewMods,
			}
			blocked, err := a.Guard.Authorize(ctx, "eve_fitting_save", "fittings", args, preview, in.ConfirmToken, token.Scopes)
			if err != nil {
				return nil, err
			}
			if blocked != nil {
				return blocked, nil
			}
			result, err := a.ESI.Post(fmt.Sprintf("/characters/%d/fittings", token.CharacterID), &token.CharacterID, nil, body)
			if err != nil {
				return nil, err
			}
			a.Guard.Record(ctx, "eve_fitting_save", "fittings", args, result)
			return map[string]any{"status": "done", "fitting_id": j.Map(result)["fitting_id"], "name": name}, nil
		})
	})

	type delIn struct {
		FittingID    int    `json:"fitting_id" jsonschema:"Fitting id from eve_fitting_list.,minimum=1"`
		Character    string `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
		ConfirmToken string `json:"confirm_token,omitempty" jsonschema:"Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here."`
	}
	addTool(s, &mcp.Tool{
		Name:        "eve_fitting_delete",
		Description: "Delete a saved fitting. Permanent — there is no undo in game. The preview names the fitting so the user can confirm before the token is spent.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in delIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *session.Session) (any, error) {
			token, err := a.ResolveCharacter(in.Character)
			if err != nil {
				return nil, err
			}
			existing, err := a.ESI.Get(fmt.Sprintf("/characters/%d/fittings", token.CharacterID), &token.CharacterID, nil, nil)
			if err != nil {
				return nil, err
			}
			var match map[string]any
			for _, f := range j.Maps(existing.Data) {
				if j.Int(f["fitting_id"]) == in.FittingID {
					match = f
					break
				}
			}
			if match == nil {
				return map[string]any{"error": fmt.Sprintf("%s has no fitting with id %d. Call eve_fitting_list to see the real ids.", token.CharacterName, in.FittingID)}, nil
			}
			args := map[string]any{"fitting_id": in.FittingID, "character_id": token.CharacterID}
			preview := map[string]any{
				"action": "Permanently delete a saved fitting", "character": token.CharacterName,
				"fitting_name": match["name"], "modules": len(j.Slice(match["items"])),
			}
			blocked, err := a.Guard.Authorize(ctx, "eve_fitting_delete", "fittings", args, preview, in.ConfirmToken, token.Scopes)
			if err != nil {
				return nil, err
			}
			if blocked != nil {
				return blocked, nil
			}
			if _, err := a.ESI.Delete(fmt.Sprintf("/characters/%d/fittings/%d", token.CharacterID, in.FittingID), &token.CharacterID, nil, nil); err != nil {
				return nil, err
			}
			a.Guard.Record(ctx, "eve_fitting_delete", "fittings", args, "ok")
			return map[string]any{"status": "done", "deleted": match["name"]}, nil
		})
	})
}

func registerMailOrganize(s *mcp.Server, a *session.Session) {
	type markIn struct {
		MailID       int    `json:"mail_id" jsonschema:"Mail id from eve_mail_list.,minimum=1"`
		Read         *bool  `json:"read,omitempty" jsonschema:"True marks it read, False marks it unread. Default true."`
		Character    string `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
		ConfirmToken string `json:"confirm_token,omitempty" jsonschema:"Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here."`
	}
	addTool(s, &mcp.Tool{
		Name:        "eve_mail_mark",
		Description: "Change the read flag on one mail. This does not return the mail's contents — use eve_mail_read for that. Needs a confirm_token in confirm mode.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in markIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *session.Session) (any, error) {
			token, err := a.ResolveCharacter(in.Character)
			if err != nil {
				return nil, err
			}
			read := boolDef(in.Read, true)
			args := map[string]any{"mail_id": in.MailID, "read": read, "character_id": token.CharacterID}
			label := "unread"
			if read {
				label = "read"
			}
			preview := map[string]any{"action": fmt.Sprintf("Mark mail #%d as %s", in.MailID, label), "character": token.CharacterName}
			blocked, err := a.Guard.Authorize(ctx, "eve_mail_mark", "mail_organize", args, preview, in.ConfirmToken, token.Scopes)
			if err != nil {
				return nil, err
			}
			if blocked != nil {
				return blocked, nil
			}
			if _, err := a.ESI.Put(fmt.Sprintf("/characters/%d/mail/%d", token.CharacterID, in.MailID), &token.CharacterID, nil, map[string]any{"read": read}); err != nil {
				return nil, err
			}
			a.Guard.Record(ctx, "eve_mail_mark", "mail_organize", args, "ok")
			return map[string]any{"status": "done", "mail_id": in.MailID, "read": read}, nil
		})
	})

	type delIn struct {
		MailID       int    `json:"mail_id" jsonschema:"Mail id from eve_mail_list.,minimum=1"`
		Character    string `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
		ConfirmToken string `json:"confirm_token,omitempty" jsonschema:"Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here."`
	}
	addTool(s, &mcp.Tool{
		Name:        "eve_mail_delete",
		Description: "Delete one mail. Permanent — deleted EVE mail cannot be recovered. The preview shows sender, subject and date so the user can confirm.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in delIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *session.Session) (any, error) {
			token, err := a.ResolveCharacter(in.Character)
			if err != nil {
				return nil, err
			}
			header, err := a.ESI.Get(fmt.Sprintf("/characters/%d/mail/%d", token.CharacterID, in.MailID), &token.CharacterID, nil, nil)
			if err != nil {
				return nil, err
			}
			mail := j.Map(header.Data)
			sender, _ := a.Resolver.Name(j.Int(mail["from"]), nil)
			args := map[string]any{"mail_id": in.MailID, "character_id": token.CharacterID}
			preview := map[string]any{
				"action": "Permanently delete a mail", "character": token.CharacterName,
				"subject": mail["subject"], "from": sender, "timestamp": mail["timestamp"],
			}
			blocked, err := a.Guard.Authorize(ctx, "eve_mail_delete", "mail_organize", args, preview, in.ConfirmToken, token.Scopes)
			if err != nil {
				return nil, err
			}
			if blocked != nil {
				return blocked, nil
			}
			if _, err := a.ESI.Delete(fmt.Sprintf("/characters/%d/mail/%d", token.CharacterID, in.MailID), &token.CharacterID, nil, nil); err != nil {
				return nil, err
			}
			a.Guard.Record(ctx, "eve_mail_delete", "mail_organize", args, "ok")
			return map[string]any{"status": "done", "deleted_subject": mail["subject"]}, nil
		})
	})
}

func registerMailSend(s *mcp.Server, a *session.Session) {
	type in struct {
		To           []string `json:"to" jsonschema:"Exact character, corporation or alliance names."`
		Subject      string   `json:"subject" jsonschema:"Mail subject."`
		Body         string   `json:"body" jsonschema:"Mail body text."`
		Character    string   `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
		ApprovedCost int      `json:"approved_cost,omitempty" jsonschema:"ISK you accept paying for CSPA charges. 0 refuses to pay.,minimum=0"`
		ConfirmToken string   `json:"confirm_token,omitempty" jsonschema:"Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here."`
	}
	addTool(s, &mcp.Tool{
		Name:        "eve_mail_send",
		Description: "Send an in-game EVE mail from this character to other players.\n\nThe most consequential tool on this server. The mail cannot be recalled. Show the preview to the user word for word — including the full body — and get an explicit yes before confirming.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in in) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *session.Session) (any, error) {
			token, err := a.ResolveCharacter(in.Character)
			if err != nil {
				return nil, err
			}
			if len(in.To) > 20 {
				return map[string]any{"error": fmt.Sprintf("Refusing to mail %d recipients at once; the cap is 20. Send in smaller batches.", len(in.To))}, nil
			}
			only := []string{"characters", "corporations", "alliances"}
			resolutions, err := a.Resolver.ResolveNames(in.To, nil, only)
			if err != nil {
				return nil, err
			}
			var recipients []map[string]any
			var resolvedNames []string
			var unknown []string
			var ambiguous []names.NameResolution
			seen := map[int]struct{}{}
			for _, asked := range in.To {
				match := resolutions[strings.ToLower(strings.TrimSpace(asked))]
				if match.Chosen == nil {
					unknown = append(unknown, asked)
				} else if match.Ambiguous() {
					ambiguous = append(ambiguous, match)
				} else if _, ok := seen[match.Chosen.ID]; !ok {
					seen[match.Chosen.ID] = struct{}{}
					recipients = append(recipients, map[string]any{
						"recipient_id": match.Chosen.ID, "recipient_type": recipientKeys[match.Chosen.Category],
					})
					resolvedNames = append(resolvedNames, fmt.Sprintf("%s (%s)", match.Chosen.Name, match.Chosen.Kind))
				}
			}
			if len(unknown) > 0 {
				return map[string]any{"error": fmt.Sprintf("Could not resolve recipient(s): %v. Names must match exactly; check them with eve_universe_search. Nothing was sent.", unknown)}, nil
			}
			if len(ambiguous) > 0 {
				var parts []string
				for _, m := range ambiguous {
					parts = append(parts, m.Describe())
				}
				return map[string]any{"error": "Refusing to send — " + strings.Join(parts, "; ") + ". EVE mail cannot be recalled, so confirm which one is meant with eve_universe_search. Nothing was sent."}, nil
			}
			subj, body := in.Subject, in.Body
			if len(subj) > 1000 {
				subj = subj[:1000]
			}
			if len(body) > 10000 {
				body = body[:10000]
			}
			payload := map[string]any{"recipients": recipients, "subject": subj, "body": body, "approved_cost": in.ApprovedCost}
			args := map[string]any{"recipients": recipients, "subject": subj, "body": body, "approved_cost": in.ApprovedCost, "character_id": token.CharacterID}
			preview := map[string]any{
				"action": "SEND AN IN-GAME MAIL — another player will receive this and it cannot be recalled",
				"from":   token.CharacterName, "to": resolvedNames, "subject": subj, "body": body,
				"approved_cspa_cost_isk": in.ApprovedCost,
			}
			blocked, err := a.Guard.Authorize(ctx, "eve_mail_send", "mail_send", args, preview, in.ConfirmToken, token.Scopes)
			if err != nil {
				return nil, err
			}
			if blocked != nil {
				return blocked, nil
			}
			mailID, err := a.ESI.Post(fmt.Sprintf("/characters/%d/mail", token.CharacterID), &token.CharacterID, nil, payload)
			if err != nil {
				return nil, err
			}
			a.Guard.Record(ctx, "eve_mail_send", "mail_send", args, mailID)
			return map[string]any{"status": "sent", "mail_id": mailID, "to": resolvedNames}, nil
		})
	})
}

func registerContacts(s *mcp.Server, a *session.Session) {
	type setIn struct {
		Names        []string `json:"names" jsonschema:"Exact character, corporation or alliance names."`
		Standing     float64  `json:"standing" jsonschema:"-10.0 to 10.0.,minimum=-10,maximum=10"`
		Watched      *bool    `json:"watched,omitempty" jsonschema:"Add to the watch list. Characters only."`
		Character    string   `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
		ConfirmToken string   `json:"confirm_token,omitempty" jsonschema:"Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here."`
	}
	addTool(s, &mcp.Tool{
		Name:        "eve_contacts_set",
		Description: "Add or update contacts with a standing.\n\nA negative standing colours that player red in the overview. Treat it as a visible social act.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in setIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *session.Session) (any, error) {
			token, err := a.ResolveCharacter(in.Character)
			if err != nil {
				return nil, err
			}
			matches, failure, err := resolveContacts(a, in.Names)
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
				if m.Category == "characters" {
					watchable[m.ID] = struct{}{}
				}
			}
			existing, err := a.ESI.GetAllPages(fmt.Sprintf("/characters/%d/contacts", token.CharacterID), &token.CharacterID, nil, 40)
			if err != nil {
				return nil, err
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
			args := map[string]any{"contact_ids": contactIDs, "standing": in.Standing, "watched": watched, "character_id": token.CharacterID}
			preview := map[string]any{
				"action":    "Set contact standings (visible in the character's overview)",
				"character": token.CharacterName, "contacts": resolved, "standing": in.Standing,
				"watched": watched, "new_contacts": len(neu), "updated_contacts": len(updating),
			}
			if watched && len(watchable) != len(contactIDs) {
				preview["watched_note"] = fmt.Sprintf("Only %d of %d are characters; the rest are corporations or alliances, which cannot be watched.", len(watchable), len(contactIDs))
			}
			blocked, err := a.Guard.Authorize(ctx, "eve_contacts_set", "contacts", args, preview, in.ConfirmToken, token.Scopes)
			if err != nil {
				return nil, err
			}
			if blocked != nil {
				return blocked, nil
			}
			appliedU, appliedA := []int{}, []int{}
			path := fmt.Sprintf("/characters/%d/contacts", token.CharacterID)
			run := func(verb string, ids []int, flag bool) error {
				var call func(string, *int, map[string]any, any) (any, error)
				if verb == "update" {
					call = a.ESI.Put
				} else {
					call = a.ESI.Post
				}
				if _, err := call(path, &token.CharacterID, map[string]any{"standing": in.Standing, "watched": flag}, ids); err != nil {
					return err
				}
				if verb == "update" {
					appliedU = append(appliedU, ids...)
				} else {
					appliedA = append(appliedA, ids...)
				}
				a.Guard.Record(ctx, "eve_contacts_set", "contacts", map[string]any{"contact_ids": ids, "standing": in.Standing, "watched": flag, "character_id": token.CharacterID, "phase": verb}, "ok")
				return nil
			}
			ops := []struct {
				verb string
				ids  []int
				flag bool
			}{}
			for _, pair := range []struct {
				verb string
				ids  []int
			}{{"update", updating}, {"add", neu}} {
				if len(pair.ids) == 0 {
					continue
				}
				if !watched {
					ops = append(ops, struct {
						verb string
						ids  []int
						flag bool
					}{pair.verb, pair.ids, false})
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
					ops = append(ops, struct {
						verb string
						ids  []int
						flag bool
					}{pair.verb, yes, true})
				}
				if len(no) > 0 {
					ops = append(ops, struct {
						verb string
						ids  []int
						flag bool
					}{pair.verb, no, false})
				}
			}
			for _, op := range ops {
				if err := run(op.verb, op.ids, op.flag); err != nil {
					if len(appliedU)+len(appliedA) == 0 {
						return nil, err
					}
					status := 0
					if e, ok := err.(esi.Error); ok {
						status = e.Status
					}
					a.Guard.Audit(map[string]any{"event": "partial_write", "tool": "eve_contacts_set", "capability": "contacts", "completed": map[string]any{"updated": appliedU, "added": appliedA}, "error": err.Error()})
					return map[string]any{
						"error": fmt.Sprintf("Partially applied. Standing %v reached %d existing and %d new contact(s) before this failed: %v. Call eve_contacts_set again with the same arguments.", in.Standing, len(appliedU), len(appliedA), err),
						"kind":  "EsiError", "status": status,
					}, nil
				}
			}
			return map[string]any{"status": "done", "contacts": resolved, "standing": in.Standing}, nil
		})
	})

	type delIn struct {
		Names        []string `json:"names" jsonschema:"Exact contact names to remove."`
		Character    string   `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
		ConfirmToken string   `json:"confirm_token,omitempty" jsonschema:"Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here."`
	}
	addTool(s, &mcp.Tool{
		Name:        "eve_contacts_delete",
		Description: "Remove contacts from the character's contact list. Removing a contact drops any standing set on them.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in delIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *session.Session) (any, error) {
			token, err := a.ResolveCharacter(in.Character)
			if err != nil {
				return nil, err
			}
			matches, failure, err := resolveContacts(a, in.Names)
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
			args := map[string]any{"contact_ids": ids, "character_id": token.CharacterID}
			preview := map[string]any{"action": "Delete contacts and the standings set on them", "character": token.CharacterName, "contacts": resolved}
			blocked, err := a.Guard.Authorize(ctx, "eve_contacts_delete", "contacts", args, preview, in.ConfirmToken, token.Scopes)
			if err != nil {
				return nil, err
			}
			if blocked != nil {
				return blocked, nil
			}
			if _, err := a.ESI.Delete(fmt.Sprintf("/characters/%d/contacts", token.CharacterID), &token.CharacterID, map[string]any{"contact_ids": ids}, nil); err != nil {
				return nil, err
			}
			a.Guard.Record(ctx, "eve_contacts_delete", "contacts", args, "ok")
			return map[string]any{"status": "done", "removed": resolved}, nil
		})
	})
}

func registerCalendar(s *mcp.Server, a *session.Session) {
	type in struct {
		EventID      int    `json:"event_id" jsonschema:"Event id from the in-game calendar.,minimum=1"`
		Response     string `json:"response" jsonschema:"accepted, declined, or tentative."`
		Character    string `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
		ConfirmToken string `json:"confirm_token,omitempty" jsonschema:"Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here."`
	}
	addTool(s, &mcp.Tool{
		Name:        "eve_calendar_respond",
		Description: "Respond to a calendar event invitation. The organiser and other invitees see the answer.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in in) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *session.Session) (any, error) {
			token, err := a.ResolveCharacter(in.Character)
			if err != nil {
				return nil, err
			}
			detail, err := a.ESI.Get(fmt.Sprintf("/characters/%d/calendar/%d", token.CharacterID, in.EventID), &token.CharacterID, nil, nil)
			if err != nil {
				return nil, err
			}
			event := j.Map(detail.Data)
			args := map[string]any{"event_id": in.EventID, "response": in.Response, "character_id": token.CharacterID}
			preview := map[string]any{
				"action":    "Respond to a calendar invitation — the organiser is notified",
				"character": token.CharacterName, "event": event["title"], "date": event["date"],
				"owner": event["owner_name"], "response": in.Response,
			}
			blocked, err := a.Guard.Authorize(ctx, "eve_calendar_respond", "calendar", args, preview, in.ConfirmToken, token.Scopes)
			if err != nil {
				return nil, err
			}
			if blocked != nil {
				return blocked, nil
			}
			if _, err := a.ESI.Put(fmt.Sprintf("/characters/%d/calendar/%d", token.CharacterID, in.EventID), &token.CharacterID, nil, map[string]any{"response": in.Response}); err != nil {
				return nil, err
			}
			a.Guard.Record(ctx, "eve_calendar_respond", "calendar", args, "ok")
			return map[string]any{"status": "done", "event": event["title"], "response": in.Response}, nil
		})
	})
}

func resolveContacts(a *session.Session, namesIn []string) ([]names.NameMatch, map[string]any, error) {
	only := []string{"characters", "corporations", "alliances"}
	resolutions, err := a.Resolver.ResolveNames(namesIn, nil, only)
	if err != nil {
		return nil, nil, err
	}
	var matches []names.NameMatch
	var unknown []string
	var ambiguous []names.NameResolution
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
		return nil, map[string]any{"error": fmt.Sprintf("Could not resolve: %v. Names must be exact — check them with eve_universe_search. Nothing was changed.", unknown)}, nil
	}
	if len(ambiguous) > 0 {
		var parts []string
		for _, m := range ambiguous {
			parts = append(parts, m.Describe())
		}
		return nil, map[string]any{"error": "Refusing to act — " + strings.Join(parts, "; ") + ". Confirm which one is meant with eve_universe_search. Nothing was changed."}, nil
	}
	return matches, nil, nil
}

func resolveDestination(a *session.Session, name string, characterID int) (map[string]any, error) {
	order := []string{"stations", "systems"}
	resolved, err := a.Resolver.ResolveNames([]string{name}, order, order)
	if err != nil {
		return nil, err
	}
	match := resolved[strings.ToLower(strings.TrimSpace(name))]
	if match.Chosen != nil {
		out := map[string]any{"id": match.Chosen.ID, "name": match.Chosen.Name, "kind": match.Chosen.Kind}
		if match.Ambiguous() {
			out["ambiguity"] = match.Describe()
		}
		return out, nil
	}
	search, err := a.ESI.Get(fmt.Sprintf("/characters/%d/search", characterID), &characterID, map[string]any{
		"categories": []string{"structure"}, "search": name, "strict": false,
	}, nil)
	if err != nil {
		return nil, err
	}
	structures := j.Slice(j.Map(search.Data)["structure"])
	if len(structures) > 0 {
		sid := j.Int(structures[0])
		sname, _ := a.Resolver.Name(sid, &characterID)
		return map[string]any{"id": sid, "name": sname, "kind": "structure"}, nil
	}
	return map[string]any{"error": fmt.Sprintf("No system, station or visible structure is named exactly %q. Check the spelling with eve_universe_search.", name)}, nil
}

func resolveEntity(a *session.Session, name string, characterID int, kind string) (map[string]any, error) {
	if _, err := strconv.Atoi(strings.TrimSpace(name)); err == nil {
		id := j.Int(name)
		n, _ := a.Resolver.Name(id, &characterID)
		return map[string]any{"id": id, "name": n, "kind": "id"}, nil
	}
	var prefer, only []string
	if kind == "market" {
		prefer = []string{"inventory_types"}
		only = prefer
	} else {
		prefer = []string{"characters", "corporations", "alliances", "inventory_types", "systems", "stations"}
	}
	resolved, err := a.Resolver.ResolveNames([]string{name}, prefer, only)
	if err != nil {
		return nil, err
	}
	match := resolved[strings.ToLower(strings.TrimSpace(name))]
	if match.Chosen != nil {
		out := map[string]any{"id": match.Chosen.ID, "name": match.Chosen.Name, "kind": match.Chosen.Kind}
		if match.Ambiguous() {
			out["ambiguity"] = match.Describe()
		}
		return out, nil
	}
	return map[string]any{"error": fmt.Sprintf("Could not resolve %q for the %s window. Check the exact name with eve_universe_search.", name, kind)}, nil
}
