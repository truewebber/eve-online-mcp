package eve

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/truewebber/eve-online-mcp/internal/j"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type walletHistIn struct {
	Kind           string `json:"kind,omitempty"            jsonschema:"'journal' is every ISK movement. 'transactions' is market trades. 'both' returns each in its own section. Default journal."`
	RefType        string `json:"ref_type,omitempty"        jsonschema:"Journal only: keep just one reason code, e.g. 'bounty_prizes'."`
	Limit          int    `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

func registerWallet(s *mcp.Server) {
	addTool(s, &mcp.Tool{
		Name:        "eve_wallet_history",
		Description: "Where the ISK went: journal entries and market trades, with totals by category.\n\nThe current balance is not here — eve_character_overview already carries it. ESI keeps roughly the last 30 days. The by_category summary is computed over the whole window, not just the returned rows.\n\nReturns: period, totals, by_category[], and journal[] / transactions[] depending on kind.",
	}, sessionTool(walletHistory))
}

func walletHistory(ctx context.Context, a *session.Session, in walletHistIn) (any, error) {
	if err := rejectUnknownFormat(in.ResponseFormat); err != nil {
		return nil, err
	}
	kind, err := pickEnum(fKind, in.Kind, fJournal, fJournal, fTransactions, vBoth)
	if err != nil {
		return nil, err
	}
	token, err := a.Character(ctx)
	if err != nil {
		return nil, wrap("walletHistory", err)
	}
	if err := a.RequireScope(token, "esi-wallet.read_character_wallet.v1", "wallet history"); err != nil {
		return nil, wrap("walletHistory", err)
	}
	cid := token.CharacterID
	out := map[string]any{fCharacter: token.CharacterName, fPeriod: "last ~30 days (ESI retention limit)"}
	if kind == fJournal || kind == vBoth {
		sec, err := journalSection(ctx, a, cid, in.RefType, limitOr(in.Limit, limitDefault), concise(in.ResponseFormat))
		if err != nil {
			return nil, err
		}
		out["journal_section"] = sec
	}
	if kind == fTransactions || kind == vBoth {
		sec, err := transactionSection(ctx, a, esiPath("characters", esiID(cid), "wallet", "transactions"), cid, limitOr(in.Limit, limitDefault), concise(in.ResponseFormat))
		if err != nil {
			return nil, err
		}
		out["transactions_section"] = sec
	}
	if kind == fJournal {
		sec := j.Map(out["journal_section"])
		delete(out, "journal_section")

		return merge(out, sec), nil
	}
	if kind == fTransactions {
		sec := j.Map(out["transactions_section"])
		delete(out, "transactions_section")

		return merge(out, sec), nil
	}

	return out, nil
}

func journalSection(ctx context.Context, a *session.Session, cid int, refType string, limit int, conciseMode bool) (map[string]any, error) {
	result, err := a.ESI.GetAllPages(ctx, esiPath("characters", esiID(cid), "wallet", "journal"), &cid, nil, pagesShort)
	if err != nil {
		return nil, wrap("journalSection", err)
	}

	return summarizeJournal(result.Data, result.StaleNote(), result.Truncated, pagesShort, refType, limit, conciseMode, "")
}

type journalTot struct{ in, out, n float64 }

func summarizeJournal(data any, stale string, truncated bool, pageCap int, refType string, limit int, conciseMode bool, divisionNote string) (map[string]any, error) {
	entries := j.Maps(data)
	available := journalRefTypes(entries)
	if refType != "" {
		filtered := filterJournalByRef(entries, refType)
		if len(filtered) == 0 {
			msg := fmt.Sprintf("No journal entries with ref_type %q in the window. Codes actually present: %v", refType, available)
			if divisionNote != "" {
				msg = fmt.Sprintf("No journal entries with ref_type %q in %s. Codes actually present: %v", refType, divisionNote, available)
			}

			return map[string]any{fJournal: []any{}, fError: msg}, nil
		}
		entries = filtered
	}
	totals, byCat := tallyJournal(entries)
	sort.Slice(entries, func(i, k int) bool { return j.Str(entries[i][fDate]) > j.Str(entries[k][fDate]) })
	rows := journalRows(entries)
	visible, meta := page(rows, limit, "Raise `limit`, or narrow with `ref_type`.")
	var gin, gout float64
	for _, b := range totals {
		gin += b.in
		gout += b.out
	}
	out := merge(map[string]any{
		"total_income": isk(gin), "total_spending": isk(gout), "net": isk(gin + gout),
		"by_category": byCat, fDataAge: stale,
		fJournal: project(visible, []string{fDate, fRefType, "amount", fDescription}, conciseMode),
	}, meta)
	if truncated {
		out["totals_caveat"] = fmt.Sprintf("Hit the %d-page read cap: the totals and by_category above cover the newest %s entries, not the full window.", pageCap, formatInt(len(entries)))
	}

	return out, nil
}

func journalRefTypes(entries []map[string]any) []string {
	codes := map[string]struct{}{}
	for _, e := range entries {
		codes[j.Str(e[fRefType])] = struct{}{}
	}
	available := make([]string, 0, len(codes))
	for c := range codes {
		if c == "" {
			c = vUnknown
		}
		available = append(available, c)
	}
	sort.Strings(available)

	return available
}

func filterJournalByRef(entries []map[string]any, refType string) []map[string]any {
	var filtered []map[string]any
	for _, e := range entries {
		if j.Str(e[fRefType]) == refType {
			filtered = append(filtered, e)
		}
	}

	return filtered
}

func tallyJournal(entries []map[string]any) (map[string]*journalTot, []map[string]any) {
	totals := map[string]*journalTot{}
	for _, e := range entries {
		amount := j.Float(e["amount"])
		name := j.Str(e[fRefType])
		if name == "" {
			name = vUnknown
		}
		b := totals[name]
		if b == nil {
			b = &journalTot{}
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
			fRefType: name, "entries": int(b.n), "income": isk(b.in), "spending": isk(b.out),
			"net_isk": mathRound(b.in+b.out, decimalPlaces),
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
	if len(byCat) > journalCategoryCap {
		byCat = byCat[:15]
	}

	return totals, byCat
}

func journalRows(entries []map[string]any) []map[string]any {
	rows := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, map[string]any{
			fDate: e[fDate], fRefType: e[fRefType], "amount": isk(e["amount"]),
			fDescription: e[fDescription], "amount_isk": e["amount"],
			"balance_after": isk(e["balance"]), "reason": e["reason"],
		})
	}

	return rows
}

func transactionSection(ctx context.Context, a *session.Session, path string, cid int, limit int, conciseMode bool) (map[string]any, error) {
	result, err := a.ESI.GetCursorPages(ctx, path, &cid, nil, "from_id", "transaction_id", txLookback, txPages)
	if err != nil {
		return nil, wrap("transactionSection", err)
	}

	return summarizeTransactions(ctx, a, cid, result.Data, result.StaleNote(), result.Truncated, limit, conciseMode)
}

func summarizeTransactions(ctx context.Context, a *session.Session, cid int, data any, stale string, truncated bool, limit int, conciseMode bool) (map[string]any, error) {
	entries := j.Maps(data)
	if len(entries) == 0 {
		return map[string]any{fTransactions: []any{}, fNote: "No market trades in the retained window."}, nil
	}
	typeSet, placeSet := map[int]struct{}{}, map[int]struct{}{}
	for _, t := range entries {
		typeSet[j.Int(t[fTypeID])] = struct{}{}
		placeSet[j.Int(t["location_id"])] = struct{}{}
	}
	typeNames, err := a.Resolver.Names(ctx, setToList(typeSet), nil)
	if err != nil {
		return nil, wrap("summarizeTransactions", err)
	}
	placeNames, err := a.Resolver.Names(ctx, setToList(placeSet), &cid)
	if err != nil {
		return nil, wrap("summarizeTransactions", err)
	}
	var bought, sold float64
	for _, t := range entries {
		total := j.Float(t["unit_price"]) * j.Float(t[fQuantity])
		if j.Bool(t["is_buy"]) {
			bought += total
		} else {
			sold += total
		}
	}
	sort.Slice(entries, func(i, k int) bool { return j.Str(entries[i][fDate]) > j.Str(entries[k][fDate]) })
	var rows []map[string]any
	for _, t := range entries {
		side := "sell"
		if j.Bool(t["is_buy"]) {
			side = "buy"
		}
		rows = append(rows, map[string]any{
			fDate: t[fDate], fSide: side, fItem: typeNames[j.Int(t[fTypeID])],
			fQuantity: t[fQuantity], fTotal: isk(j.Float(t["unit_price"]) * j.Float(t[fQuantity])),
			"unit_price": isk(t["unit_price"]), fLocation: nameOr(placeNames, j.Int(t["location_id"])),
		})
	}
	visible, meta := page(rows, limit, "")
	out := merge(map[string]any{
		"total_bought": isk(bought), "total_sold": isk(sold), "gross_margin": isk(sold - bought),
		"covers":        fmt.Sprintf("%v to %v (%s trades)", rows[len(rows)-1][fDate], rows[0][fDate], formatInt(len(entries))),
		"margin_caveat": "Sold minus bought over the trades in `covers`, not per-item profit.",
		fDataAge:        stale,
		fTransactions:   project(visible, []string{fDate, fSide, fItem, fQuantity, fTotal}, conciseMode),
	}, meta)
	if truncated {
		out["totals_caveat"] = fmt.Sprintf("Only the newest %s trades were read, so the totals cover `covers` and not the full retention window.", formatInt(len(entries)))
	}

	return out, nil
}

func formatInt(n int) string {
	return strconv.Itoa(n)
}
