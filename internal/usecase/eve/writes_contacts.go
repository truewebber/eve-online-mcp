package eve

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/j"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type contactsSetIn struct {
	Names        []string `json:"names"                   jsonschema:"Exact character, corporation or alliance names."`
	Standing     float64  `json:"standing"                jsonschema:"-10.0 to 10.0."`
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
		return nil, unresolvedResult(unknown...), nil
	}
	if len(ambiguous) > 0 {
		var parts []string
		for _, m := range ambiguous {
			parts = append(parts, m.Describe())
		}

		return nil, ambiguousResult(parts), nil
	}

	return matches, nil, nil
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
				fError:     "Partially applied. Call eve_contacts_set again with the same arguments.",
				fKind:      "EsiError",
				fStatus:    status,
				"standing": standing,
				"updated":  len(appliedU),
				"added":    len(appliedA),
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
