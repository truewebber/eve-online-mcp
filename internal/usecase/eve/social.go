package eve

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/truewebber/eve-online-mcp/internal/domain/j"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	tagRe = regexp.MustCompile(`<[^>]+>`)
	brRe  = regexp.MustCompile(`(?i)<br\s*/?>`)
)

type mailListIn struct {
	Character      string `json:"character,omitempty"       jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
	UnreadOnly     *bool  `json:"unread_only,omitempty"     jsonschema:"Only list mail that has not been read yet."`
	Limit          int    `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

type mailReadIn struct {
	MailID    int    `json:"mail_id"             jsonschema:"Mail id from eve_mail_list.,minimum=1"`
	Character string `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
}

type notesIn struct {
	Character      string `json:"character,omitempty"       jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
	Limit          int    `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

type kmIn struct {
	Character      string `json:"character,omitempty"       jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
	Limit          int    `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

type fitIn struct {
	Character      string `json:"character,omitempty"       jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
	Limit          int    `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

func registerSocial(s *mcp.Server) {
	addTool(s, &mcp.Tool{
		Name:        "eve_mail_list",
		Description: "Mail headers only — sender, subject, date, read state. Bodies are not included.\n\nTo read the actual text of one mail, follow up with eve_mail_read using the mail_id from here.\n\nReturns: unread count, mails[] with mail_id for follow-up.",
	}, sessionTool(eveMailList))
	addTool(s, &mcp.Tool{
		Name:        "eve_mail_read",
		Description: "Fetch the full body text of one mail.\n\nRead-only: this does not mark the mail as read in game. Use eve_mail_mark for that.\n\nReturns: from, to[], subject, timestamp, body (markup stripped).",
	}, sessionTool(eveMailRead))
	addTool(s, &mcp.Tool{
		Name:        "eve_social_notifications",
		Description: "In-game notifications: structure attacks, war decs, corp and contract events.\n\nThis is where genuinely time-critical things surface. The detail field is raw YAML with unresolved numeric ids.\n\nReturns: unread count, notifications[] newest first.",
	}, sessionTool(eveSocialNotifications))
	addTool(s, &mcp.Tool{
		Name:        "eve_social_killmails",
		Description: "Recent kills and losses involving this character.\n\nhull_value covers the ship hull only. Each row carries a zkillboard link.\n\nReturns: kills, losses, killmails[] newest first.",
	}, sessionTool(eveSocialKillmails))
	addTool(s, &mcp.Tool{
		Name:        "eve_fitting_list",
		Description: "Saved ship fittings with their module lists.\n\nIn concise mode module lists are omitted. Returns: fittings[] with fitting_id (needed by eve_fitting_delete).",
	}, sessionTool(eveFittingList))
}

func eveMailList(ctx context.Context, a *session.Session, in mailListIn) (any, error) {
	token, err := a.ResolveCharacter(ctx, in.Character)
	if err != nil {
		return nil, wrap("eveMailList", err)
	}
	if err := a.RequireScope(token, "esi-mail.read_mail.v1", "mail"); err != nil {
		return nil, wrap("eveMailList", err)
	}
	cid := token.CharacterID
	result, err := a.ESI.Get(ctx, fmt.Sprintf("/characters/%d/mail", cid), &cid, nil, nil)
	if err != nil {
		return nil, wrap("eveMailList", err)
	}
	mails := j.Maps(result.Data)
	unread := 0
	for _, m := range mails {
		if !j.Bool(m["is_read"]) {
			unread++
		}
	}
	if boolDef(in.UnreadOnly, false) {
		var filtered []map[string]any
		for _, m := range mails {
			if !j.Bool(m["is_read"]) {
				filtered = append(filtered, m)
			}
		}
		mails = filtered
	}
	if len(mails) == 0 {
		return map[string]any{fCharacter: token.CharacterName, "mails": []any{}, fNote: "Nothing to show."}, nil
	}
	senders := map[int]struct{}{}
	for _, m := range mails {
		if j.Int(m[fFrom]) != 0 {
			senders[j.Int(m[fFrom])] = struct{}{}
		}
	}
	names, err := a.Resolver.Names(ctx, setToList(senders), nil)
	if err != nil {
		return nil, wrap("eveMailList", err)
	}
	sort.Slice(mails, func(i, k int) bool { return j.Str(mails[i][fTimestamp]) > j.Str(mails[k][fTimestamp]) })
	var rows []map[string]any
	for _, m := range mails {
		from := names[j.Int(m[fFrom])]
		if from == "" {
			from = fmt.Sprintf("#%v", m[fFrom])
		}
		rows = append(rows, map[string]any{
			fMailID: m[fMailID], fFrom: from, fSubject: m[fSubject],
			fTimestamp: m[fTimestamp], fRead: j.Bool(m["is_read"]), "labels": m["labels"],
		})
	}
	visible, meta := page(rows, limitOr(in.Limit, limitDefault), "")

	return merge(map[string]any{
		fCharacter: token.CharacterName, fUnread: unread, fDataAge: result.StaleNote(),
		"mails": project(visible, []string{fMailID, fFrom, fSubject, fTimestamp, fRead}, concise(in.ResponseFormat)),
	}, meta), nil
}

func eveMailRead(ctx context.Context, a *session.Session, in mailReadIn) (any, error) {
	token, err := a.ResolveCharacter(ctx, in.Character)
	if err != nil {
		return nil, wrap("eveMailRead", err)
	}
	if err := a.RequireScope(token, "esi-mail.read_mail.v1", "mail"); err != nil {
		return nil, wrap("eveMailRead", err)
	}
	cid := token.CharacterID
	result, err := a.ESI.Get(ctx, fmt.Sprintf("/characters/%d/mail/%d", cid, in.MailID), &cid, nil, nil)
	if err != nil {
		return nil, wrap("eveMailRead", err)
	}
	mail := j.Map(result.Data)
	idSet := map[int]struct{}{}
	if j.Int(mail[fFrom]) != 0 {
		idSet[j.Int(mail[fFrom])] = struct{}{}
	}
	for _, r := range j.Maps(mail["recipients"]) {
		if j.Int(r["recipient_id"]) != 0 {
			idSet[j.Int(r["recipient_id"])] = struct{}{}
		}
	}
	names, err := a.Resolver.Names(ctx, setToList(idSet), nil)
	if err != nil {
		return nil, wrap("eveMailRead", err)
	}
	var to []string
	for _, r := range j.Maps(mail["recipients"]) {
		to = append(to, names[j.Int(r["recipient_id"])])
	}

	return map[string]any{
		fMailID: in.MailID, fFrom: names[j.Int(mail[fFrom])], "to": to,
		fSubject: mail[fSubject], fTimestamp: mail[fTimestamp],
		fBody: stripMarkup(j.Str(mail[fBody])),
	}, nil
}

func eveSocialNotifications(ctx context.Context, a *session.Session, in notesIn) (any, error) {
	token, err := a.ResolveCharacter(ctx, in.Character)
	if err != nil {
		return nil, wrap("eveSocialNotifications", err)
	}
	if err := a.RequireScope(token, "esi-characters.read_notifications.v1", "notifications"); err != nil {
		return nil, wrap("eveSocialNotifications", err)
	}
	cid := token.CharacterID
	result, err := a.ESI.Get(ctx, fmt.Sprintf("/characters/%d/notifications", cid), &cid, nil, nil)
	if err != nil {
		return nil, wrap("eveSocialNotifications", err)
	}
	notes := j.Maps(result.Data)
	if len(notes) == 0 {
		return map[string]any{fCharacter: token.CharacterName, "notifications": []any{}}, nil
	}
	senders := map[int]struct{}{}
	for _, n := range notes {
		if j.Int(n["sender_id"]) != 0 {
			senders[j.Int(n["sender_id"])] = struct{}{}
		}
	}
	names, err := a.Resolver.Names(ctx, setToList(senders), nil)
	if err != nil {
		return nil, wrap("eveSocialNotifications", err)
	}
	sort.Slice(notes, func(i, k int) bool { return j.Str(notes[i][fTimestamp]) > j.Str(notes[k][fTimestamp]) })
	var rows []map[string]any
	unread := 0
	for _, n := range notes {
		from := names[j.Int(n["sender_id"])]
		if from == "" {
			from = j.Str(n["sender_type"])
		}
		detail := strings.ReplaceAll(j.Str(n["text"]), "\n", " ")
		if len(detail) > textPreview {
			detail = detail[:textPreview]
		}
		var det any
		if detail != "" {
			det = detail
		}
		read := j.Bool(n["is_read"])
		if !read {
			unread++
		}
		rows = append(rows, map[string]any{
			fType: n[fType], fFrom: from, fTimestamp: n[fTimestamp],
			fRead: read, "detail": det,
		})
	}
	visible, meta := page(rows, limitOr(in.Limit, limitDefault), "")

	return merge(map[string]any{
		fCharacter: token.CharacterName, fUnread: unread, fDataAge: result.StaleNote(),
		"notifications": project(visible, []string{fType, fFrom, fTimestamp, fRead}, concise(in.ResponseFormat)),
	}, meta), nil
}

func eveSocialKillmails(ctx context.Context, a *session.Session, in kmIn) (any, error) {
	token, err := a.ResolveCharacter(ctx, in.Character)
	if err != nil {
		return nil, wrap("eveSocialKillmails", err)
	}
	if err := a.RequireScope(token, "esi-killmails.read_killmails.v1", fKillmails); err != nil {
		return nil, wrap("eveSocialKillmails", err)
	}

	return formatKillmails(ctx, a, token.CharacterName, token.CharacterID, 0, fmt.Sprintf("/characters/%d/killmails/recent", token.CharacterID), limitOr(in.Limit, limitKillmails), concise(in.ResponseFormat))
}

func eveFittingList(ctx context.Context, a *session.Session, in fitIn) (any, error) {
	token, err := a.ResolveCharacter(ctx, in.Character)
	if err != nil {
		return nil, wrap("eveFittingList", err)
	}
	if err := a.RequireScope(token, "esi-fittings.read_fittings.v1", "fittings"); err != nil {
		return nil, wrap("eveFittingList", err)
	}
	cid := token.CharacterID
	result, err := a.ESI.Get(ctx, fmt.Sprintf("/characters/%d/fittings", cid), &cid, nil, nil)
	if err != nil {
		return nil, wrap("eveFittingList", err)
	}
	fittings := j.Maps(result.Data)
	if len(fittings) == 0 {
		return map[string]any{fCharacter: token.CharacterName, "fittings": []any{}, fNote: "None saved."}, nil
	}
	idSet := map[int]struct{}{}
	for _, f := range fittings {
		idSet[j.Int(f[fShipTypeID])] = struct{}{}
		for _, i := range j.Maps(f[fItems]) {
			idSet[j.Int(i[fTypeID])] = struct{}{}
		}
	}
	names, err := a.Resolver.Names(ctx, setToList(idSet), nil)
	if err != nil {
		return nil, wrap("eveFittingList", err)
	}
	var rows []map[string]any
	for _, f := range fittings {
		var mods []string
		for _, i := range j.Maps(f[fItems]) {
			mods = append(mods, fmt.Sprintf("%v x%v [%v]", nameOr(names, j.Int(i[fTypeID])), i[fQuantity], i["flag"]))
		}
		desc := j.Str(f[fDescription])
		if len(desc) > fittingDescPreview {
			desc = desc[:fittingDescPreview]
		}
		var d any
		if desc != "" {
			d = desc
		}
		rows = append(rows, map[string]any{
			fFittingID: f[fFittingID], fName: f[fName], "ship": names[j.Int(f[fShipTypeID])],
			"module_count": len(j.Slice(f[fItems])), fDescription: d, fModules: mods,
		})
	}
	visible, meta := page(rows, limitOr(in.Limit, limitShort), "")

	return merge(map[string]any{
		fCharacter: token.CharacterName, fDataAge: result.StaleNote(),
		"fittings": project(visible, []string{fFittingID, fName, "ship", "module_count"}, concise(in.ResponseFormat)),
	}, meta), nil
}

type killmailFetch struct {
	kills  []map[string]any
	failed []any
}

type killmailSummary struct {
	rows   []map[string]any
	kills  int
	losses int
}

func formatKillmails(ctx context.Context, a *session.Session, character string, characterID, corpID int, path string, limit int, conciseMode bool) (any, error) {
	var cid *int
	if characterID != 0 {
		cid = &characterID
	}
	result, err := a.ESI.Get(ctx, path, cid, nil, nil)
	if err != nil {
		return nil, wrap("formatKillmails", err)
	}
	available := j.Maps(result.Data)
	refs := available
	if len(refs) > limit {
		refs = refs[:limit]
	}
	if len(refs) == 0 {
		return map[string]any{fCharacter: character, fKillmails: []any{}, fNote: "Nothing recent."}, nil
	}
	fetched := fetchKillmailBodies(ctx, a, refs)
	built, err := buildKillmailRows(ctx, a, fetched.kills, characterID, corpID)
	if err != nil {
		return nil, err
	}
	sort.Slice(built.rows, func(i, k int) bool { return j.Str(built.rows[i]["time"]) > j.Str(built.rows[k]["time"]) })
	visible, meta := page(built.rows, limit, "")
	if len(available) > limit {
		meta = map[string]any{
			fReturned: len(visible), "total_available": len(available), fTruncated: true,
			"how_to_see_more": fmt.Sprintf("Raise `limit` (currently %d).", limit),
		}
	}
	out := merge(map[string]any{
		fCharacter: character, "kills": built.kills, "losses": built.losses,
		"hull_value_caveat": "Hull only — fitted modules and cargo are not included.",
		fKillmails:          project(visible, []string{"role", "time", fSystem, "victim", "ship_lost"}, conciseMode),
	}, meta)
	if len(fetched.failed) > 0 {
		out["unavailable"] = len(fetched.failed)
		out["unavailable_note"] = fmt.Sprintf("%d of %d killmails could not be fetched from ESI, so kills/losses below undercount by that many. Try again shortly.", len(fetched.failed), len(refs))
	}

	return out, nil
}

func fetchKillmailBodies(ctx context.Context, a *session.Session, refs []map[string]any) killmailFetch {
	type box struct {
		id   any
		data map[string]any
		err  error
	}
	ch := make(chan box, len(refs))
	for _, ref := range refs {
		go func(ref map[string]any) {
			r, err := a.ESI.Get(ctx, fmt.Sprintf("/killmails/%d/%s", j.Int(ref["killmail_id"]), j.Str(ref["killmail_hash"])), nil, nil, nil)
			if err != nil {
				ch <- box{ref["killmail_id"], nil, err}

				return
			}
			ch <- box{ref["killmail_id"], j.Map(r.Data), nil}
		}(ref)
	}
	var kills []map[string]any
	var failed []any
	for range refs {
		b := <-ch
		if b.err != nil {
			failed = append(failed, b.id)
			a.Logger.Error("eve: killmail fetch", "id", b.id, "err", b.err)
		} else {
			kills = append(kills, b.data)
		}
	}

	return killmailFetch{kills: kills, failed: failed}
}

func buildKillmailRows(ctx context.Context, a *session.Session, kills []map[string]any, characterID, corpID int) (killmailSummary, error) {
	idSet := map[int]struct{}{}
	for _, kill := range kills {
		victim := j.Map(kill["victim"])
		for _, k := range []string{fCharacterID, "corporation_id", fShipTypeID} {
			if j.Int(victim[k]) != 0 {
				idSet[j.Int(victim[k])] = struct{}{}
			}
		}
		if j.Int(kill["solar_system_id"]) != 0 {
			idSet[j.Int(kill["solar_system_id"])] = struct{}{}
		}
	}
	names, err := a.Resolver.Names(ctx, setToList(idSet), nil)
	if err != nil {
		return killmailSummary{}, wrap("buildKillmailRows", err)
	}
	prices, err := a.Resolver.ReferencePrices(ctx)
	if err != nil {
		return killmailSummary{}, wrap("buildKillmailRows", err)
	}
	rows := make([]map[string]any, 0, len(kills))
	killsN, losses := 0, 0
	for _, kill := range kills {
		victim := j.Map(kill["victim"])
		var wasVictim bool
		if corpID != 0 {
			wasVictim = j.Int(victim["corporation_id"]) == corpID
		} else {
			wasVictim = j.Int(victim[fCharacterID]) == characterID
		}
		role := "kill"
		if wasVictim {
			role = "loss"
			losses++
		} else {
			killsN++
		}
		who := names[j.Int(victim[fCharacterID])]
		if who == "" {
			who = names[j.Int(victim["corporation_id"])]
		}
		rows = append(rows, map[string]any{
			"role": role, "time": kill["killmail_time"], fSystem: names[j.Int(kill["solar_system_id"])],
			"victim": who, "ship_lost": names[j.Int(victim[fShipTypeID])],
			"hull_value": isk(unitPrice(prices, j.Int(victim[fShipTypeID]))),
			"attackers":  len(j.Slice(kill["attackers"])),
			"zkill":      fmt.Sprintf("https://zkillboard.com/kill/%v/", kill["killmail_id"]),
		})
	}

	return killmailSummary{rows: rows, kills: killsN, losses: losses}, nil
}

func stripMarkup(body string) string {
	return strings.TrimSpace(tagRe.ReplaceAllString(brRe.ReplaceAllString(body, "\n"), ""))
}
