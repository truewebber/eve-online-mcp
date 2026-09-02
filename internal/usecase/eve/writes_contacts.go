package eve

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/domain/write"
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
		Name:        write.ToolContactsSet,
		Description: "Add or update contacts with a standing.\n\nA negative standing colours that player red in the overview. Treat it as a visible social act.",
	}, sessionTool(eveContactsSet))
	addTool(s, &mcp.Tool{
		Name:        write.ToolContactsDelete,
		Description: "Remove contacts from this character's contact list.\n\nDeleting a contact also clears any standing set on them. That is a visible social change, so confirm the names before the second call. It does not block or report anyone.",
	}, sessionTool(eveContactsDelete))
}

func eveContactsSet(ctx context.Context, a *session.Session, in contactsSetIn) (any, error) {
	token, err := a.Character(ctx)
	if err != nil {
		return nil, wrap("eveContactsSet", err)
	}
	found, err := resolveContacts(ctx, a, in.Names)
	if err != nil {
		return nil, err
	}
	if found.failure != nil {
		return found.failure, nil
	}
	existing, err := a.ESI.GetAllPages(ctx, esiPath("characters", esiID(token.CharacterID), "contacts"), &token.CharacterID, nil, pagesESI)
	if err != nil {
		return nil, wrap("eveContactsSet", err)
	}
	plan := planContactSet(found.matches, knownContactIDs(existing.Data))
	watched := boolDef(in.Watched, false)
	args := map[string]any{fContactIDs: plan.ids, fStanding: in.Standing, fWatched: watched, fCharacterID: token.CharacterID}
	preview := contactSetPreview(token.CharacterName, plan, in.Standing, watched)
	blocked, err := a.Guard.Authorize(ctx, write.Authz{
		Tool: write.ToolContactsSet, Capability: write.CapContacts,
		Args: args, Preview: preview, Token: in.ConfirmToken, Scopes: token.Scopes,
	})
	if err != nil {
		return nil, wrap("eveContactsSet", err)
	}
	if blocked.Required != nil {
		return blocked.Required, nil
	}
	applied, err := applyContactOps(ctx, a, contactApplyIn{
		characterID: token.CharacterID, standing: in.Standing,
		ops: buildContactOps(plan.updating, plan.neu, watched, plan.watchable),
	})
	if err != nil {
		return nil, err
	}
	if applied.fail != nil {
		return applied.fail, nil
	}

	return map[string]any{fStatus: vDone, fContacts: plan.names, fStanding: in.Standing}, nil
}

type contactPlan struct {
	ids, updating, neu []int
	names              []string
	watchable          map[int]struct{}
}

func knownContactIDs(data any) map[int]struct{} {
	known := map[int]struct{}{}
	for _, c := range j.Maps(data) {
		known[j.Int(c["contact_id"])] = struct{}{}
	}

	return known
}

func planContactSet(matches []esi.NameMatch, known map[int]struct{}) contactPlan {
	out := contactPlan{watchable: map[int]struct{}{}}
	for _, m := range matches {
		out.ids = append(out.ids, m.ID)
		out.names = append(out.names, m.Name)
		if m.Category == fCharacters {
			out.watchable[m.ID] = struct{}{}
		}
		if _, ok := known[m.ID]; ok {
			out.updating = append(out.updating, m.ID)
		} else {
			out.neu = append(out.neu, m.ID)
		}
	}

	return out
}

func contactSetPreview(character string, plan contactPlan, standing float64, watched bool) map[string]any {
	preview := map[string]any{
		fAction:    "Set contact standings (visible in the character's overview)",
		fCharacter: character, fContacts: plan.names, fStanding: standing,
		fWatched: watched, "new_contacts": len(plan.neu), "updated_contacts": len(plan.updating),
	}
	if watched && len(plan.watchable) != len(plan.ids) {
		preview["watched_note"] = fmt.Sprintf("Only %d of %d are characters; the rest are corporations or alliances, which cannot be watched.", len(plan.watchable), len(plan.ids))
	}

	return preview
}

func eveContactsDelete(ctx context.Context, a *session.Session, in contactsDeleteIn) (any, error) {
	token, err := a.Character(ctx)
	if err != nil {
		return nil, wrap("eveContactsDelete", err)
	}
	found, err := resolveContacts(ctx, a, in.Names)
	if err != nil {
		return nil, err
	}
	if found.failure != nil {
		return found.failure, nil
	}
	matches := found.matches
	var ids []int
	var resolved []string
	for _, m := range matches {
		ids = append(ids, m.ID)
		resolved = append(resolved, m.Name)
	}
	args := map[string]any{fContactIDs: ids, fCharacterID: token.CharacterID}
	preview := map[string]any{fAction: "Delete contacts and the standings set on them", fCharacter: token.CharacterName, fContacts: resolved}
	blocked, err := a.Guard.Authorize(ctx, write.Authz{
		Tool: write.ToolContactsDelete, Capability: write.CapContacts,
		Args: args, Preview: preview, Token: in.ConfirmToken, Scopes: token.Scopes,
	})
	if err != nil {
		return nil, wrap("eveContactsDelete", err)
	}
	if blocked.Required != nil {
		return blocked.Required, nil
	}
	_, err = a.ESI.Delete(ctx, esiPath("characters", esiID(token.CharacterID), "contacts"), &token.CharacterID, map[string]any{fContactIDs: ids}, nil)
	recordWrite(ctx, a, writeLog{tool: write.ToolContactsDelete, capability: write.CapContacts, args: args, err: err})
	if err != nil {
		return nil, wrap("eveContactsDelete", err)
	}

	return map[string]any{fStatus: vDone, "removed": resolved}, nil
}

type contactResolved struct {
	matches []esi.NameMatch
	failure map[string]any
}

func resolveContacts(ctx context.Context, a *session.Session, namesIn []string) (contactResolved, error) {
	only := []string{fCharacters, fCorporations, fAlliances}
	resolutions, err := a.Resolver.ResolveNames(ctx, namesIn, nil, only)
	if err != nil {
		return contactResolved{}, wrap("resolveContacts", err)
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
		return contactResolved{failure: unresolvedResult(unknown...)}, nil
	}
	if len(ambiguous) > 0 {
		var parts []string
		for _, m := range ambiguous {
			parts = append(parts, m.Describe())
		}

		return contactResolved{failure: ambiguousResult(parts)}, nil
	}

	return contactResolved{matches: matches}, nil
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

type contactApplyIn struct {
	characterID int
	standing    float64
	ops         []contactOp
}

func applyContactOps(ctx context.Context, a *session.Session, in contactApplyIn) (contactApplyResult, error) {
	appliedU, appliedA := []int{}, []int{}
	path := esiPath("characters", esiID(in.characterID), "contacts")
	for _, op := range in.ops {
		err := runContactOp(ctx, a, contactOpRun{characterID: in.characterID, path: path, standing: in.standing, op: op})
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
				"standing": in.standing,
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

type contactOpRun struct {
	characterID int
	path        esi.Route
	standing    float64
	op          contactOp
}

func runContactOp(ctx context.Context, a *session.Session, in contactOpRun) error {
	var call func(context.Context, esi.Route, *int, map[string]any, any) (any, error)
	if in.op.verb == vUpdate {
		call = a.ESI.Put
	} else {
		call = a.ESI.Post
	}
	args := map[string]any{fContactIDs: in.op.ids, fStanding: in.standing, fWatched: in.op.flag, fCharacterID: in.characterID, "phase": in.op.verb}
	_, err := call(ctx, in.path, &in.characterID, map[string]any{fStanding: in.standing, fWatched: in.op.flag}, in.op.ids)
	recordWrite(ctx, a, writeLog{tool: write.ToolContactsSet, capability: write.CapContacts, args: args, err: err})
	if err != nil {
		return err
	}

	return nil
}
