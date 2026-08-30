package eve

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/truewebber/eve-online-mcp/internal/domain/j"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerWallet(s *mcp.Server) {
	type histIn struct {
		Character      string `json:"character,omitempty"       jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
		Kind           string `json:"kind,omitempty"            jsonschema:"'journal' is every ISK movement. 'transactions' is market trades. 'both' returns each in its own section. Default journal."`
		RefType        string `json:"ref_type,omitempty"        jsonschema:"Journal only: keep just one reason code, e.g. 'bounty_prizes'."`
		Limit          int    `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
		ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
	}
	addTool(s, &mcp.Tool{
		Name:        "eve_wallet_history",
		Description: "Where the ISK went: journal entries and market trades, with totals by category.\n\nThe current balance is not here — eve_character_overview already carries it. ESI keeps roughly the last 30 days. The by_category summary is computed over the whole window, not just the returned rows.\n\nReturns: period, totals, by_category[], and journal[] / transactions[] depending on kind.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in histIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *session.Session) (any, error) {
			token, err := a.ResolveCharacter(in.Character)
			if err != nil {
				return nil, err
			}
			if err := a.RequireScope(token, "esi-wallet.read_character_wallet.v1", "wallet history"); err != nil {
				return nil, err
			}
			kind := in.Kind
			if kind == "" {
				kind = "journal"
			}
			cid := token.CharacterID
			out := map[string]any{"character": token.CharacterName, "period": "last ~30 days (ESI retention limit)"}
			if kind == "journal" || kind == "both" {
				sec, err := journalSection(a, cid, in.RefType, limitOr(in.Limit, 15), concise(in.ResponseFormat))
				if err != nil {
					return nil, err
				}
				out["journal_section"] = sec
			}
			if kind == "transactions" || kind == "both" {
				sec, err := transactionSection(a, fmt.Sprintf("/characters/%d/wallet/transactions", cid), cid, limitOr(in.Limit, 15), concise(in.ResponseFormat))
				if err != nil {
					return nil, err
				}
				out["transactions_section"] = sec
			}
			if kind == "journal" {
				sec := j.Map(out["journal_section"])
				delete(out, "journal_section")

				return merge(out, sec), nil
			}
			if kind == "transactions" {
				sec := j.Map(out["transactions_section"])
				delete(out, "transactions_section")

				return merge(out, sec), nil
			}

			return out, nil
		})
	})
}

func journalSection(a *session.Session, cid int, refType string, limit int, conciseMode bool) (map[string]any, error) {
	result, err := a.ESI.GetAllPages(fmt.Sprintf("/characters/%d/wallet/journal", cid), &cid, nil, 10)
	if err != nil {
		return nil, err
	}

	return summarizeJournal(result.Data, result.StaleNote(), result.Truncated, 10, refType, limit, conciseMode, "")
}

func summarizeJournal(data any, stale string, truncated bool, pageCap int, refType string, limit int, conciseMode bool, divisionNote string) (map[string]any, error) {
	entries := j.Maps(data)
	codes := map[string]struct{}{}
	for _, e := range entries {
		codes[j.Str(e["ref_type"])] = struct{}{}
	}
	var available []string
	for c := range codes {
		if c == "" {
			c = "unknown"
		}
		available = append(available, c)
	}
	sort.Strings(available)
	if refType != "" {
		var filtered []map[string]any
		for _, e := range entries {
			if j.Str(e["ref_type"]) == refType {
				filtered = append(filtered, e)
			}
		}
		if len(filtered) == 0 {
			msg := fmt.Sprintf("No journal entries with ref_type %q in the window. Codes actually present: %v", refType, available)
			if divisionNote != "" {
				msg = fmt.Sprintf("No journal entries with ref_type %q in %s. Codes actually present: %v", refType, divisionNote, available)
			}

			return map[string]any{"journal": []any{}, "error": msg}, nil
		}
		entries = filtered
	}
	type tot struct{ in, out, n float64 }
	totals := map[string]*tot{}
	for _, e := range entries {
		amount := j.Float(e["amount"])
		name := j.Str(e["ref_type"])
		if name == "" {
			name = "unknown"
		}
		b := totals[name]
		if b == nil {
			b = &tot{}
			totals[name] = b
		}
		if amount >= 0 {
			b.in += amount
		} else {
			b.out += amount
		}
		b.n++
	}
	var byCat []map[string]any
	for name, b := range totals {
		byCat = append(byCat, map[string]any{
			"ref_type": name, "entries": int(b.n), "income": isk(b.in), "spending": isk(b.out),
			"net_isk": mathRound(b.in+b.out, 2),
		})
	}
	sort.Slice(byCat, func(i, k int) bool {
		ai, ak := j.Float(byCat[i]["net_isk"]), j.Float(byCat[k]["net_isk"])
		if ai < 0 {
			ai = -ai
		}
		if ak < 0 {
			ak = -ak
		}

		return ai > ak
	})
	if len(byCat) > 15 {
		byCat = byCat[:15]
	}
	sort.Slice(entries, func(i, k int) bool { return j.Str(entries[i]["date"]) > j.Str(entries[k]["date"]) })
	var rows []map[string]any
	for _, e := range entries {
		rows = append(rows, map[string]any{
			"date": e["date"], "ref_type": e["ref_type"], "amount": isk(e["amount"]),
			"description": e["description"], "amount_isk": e["amount"],
			"balance_after": isk(e["balance"]), "reason": e["reason"],
		})
	}
	visible, meta := page(rows, limit, "Raise `limit`, or narrow with `ref_type`.")
	var gin, gout float64
	for _, b := range totals {
		gin += b.in
		gout += b.out
	}
	out := merge(map[string]any{
		"total_income": isk(gin), "total_spending": isk(gout), "net": isk(gin + gout),
		"by_category": byCat, "data_age": stale,
		"journal": project(visible, []string{"date", "ref_type", "amount", "description"}, conciseMode),
	}, meta)
	if truncated {
		out["totals_caveat"] = fmt.Sprintf("Hit the %d-page read cap: the totals and by_category above cover the newest %s entries, not the full window.", pageCap, formatInt(len(entries)))
	}

	return out, nil
}

func transactionSection(a *session.Session, path string, cid int, limit int, conciseMode bool) (map[string]any, error) {
	result, err := a.ESI.GetCursorPages(path, &cid, nil, "from_id", "transaction_id", 2500, 4)
	if err != nil {
		return nil, err
	}

	return summarizeTransactions(a, cid, result.Data, result.StaleNote(), result.Truncated, limit, conciseMode)
}

func summarizeTransactions(a *session.Session, cid int, data any, stale string, truncated bool, limit int, conciseMode bool) (map[string]any, error) {
	entries := j.Maps(data)
	if len(entries) == 0 {
		return map[string]any{"transactions": []any{}, "note": "No market trades in the retained window."}, nil
	}
	typeSet, placeSet := map[int]struct{}{}, map[int]struct{}{}
	for _, t := range entries {
		typeSet[j.Int(t["type_id"])] = struct{}{}
		placeSet[j.Int(t["location_id"])] = struct{}{}
	}
	typeNames, _ := a.Resolver.Names(setToList(typeSet), nil)
	placeNames, _ := a.Resolver.Names(setToList(placeSet), &cid)
	var bought, sold float64
	for _, t := range entries {
		total := j.Float(t["unit_price"]) * j.Float(t["quantity"])
		if j.Bool(t["is_buy"]) {
			bought += total
		} else {
			sold += total
		}
	}
	sort.Slice(entries, func(i, k int) bool { return j.Str(entries[i]["date"]) > j.Str(entries[k]["date"]) })
	var rows []map[string]any
	for _, t := range entries {
		side := "sell"
		if j.Bool(t["is_buy"]) {
			side = "buy"
		}
		rows = append(rows, map[string]any{
			"date": t["date"], "side": side, "item": typeNames[j.Int(t["type_id"])],
			"quantity": t["quantity"], "total": isk(j.Float(t["unit_price"]) * j.Float(t["quantity"])),
			"unit_price": isk(t["unit_price"]), "location": nameOr(placeNames, j.Int(t["location_id"])),
		})
	}
	visible, meta := page(rows, limit, "")
	out := merge(map[string]any{
		"total_bought": isk(bought), "total_sold": isk(sold), "gross_margin": isk(sold - bought),
		"covers":        fmt.Sprintf("%v to %v (%s trades)", rows[len(rows)-1]["date"], rows[0]["date"], formatInt(len(entries))),
		"margin_caveat": "Sold minus bought over the trades in `covers`, not per-item profit.",
		"data_age":      stale,
		"transactions":  project(visible, []string{"date", "side", "item", "quantity", "total"}, conciseMode),
	}, meta)
	if truncated {
		out["totals_caveat"] = fmt.Sprintf("Only the newest %s trades were read, so the totals cover `covers` and not the full retention window.", formatInt(len(entries)))
	}

	return out, nil
}

func formatInt(n int) string {
	return strconv.Itoa(n)
}
