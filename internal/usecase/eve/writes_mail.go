package eve

import (
	"context"
	"fmt"
	"strings"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/domain/write"
	"github.com/truewebber/eve-online-mcp/internal/j"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mailMarkIn struct {
	MailID       int    `json:"mail_id"                 jsonschema:"Mail id from eve_mail_list."`
	Read         *bool  `json:"read,omitempty"          jsonschema:"True marks it read, False marks it unread. Default true."`
	ConfirmToken string `json:"confirm_token,omitempty" jsonschema:"Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here."`
}

type mailDeleteIn struct {
	MailID       int    `json:"mail_id"                 jsonschema:"Mail id from eve_mail_list."`
	ConfirmToken string `json:"confirm_token,omitempty" jsonschema:"Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here."`
}

type mailSendIn struct {
	To           []string `json:"to"                      jsonschema:"Exact character, corporation or alliance names."`
	Subject      string   `json:"subject"                 jsonschema:"Mail subject."`
	Body         string   `json:"body"                    jsonschema:"Mail body text."`
	ApprovedCost int      `json:"approved_cost,omitempty" jsonschema:"ISK you accept paying for CSPA charges. 0 refuses to pay."`
	ConfirmToken string   `json:"confirm_token,omitempty" jsonschema:"Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here."`
}

type mailComposeIn struct {
	To           []string `json:"to"                      jsonschema:"Exact character names, up to 50."`
	ToGroup      string   `json:"to_group,omitempty"      jsonschema:"One corporation, alliance or mailing list name. EVE allows a single mailing group per mail, so this is one name, not a list."`
	Subject      string   `json:"subject"                 jsonschema:"Mail subject, up to 1000 characters."`
	Body         string   `json:"body"                    jsonschema:"Mail body text, up to 10000 characters."`
	ConfirmToken string   `json:"confirm_token,omitempty" jsonschema:"Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here."`
}

type mailRecipients struct {
	recipients    []map[string]any
	resolvedNames []string
	characterIDs  []int
	fail          map[string]any
}

type composeRecipients struct {
	characterIDs []int
	names        []string
	groupID      int
	groupName    string
	fail         map[string]any
}

func registerMailOrganize(s *mcp.Server) {
	addTool(s, &mcp.Tool{
		Name:        write.ToolMailMark,
		Description: "Change the read flag on one mail. This does not return the mail's contents — use eve_mail_read for that. Unread mail is what eve_mail_list can filter on; this is how a mail leaves that list.",
	}, sessionTool(eveMailMark))
	addTool(s, &mcp.Tool{
		Name:        write.ToolMailDelete,
		Description: "Delete one mail. Permanent — deleted EVE mail cannot be recovered. The preview shows sender, subject and date so the user can confirm.",
	}, sessionTool(eveMailDelete))
}

func registerMailSend(s *mcp.Server) {
	addTool(s, &mcp.Tool{
		Name:        write.ToolMailSend,
		Description: "Send an in-game EVE mail from this character to other players.\n\nThe most consequential tool on this server. The mail cannot be recalled. Show the preview to the user word for word — the full body and the priced CSPA charge — and get an explicit yes before confirming. Capped at 5 mails per hour; eve_auth_status reports how many are left. If the user is in front of their client, eve_mail_compose does the same job and leaves the sending to them.",
	}, sessionTool(eveMailSend))
}

func registerMailCompose(s *mcp.Server) {
	addTool(s, &mcp.Tool{
		Name:        write.ToolMailCompose,
		Description: "Open a pre-filled mail in the player's client without sending it.\n\nThe safe half of mail: recipients, subject and body are filled in, the compose window opens in the running game client, and the Send button stays the player's. Nothing leaves the character, no CSPA charge is possible, and it does not count against the hourly send cap. Prefer it over eve_mail_send whenever the user is at their keyboard — eve_mail_send is for a mail that has to go out without them touching the client.\n\nNeeds the EVE client logged in on this character. There is no way to tell from here whether it is, so report that the window was requested, never that a mail was delivered.",
	}, sessionTool(eveMailCompose))
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
	blocked, err := a.Guard.Authorize(ctx, write.Authz{
		Tool: write.ToolMailMark, Capability: write.CapMailOrganize,
		Args: args, Preview: preview, Token: in.ConfirmToken, Scopes: token.Scopes,
	})
	if err != nil {
		return nil, wrap("eveMailMark", err)
	}
	if blocked.Required != nil {
		return blocked.Required, nil
	}
	_, err = a.ESI.Put(ctx, esiPath("characters", esiID(token.CharacterID), "mail", esiID(in.MailID)), &token.CharacterID, nil, map[string]any{fRead: read})
	recordWrite(ctx, a, writeLog{tool: write.ToolMailMark, capability: write.CapMailOrganize, args: args, err: err})
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
	blocked, err := a.Guard.Authorize(ctx, write.Authz{
		Tool: write.ToolMailDelete, Capability: write.CapMailOrganize,
		Args: args, Preview: preview, Token: in.ConfirmToken, Scopes: token.Scopes,
	})
	if err != nil {
		return nil, wrap("eveMailDelete", err)
	}
	if blocked.Required != nil {
		return blocked.Required, nil
	}
	_, err = a.ESI.Delete(ctx, esiPath("characters", esiID(token.CharacterID), "mail", esiID(in.MailID)), &token.CharacterID, nil, nil)
	recordWrite(ctx, a, writeLog{tool: write.ToolMailDelete, capability: write.CapMailOrganize, args: args, err: err})
	if err != nil {
		return nil, wrap("eveMailDelete", err)
	}

	return map[string]any{fStatus: vDone, "deleted_subject": mail[fSubject]}, nil
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
	charge, err := priceCSPA(ctx, a, token.CharacterID, resolved.characterIDs)
	if err != nil {
		return nil, err
	}
	if charge > float64(in.ApprovedCost) {
		return nil, CSPAExceedsError{Cost: charge, Approved: in.ApprovedCost}
	}
	clipped := clipMail(in.Subject, in.Body)
	payload := map[string]any{fRecipients: resolved.recipients, fSubject: clipped.subject, fBody: clipped.body, fApprovedCost: in.ApprovedCost}
	args := map[string]any{fRecipients: resolved.recipients, fSubject: clipped.subject, fBody: clipped.body, fApprovedCost: in.ApprovedCost, fCharacterID: token.CharacterID}
	preview := map[string]any{
		fAction: "SEND AN IN-GAME MAIL — another player will receive this and it cannot be recalled",
		fFrom:   token.CharacterName, "to": resolved.resolvedNames, fSubject: clipped.subject, fBody: clipped.body,
		"approved_cspa_cost_isk": in.ApprovedCost,
		"priced_cspa_cost_isk":   charge,
	}
	blocked, err := a.Guard.Authorize(ctx, write.Authz{
		Tool: write.ToolMailSend, Capability: write.CapMailSend,
		Args: args, Preview: preview, Token: in.ConfirmToken, Scopes: token.Scopes,
	})
	if err != nil {
		return nil, wrap("eveMailSend", err)
	}
	if blocked.Required != nil {
		return blocked.Required, nil
	}
	mailID, err := a.ESI.Post(ctx, esiPath("characters", esiID(token.CharacterID), "mail"), &token.CharacterID, nil, payload)
	recordWrite(ctx, a, writeLog{tool: write.ToolMailSend, capability: write.CapMailSend, args: args, err: err})
	if err != nil {
		return nil, wrap("eveMailSend", err)
	}

	return map[string]any{fStatus: "sent", fMailID: mailID, "to": resolved.resolvedNames}, nil
}

func eveMailCompose(ctx context.Context, a *session.Session, in mailComposeIn) (any, error) {
	if err := rejectComposeBounds(in); err != nil {
		return nil, err
	}
	token, err := a.Character(ctx)
	if err != nil {
		return nil, wrap("eveMailCompose", err)
	}
	resolved, err := resolveComposeRecipients(ctx, a, in.To, in.ToGroup)
	if err != nil {
		return nil, err
	}
	if resolved.fail != nil {
		return resolved.fail, nil
	}
	body := composeWindowBody(resolved, in.Subject, in.Body)
	args := map[string]any{
		fRecipients: resolved.characterIDs, fSubject: in.Subject, fBody: in.Body,
		fCharacterID: token.CharacterID,
	}
	if resolved.groupID != 0 {
		args["to_corp_or_alliance_id"] = resolved.groupID
	}
	preview := composePreview(token.CharacterName, resolved, in.Subject, in.Body)
	blocked, err := a.Guard.Authorize(ctx, write.Authz{
		Tool: write.ToolMailCompose, Capability: write.CapOpenWindow,
		Args: args, Preview: preview, Token: in.ConfirmToken, Scopes: token.Scopes,
	})
	if err != nil {
		return nil, wrap("eveMailCompose", err)
	}
	if blocked.Required != nil {
		return blocked.Required, nil
	}
	_, err = a.ESI.Post(ctx, esi.Path("/ui/openwindow/newmail"), &token.CharacterID, nil, body)
	recordWrite(ctx, a, writeLog{tool: write.ToolMailCompose, capability: write.CapOpenWindow, args: args, err: err})
	if err != nil {
		return nil, wrap("eveMailCompose", err)
	}

	return map[string]any{
		fStatus: vDone, "opened": "compose window",
		fNote: clientCaveat + " The window was requested; this did not send a mail.",
	}, nil
}

func rejectComposeBounds(in mailComposeIn) error {
	if len(in.To) == 0 {
		return ValidationError{Field: "to", Invariant: invariantRequired}
	}
	if len(in.To) > mailComposeRecipientsMax {
		return ValidationError{Field: "to", Invariant: "must have at most 50 names"}
	}
	if len(in.Subject) > mailSubjectMax {
		return ValidationError{Field: fSubject, Invariant: "must be at most 1000 characters"}
	}
	if len(in.Body) > mailBodyMax {
		return ValidationError{Field: fBody, Invariant: "must be at most 10000 characters"}
	}

	return nil
}

func composeWindowBody(resolved composeRecipients, subject, body string) map[string]any {
	out := map[string]any{fRecipients: resolved.characterIDs, fSubject: subject, fBody: body}
	if resolved.groupID != 0 {
		out["to_corp_or_alliance_id"] = resolved.groupID
	}

	return out
}

func composePreview(character string, resolved composeRecipients, subject, body string) map[string]any {
	preview := map[string]any{
		fAction:    "Open a pre-filled mail compose window in the game client",
		fCharacter: character, "to": resolved.names, fSubject: subject, fBody: body,
		fNote: "The player still has to press Send. This does not send a mail.",
	}
	if resolved.groupName != "" {
		preview["to_group"] = resolved.groupName
	}

	return preview
}

type mailClip struct {
	subject, body string
}

func clipMail(subject, body string) mailClip {
	if len(subject) > mailSubjectMax {
		subject = subject[:mailSubjectMax]
	}
	if len(body) > mailBodyMax {
		body = body[:mailBodyMax]
	}

	return mailClip{subject: subject, body: body}
}

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

func resolveMailRecipients(ctx context.Context, a *session.Session, to []string) (mailRecipients, error) {
	only := []string{fCharacters, fCorporations, fAlliances}
	resolutions, err := a.Resolver.ResolveNames(ctx, to, nil, only)
	if err != nil {
		return mailRecipients{}, wrap("resolveMailRecipients", err)
	}
	var recipients []map[string]any
	var resolvedNames []string
	var characterIDs []int
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
			if match.Chosen.Category == fCharacters {
				characterIDs = append(characterIDs, match.Chosen.ID)
			}
		}
	}
	if fail := nameResolveFail(unknown, ambiguous); fail != nil {
		return mailRecipients{fail: fail}, nil
	}

	return mailRecipients{recipients: recipients, resolvedNames: resolvedNames, characterIDs: characterIDs}, nil
}

func resolveComposeRecipients(ctx context.Context, a *session.Session, to []string, toGroup string) (composeRecipients, error) {
	resolutions, err := a.Resolver.ResolveNames(ctx, to, nil, []string{fCharacters})
	if err != nil {
		return composeRecipients{}, wrap("resolveComposeRecipients", err)
	}
	var ids []int
	var names []string
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
			ids = append(ids, match.Chosen.ID)
			names = append(names, match.Chosen.Name)
		}
	}
	if fail := nameResolveFail(unknown, ambiguous); fail != nil {
		return composeRecipients{fail: fail}, nil
	}
	out := composeRecipients{characterIDs: ids, names: names}
	if strings.TrimSpace(toGroup) == "" {
		return out, nil
	}
	group, err := resolveMailRecipients(ctx, a, []string{toGroup})
	if err != nil {
		return composeRecipients{}, err
	}
	if group.fail != nil {
		return composeRecipients{fail: group.fail}, nil
	}
	if len(group.recipients) != 1 {
		return composeRecipients{fail: unresolvedResult(toGroup)}, nil
	}
	kind := j.Str(group.recipients[0]["recipient_type"])
	if kind != fCorporation && kind != fAlliance {
		return composeRecipients{fail: unresolvedResult(toGroup)}, nil
	}
	out.groupID = j.Int(group.recipients[0]["recipient_id"])
	out.groupName = group.resolvedNames[0]

	return out, nil
}

func nameResolveFail(unknown []string, ambiguous []esi.NameResolution) map[string]any {
	if len(unknown) > 0 {
		return unresolvedResult(unknown...)
	}
	if len(ambiguous) == 0 {
		return nil
	}
	var parts []string
	for _, m := range ambiguous {
		parts = append(parts, m.Describe())
	}

	return ambiguousResult(parts)
}

func priceCSPA(ctx context.Context, a *session.Session, characterID int, ids []int) (float64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	if len(ids) > cspaRecipientsMax {
		return 0, ValidationError{Field: "to", Invariant: "must have at most 100 character recipients to price CSPA"}
	}
	body, err := a.ESI.Post(ctx, esiPath("characters", esiID(characterID), "cspa"), &characterID, nil, ids)
	if err != nil {
		return 0, PreviewReadError{Read: "CSPA charge", err: wrap("priceCSPA", err)}
	}
	charge, ok := cspaCharge(body)
	if !ok {
		return 0, wrap("priceCSPA", ErrCSPAUnpriced)
	}

	return charge, nil
}

func cspaCharge(body any) (float64, bool) {
	switch body.(type) {
	case float64, float32, int, int64, int32:
		return j.Float(body), true
	default:
		return 0, false
	}
}
