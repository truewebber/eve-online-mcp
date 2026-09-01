package eve

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/truewebber/eve-online-mcp/internal/j"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

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
			return eveUISetWaypoint(ctx, a, in.Destination, in.ClearOtherWaypoints, in.AddToBeginning, in.ConfirmToken)
		})
	})
}

func eveUISetWaypoint(ctx context.Context, a *session.Session, destination string, clearOther, addToBeginning *bool, confirmToken string) (any, error) {
	token, err := a.Character(ctx)
	if err != nil {
		return nil, wrap("eveUISetWaypoint", err)
	}
	target, err := resolveDestination(ctx, a, destination, token.CharacterID)
	if err != nil {
		return nil, err
	}
	if _, ok := target[fError]; ok {
		return target, nil
	}
	clearOthers := boolDef(clearOther, true)
	add := boolDef(addToBeginning, false)
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
	blocked, err := a.Guard.Authorize(ctx, "eve_ui_set_waypoint", "waypoint", args, preview, confirmToken, token.Scopes)
	if err != nil {
		return nil, wrap("eveUISetWaypoint", err)
	}
	if blocked.Required != nil {
		return blocked.Required, nil
	}
	_, err = a.ESI.Post(ctx, "/ui/autopilot/waypoint", &token.CharacterID, map[string]any{
		"destination_id": target["id"], "clear_other_waypoints": clearOthers, "add_to_beginning": add,
	}, nil)
	recordWrite(ctx, a, "eve_ui_set_waypoint", "waypoint", args, err)
	if err != nil {
		return nil, wrap("eveUISetWaypoint", err)
	}

	return map[string]any{fStatus: vDone, "waypoint_set_to": target[fName], fNote: clientCaveat}, nil
}

func registerOpenWindow(s *mcp.Server) {
	addTool(s, &mcp.Tool{
		Name:        "eve_ui_open_window",
		Description: "Open a window in the running game client.\n\nGood for handing something off to the player. Changes nothing in game and costs nothing.\n\nA `window` outside the three values is refused with the list of accepted ones — it never falls back to one of them. For a pre-filled mail window, that is eve_mail_compose.",
	}, sessionTool(eveUIOpenWindow))
}

type openWindowIn struct {
	Window       string `json:"window"                  jsonschema:"'market' opens market details for an item. 'info' opens Show Info. 'contract' opens one contract."`
	Target       string `json:"target"                  jsonschema:"For market, an exact item name. For info, an exact name of any entity. For contract, the numeric contract_id."`
	ConfirmToken string `json:"confirm_token,omitempty" jsonschema:"Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here."`
}

func eveUIOpenWindow(ctx context.Context, a *session.Session, in openWindowIn) (any, error) {
	kind, err := requireEnum(fWindow, in.Window, windowMarket, windowInfo, windowContract)
	if err != nil {
		return nil, err
	}
	token, err := a.Character(ctx)
	if err != nil {
		return nil, wrap("eveUIOpenWindow", err)
	}
	plan, err := planOpenWindow(ctx, a, kind, in.Target, token.CharacterID)
	if err != nil {
		return nil, err
	}
	if plan.refuse != nil {
		return plan.refuse, nil
	}
	args := map[string]any{fWindow: kind, "params": plan.params, fCharacterID: token.CharacterID}
	preview := map[string]any{
		fAction:    fmt.Sprintf("Open the %s window in the game client", kind),
		fCharacter: token.CharacterName, "target": plan.label,
	}
	if plan.resolved != nil {
		if amb := j.Str(plan.resolved["ambiguity"]); amb != "" {
			preview["ambiguous_name"] = amb + " — this opens the first. Cancel and use eve_universe_search if the other one was meant."
		}
	}
	blocked, err := a.Guard.Authorize(ctx, "eve_ui_open_window", "openwindow", args, preview, in.ConfirmToken, token.Scopes)
	if err != nil {
		return nil, wrap("eveUIOpenWindow", err)
	}
	if blocked.Required != nil {
		return blocked.Required, nil
	}
	_, err = a.ESI.Post(ctx, plan.path, &token.CharacterID, plan.params, nil)
	recordWrite(ctx, a, "eve_ui_open_window", "openwindow", args, err)
	if err != nil {
		return nil, wrap("eveUIOpenWindow", err)
	}

	return map[string]any{fStatus: vDone, "opened": plan.label, fNote: clientCaveat}, nil
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
		fieldCategories: []string{fStructure}, "search": name, fStrict: false,
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

	return unresolvedResult(name), nil
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
	if kind == windowMarket {
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

	return unresolvedResult(name), nil
}

type windowPlan struct {
	path     string
	params   map[string]any
	label    string
	resolved map[string]any
	refuse   map[string]any
}

func planOpenWindow(ctx context.Context, a *session.Session, kind, target string, characterID int) (windowPlan, error) {
	switch kind {
	case windowContract:
		return planContractWindow(target)
	case windowMarket:
		return planNamedWindow(ctx, a, kind, target, characterID, "/ui/openwindow/marketdetails", fTypeID)
	case windowInfo:
		return planNamedWindow(ctx, a, kind, target, characterID, "/ui/openwindow/information", "target_id")
	default:
		return windowPlan{}, ValidationError{Field: fWindow, Invariant: enumInvariant(windowMarket, windowInfo, windowContract)}
	}
}

func planContractWindow(target string) (windowPlan, error) {
	id, ok := parseContractID(target)
	if !ok {
		return windowPlan{}, ValidationError{Field: "target", Invariant: "must be the numeric contract_id from eve_market_contracts"}
	}

	return windowPlan{
		path:   "/ui/openwindow/contract",
		params: map[string]any{"contract_id": id},
		label:  "contract #" + strings.TrimSpace(target),
	}, nil
}

func planNamedWindow(ctx context.Context, a *session.Session, kind, target string, characterID int, path, idKey string) (windowPlan, error) {
	resolved, err := resolveEntity(ctx, a, target, characterID, kind)
	if err != nil {
		return windowPlan{}, err
	}
	if _, ok := resolved[fError]; ok {
		return windowPlan{refuse: resolved}, nil
	}
	label := j.Str(resolved[fName])
	if k := j.Str(resolved[fKind]); k != "" && k != "id" {
		label = fmt.Sprintf("%s (%s)", label, k)
	}

	return windowPlan{
		path: path, params: map[string]any{idKey: resolved["id"]},
		label: label, resolved: resolved,
	}, nil
}

func parseContractID(s string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, false
	}

	return n, true
}
