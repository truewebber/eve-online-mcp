package eve

import (
	"context"
	"fmt"
	"strings"

	"github.com/truewebber/eve-online-mcp/internal/j"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

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
	FittingID    int    `json:"fitting_id"              jsonschema:"Fitting id from eve_fitting_list."`
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
		return map[string]any{
			fError:     "No fitting with that id. Call eve_fitting_list.",
			fKind:      kindError,
			fFittingID: in.FittingID,
		}, nil
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
		return fittingResolved{fail: unresolvedResult(ship)}, nil
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
		return fittingResolved{fail: unresolvedResult(unknown...)}, nil
	}

	return fittingResolved{shipID: shipID, items: items, previewMods: previewMods}, nil
}
