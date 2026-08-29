package tools

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"

	"eve-mcp/internal/app"
	"eve-mcp/internal/j"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	tagRe = regexp.MustCompile(`<[^>]+>`)
	brRe  = regexp.MustCompile(`(?i)<br\s*/?>`)
)

func registerSocial(s *mcp.Server, a *app.App) {
	type mailListIn struct {
		Character      string `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
		UnreadOnly     *bool  `json:"unread_only,omitempty" jsonschema:"Only list mail that has not been read yet."`
		Limit          int    `json:"limit,omitempty" jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
		ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
	}
	addTool(s, &mcp.Tool{
		Name: "eve_mail_list",
		Description: "Mail headers only — sender, subject, date, read state. Bodies are not included.\n\nTo read the actual text of one mail, follow up with eve_mail_read using the mail_id from here.\n\nReturns: unread count, mails[] with mail_id for follow-up.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in mailListIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *app.App) (any, error) {
			token, err := a.ResolveCharacter(in.Character)
			if err != nil {
				return nil, err
			}
			if err := a.RequireScope(token, "esi-mail.read_mail.v1", "mail"); err != nil {
				return nil, err
			}
			cid := token.CharacterID
			result, err := a.ESI.Get(fmt.Sprintf("/characters/%d/mail", cid), &cid, nil, nil)
			if err != nil {
				return nil, err
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
				return map[string]any{"character": token.CharacterName, "mails": []any{}, "note": "Nothing to show."}, nil
			}
			senders := map[int]struct{}{}
			for _, m := range mails {
				if j.Int(m["from"]) != 0 {
					senders[j.Int(m["from"])] = struct{}{}
				}
			}
			names, _ := a.Resolver.Names(setToList(senders), nil)
			sort.Slice(mails, func(i, k int) bool { return j.Str(mails[i]["timestamp"]) > j.Str(mails[k]["timestamp"]) })
			var rows []map[string]any
			for _, m := range mails {
				from := names[j.Int(m["from"])]
				if from == "" {
					from = fmt.Sprintf("#%v", m["from"])
				}
				rows = append(rows, map[string]any{
					"mail_id": m["mail_id"], "from": from, "subject": m["subject"],
					"timestamp": m["timestamp"], "read": j.Bool(m["is_read"]), "labels": m["labels"],
				})
			}
			visible, meta := page(rows, limitOr(in.Limit, 15), "")
			return merge(map[string]any{
				"character": token.CharacterName, "unread": unread, "data_age": result.StaleNote(),
				"mails": project(visible, []string{"mail_id", "from", "subject", "timestamp", "read"}, concise(in.ResponseFormat)),
			}, meta), nil
		})
	})

	type mailReadIn struct {
		MailID    int    `json:"mail_id" jsonschema:"Mail id from eve_mail_list.,minimum=1"`
		Character string `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
	}
	addTool(s, &mcp.Tool{
		Name: "eve_mail_read",
		Description: "Fetch the full body text of one mail.\n\nRead-only: this does not mark the mail as read in game. Use eve_mail_mark for that.\n\nReturns: from, to[], subject, timestamp, body (markup stripped).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in mailReadIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *app.App) (any, error) {
			token, err := a.ResolveCharacter(in.Character)
			if err != nil {
				return nil, err
			}
			if err := a.RequireScope(token, "esi-mail.read_mail.v1", "mail"); err != nil {
				return nil, err
			}
			cid := token.CharacterID
			result, err := a.ESI.Get(fmt.Sprintf("/characters/%d/mail/%d", cid, in.MailID), &cid, nil, nil)
			if err != nil {
				return nil, err
			}
			mail := j.Map(result.Data)
			idSet := map[int]struct{}{}
			if j.Int(mail["from"]) != 0 {
				idSet[j.Int(mail["from"])] = struct{}{}
			}
			for _, r := range j.Maps(mail["recipients"]) {
				if j.Int(r["recipient_id"]) != 0 {
					idSet[j.Int(r["recipient_id"])] = struct{}{}
				}
			}
			names, _ := a.Resolver.Names(setToList(idSet), nil)
			var to []string
			for _, r := range j.Maps(mail["recipients"]) {
				to = append(to, names[j.Int(r["recipient_id"])])
			}
			return map[string]any{
				"mail_id": in.MailID, "from": names[j.Int(mail["from"])], "to": to,
				"subject": mail["subject"], "timestamp": mail["timestamp"],
				"body": stripMarkup(j.Str(mail["body"])),
			}, nil
		})
	})

	type notesIn struct {
		Character      string `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
		Limit          int    `json:"limit,omitempty" jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
		ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
	}
	addTool(s, &mcp.Tool{
		Name: "eve_social_notifications",
		Description: "In-game notifications: structure attacks, war decs, corp and contract events.\n\nThis is where genuinely time-critical things surface. The detail field is raw YAML with unresolved numeric ids.\n\nReturns: unread count, notifications[] newest first.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in notesIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *app.App) (any, error) {
			token, err := a.ResolveCharacter(in.Character)
			if err != nil {
				return nil, err
			}
			if err := a.RequireScope(token, "esi-characters.read_notifications.v1", "notifications"); err != nil {
				return nil, err
			}
			cid := token.CharacterID
			result, err := a.ESI.Get(fmt.Sprintf("/characters/%d/notifications", cid), &cid, nil, nil)
			if err != nil {
				return nil, err
			}
			notes := j.Maps(result.Data)
			if len(notes) == 0 {
				return map[string]any{"character": token.CharacterName, "notifications": []any{}}, nil
			}
			senders := map[int]struct{}{}
			for _, n := range notes {
				if j.Int(n["sender_id"]) != 0 {
					senders[j.Int(n["sender_id"])] = struct{}{}
				}
			}
			names, _ := a.Resolver.Names(setToList(senders), nil)
			sort.Slice(notes, func(i, k int) bool { return j.Str(notes[i]["timestamp"]) > j.Str(notes[k]["timestamp"]) })
			var rows []map[string]any
			unread := 0
			for _, n := range notes {
				from := names[j.Int(n["sender_id"])]
				if from == "" {
					from = j.Str(n["sender_type"])
				}
				detail := strings.ReplaceAll(j.Str(n["text"]), "\n", " ")
				if len(detail) > 300 {
					detail = detail[:300]
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
					"type": n["type"], "from": from, "timestamp": n["timestamp"],
					"read": read, "detail": det,
				})
			}
			visible, meta := page(rows, limitOr(in.Limit, 15), "")
			return merge(map[string]any{
				"character": token.CharacterName, "unread": unread, "data_age": result.StaleNote(),
				"notifications": project(visible, []string{"type", "from", "timestamp", "read"}, concise(in.ResponseFormat)),
			}, meta), nil
		})
	})

	type kmIn struct {
		Character      string `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
		Limit          int    `json:"limit,omitempty" jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
		ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
	}
	addTool(s, &mcp.Tool{
		Name: "eve_social_killmails",
		Description: "Recent kills and losses involving this character.\n\nhull_value covers the ship hull only. Each row carries a zkillboard link.\n\nReturns: kills, losses, killmails[] newest first.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in kmIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *app.App) (any, error) {
			token, err := a.ResolveCharacter(in.Character)
			if err != nil {
				return nil, err
			}
			if err := a.RequireScope(token, "esi-killmails.read_killmails.v1", "killmails"); err != nil {
				return nil, err
			}
			return formatKillmails(a, token.CharacterName, token.CharacterID, 0, fmt.Sprintf("/characters/%d/killmails/recent", token.CharacterID), limitOr(in.Limit, 8), concise(in.ResponseFormat))
		})
	})

	type fitIn struct {
		Character      string `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
		Limit          int    `json:"limit,omitempty" jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
		ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
	}
	addTool(s, &mcp.Tool{
		Name: "eve_fitting_list",
		Description: "Saved ship fittings with their module lists.\n\nIn concise mode module lists are omitted. Returns: fittings[] with fitting_id (needed by eve_fitting_delete).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in fitIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *app.App) (any, error) {
			token, err := a.ResolveCharacter(in.Character)
			if err != nil {
				return nil, err
			}
			if err := a.RequireScope(token, "esi-fittings.read_fittings.v1", "fittings"); err != nil {
				return nil, err
			}
			cid := token.CharacterID
			result, err := a.ESI.Get(fmt.Sprintf("/characters/%d/fittings", cid), &cid, nil, nil)
			if err != nil {
				return nil, err
			}
			fittings := j.Maps(result.Data)
			if len(fittings) == 0 {
				return map[string]any{"character": token.CharacterName, "fittings": []any{}, "note": "None saved."}, nil
			}
			idSet := map[int]struct{}{}
			for _, f := range fittings {
				idSet[j.Int(f["ship_type_id"])] = struct{}{}
				for _, i := range j.Maps(f["items"]) {
					idSet[j.Int(i["type_id"])] = struct{}{}
				}
			}
			names, _ := a.Resolver.Names(setToList(idSet), nil)
			var rows []map[string]any
			for _, f := range fittings {
				var mods []string
				for _, i := range j.Maps(f["items"]) {
					mods = append(mods, fmt.Sprintf("%v x%v [%v]", nameOr(names, j.Int(i["type_id"])), i["quantity"], i["flag"]))
				}
				desc := j.Str(f["description"])
				if len(desc) > 200 {
					desc = desc[:200]
				}
				var d any
				if desc != "" {
					d = desc
				}
				rows = append(rows, map[string]any{
					"fitting_id": f["fitting_id"], "name": f["name"], "ship": names[j.Int(f["ship_type_id"])],
					"module_count": len(j.Slice(f["items"])), "description": d, "modules": mods,
				})
			}
			visible, meta := page(rows, limitOr(in.Limit, 10), "")
			return merge(map[string]any{
				"character": token.CharacterName, "data_age": result.StaleNote(),
				"fittings": project(visible, []string{"fitting_id", "name", "ship", "module_count"}, concise(in.ResponseFormat)),
			}, meta), nil
		})
	})
}

func formatKillmails(a *app.App, character string, characterID, corpID int, path string, limit int, conciseMode bool) (any, error) {
	var cid *int
	if characterID != 0 {
		cid = &characterID
	}
	result, err := a.ESI.Get(path, cid, nil, nil)
	if err != nil {
		return nil, err
	}
	available := j.Maps(result.Data)
	refs := available
	if len(refs) > limit {
		refs = refs[:limit]
	}
	if len(refs) == 0 {
		return map[string]any{"character": character, "killmails": []any{}, "note": "Nothing recent."}, nil
	}
	type box struct {
		id   any
		data map[string]any
		err  error
	}
	ch := make(chan box, len(refs))
	for _, ref := range refs {
		go func(ref map[string]any) {
			r, err := a.ESI.Get(fmt.Sprintf("/killmails/%d/%s", j.Int(ref["killmail_id"]), j.Str(ref["killmail_hash"])), nil, nil, nil)
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
			log.Printf("killmail %v could not be fetched: %v", b.id, b.err)
		} else {
			kills = append(kills, b.data)
		}
	}
	idSet := map[int]struct{}{}
	for _, kill := range kills {
		victim := j.Map(kill["victim"])
		for _, k := range []string{"character_id", "corporation_id", "ship_type_id"} {
			if j.Int(victim[k]) != 0 {
				idSet[j.Int(victim[k])] = struct{}{}
			}
		}
		if j.Int(kill["solar_system_id"]) != 0 {
			idSet[j.Int(kill["solar_system_id"])] = struct{}{}
		}
	}
	names, _ := a.Resolver.Names(setToList(idSet), nil)
	prices, _ := a.Resolver.ReferencePrices()
	var rows []map[string]any
	killsN, losses := 0, 0
	for _, kill := range kills {
		victim := j.Map(kill["victim"])
		wasVictim := false
		if corpID != 0 {
			wasVictim = j.Int(victim["corporation_id"]) == corpID
		} else {
			wasVictim = j.Int(victim["character_id"]) == characterID
		}
		role := "kill"
		if wasVictim {
			role = "loss"
			losses++
		} else {
			killsN++
		}
		who := names[j.Int(victim["character_id"])]
		if who == "" {
			who = names[j.Int(victim["corporation_id"])]
		}
		rows = append(rows, map[string]any{
			"role": role, "time": kill["killmail_time"], "system": names[j.Int(kill["solar_system_id"])],
			"victim": who, "ship_lost": names[j.Int(victim["ship_type_id"])],
			"hull_value": isk(unitPrice(prices, j.Int(victim["ship_type_id"]))),
			"attackers":  len(j.Slice(kill["attackers"])),
			"zkill":      fmt.Sprintf("https://zkillboard.com/kill/%v/", kill["killmail_id"]),
		})
	}
	sort.Slice(rows, func(i, k int) bool { return j.Str(rows[i]["time"]) > j.Str(rows[k]["time"]) })
	visible, meta := page(rows, limit, "")
	if len(available) > limit {
		meta = map[string]any{
			"returned": len(visible), "total_available": len(available), "truncated": true,
			"how_to_see_more": fmt.Sprintf("Raise `limit` (currently %d).", limit),
		}
	}
	out := merge(map[string]any{
		"character": character, "kills": killsN, "losses": losses,
		"hull_value_caveat": "Hull only — fitted modules and cargo are not included.",
		"killmails":         project(visible, []string{"role", "time", "system", "victim", "ship_lost"}, conciseMode),
	}, meta)
	if len(failed) > 0 {
		out["unavailable"] = len(failed)
		out["unavailable_note"] = fmt.Sprintf("%d of %d killmails could not be fetched from ESI, so kills/losses below undercount by that many. Try again shortly.", len(failed), len(refs))
	}
	return out, nil
}

func stripMarkup(body string) string {
	return strings.TrimSpace(tagRe.ReplaceAllString(brRe.ReplaceAllString(body, "\n"), ""))
}
