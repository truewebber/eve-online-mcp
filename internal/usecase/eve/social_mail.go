package eve

import (
	"context"
	"fmt"
	"sort"

	"github.com/truewebber/eve-online-mcp/internal/j"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"
)

type mailListIn struct {
	UnreadOnly     *bool  `json:"unread_only,omitempty"     jsonschema:"Only list mail that has not been read yet."`
	LastMailID     int    `json:"last_mail_id,omitempty"    jsonschema:"Return mail older than this id — pass back the next_cursor from a previous call to reach further into the past. Empty starts at the newest."`
	Limit          int    `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

type mailReadIn struct {
	MailID int `json:"mail_id" jsonschema:"Mail id from eve_mail_list."`
}

func eveMailList(ctx context.Context, a *session.Session, in mailListIn) (any, error) {
	if err := rejectUnknownFormat(in.ResponseFormat); err != nil {
		return nil, err
	}
	token, err := a.Character(ctx)
	if err != nil {
		return nil, wrap("eveMailList", err)
	}
	if err := a.RequireScope(token, "esi-mail.read_mail.v1", "mail"); err != nil {
		return nil, wrap("eveMailList", err)
	}
	cid := token.CharacterID
	result, err := a.ESI.Get(ctx, esiPath("characters", esiID(cid), "mail"), &cid, esiCursorQuery(fLastMailID, in.LastMailID), nil)
	if err != nil {
		return nil, wrap("eveMailList", err)
	}
	esiMails := j.Maps(result.Data)
	mails := filterMail(esiMails, boolDef(in.UnreadOnly, false))
	if len(mails) == 0 {
		return map[string]any{fCharacter: token.CharacterName, "mails": []any{}, fNote: "Nothing to show.", fDataAge: result.StaleNote()}, nil
	}
	names, err := a.Resolver.Names(ctx, mailSenderIDs(mails), nil)
	if err != nil {
		return nil, wrap("eveMailList", err)
	}
	sort.Slice(mails, func(i, k int) bool { return j.Str(mails[i][fTimestamp]) > j.Str(mails[k][fTimestamp]) })
	paged := pageByCursor(cursorPageIn{
		Shown: mailRows(mails, names), Limit: limitOr(in.Limit, limitDefault),
		Key: fMailID, Hint: "Pass next_cursor as last_mail_id.", ESI: esiMails,
	})

	return merge(map[string]any{
		fCharacter: token.CharacterName, fUnread: countUnreadMail(esiMails), fDataAge: result.StaleNote(),
		"mails": project(paged.Rows, []string{fMailID, fFrom, fSubject, fTimestamp, fRead}, concise(in.ResponseFormat)),
	}, paged.fields), nil
}

func countUnreadMail(mails []map[string]any) int {
	unread := 0
	for _, m := range mails {
		if !j.Bool(m["is_read"]) {
			unread++
		}
	}

	return unread
}

func filterMail(mails []map[string]any, unreadOnly bool) []map[string]any {
	if !unreadOnly {
		return mails
	}
	var filtered []map[string]any
	for _, m := range mails {
		if !j.Bool(m["is_read"]) {
			filtered = append(filtered, m)
		}
	}

	return filtered
}

func mailSenderIDs(mails []map[string]any) []int {
	senders := map[int]struct{}{}
	for _, m := range mails {
		if j.Int(m[fFrom]) != 0 {
			senders[j.Int(m[fFrom])] = struct{}{}
		}
	}

	return setToList(senders)
}

func mailRows(mails []map[string]any, names map[int]string) []map[string]any {
	rows := make([]map[string]any, 0, len(mails))
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

	return rows
}

func eveMailRead(ctx context.Context, a *session.Session, in mailReadIn) (any, error) {
	token, err := a.Character(ctx)
	if err != nil {
		return nil, wrap("eveMailRead", err)
	}
	if err := a.RequireScope(token, "esi-mail.read_mail.v1", "mail"); err != nil {
		return nil, wrap("eveMailRead", err)
	}
	cid := token.CharacterID
	result, err := a.ESI.Get(ctx, esiPath("characters", esiID(cid), "mail", esiID(in.MailID)), &cid, nil, nil)
	if err != nil {
		return nil, wrap("eveMailRead", err)
	}
	mail := j.Map(result.Data)
	idSet := map[int]struct{}{}
	if j.Int(mail[fFrom]) != 0 {
		idSet[j.Int(mail[fFrom])] = struct{}{}
	}
	for _, r := range j.Maps(mail[fRecipients]) {
		if j.Int(r["recipient_id"]) != 0 {
			idSet[j.Int(r["recipient_id"])] = struct{}{}
		}
	}
	names, err := a.Resolver.Names(ctx, setToList(idSet), nil)
	if err != nil {
		return nil, wrap("eveMailRead", err)
	}
	var to []string
	for _, r := range j.Maps(mail[fRecipients]) {
		to = append(to, names[j.Int(r["recipient_id"])])
	}

	return map[string]any{
		fMailID: in.MailID, fFrom: names[j.Int(mail[fFrom])], "to": to,
		fSubject: mail[fSubject], fTimestamp: mail[fTimestamp],
		fBody: stripMarkup(j.Str(mail[fBody])), fDataAge: result.StaleNote(),
	}, nil
}
