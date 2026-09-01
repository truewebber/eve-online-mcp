package eve

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/j"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type walletHistIn struct {
	Kind           string `json:"kind,omitempty"            jsonschema:"'journal' is every ISK movement. 'transactions' is market trades. 'both' returns each in its own section. Default journal."`
	RefType        string `json:"ref_type,omitempty"        jsonschema:"Journal only: keep just one reason code, e.g. 'bounty_prizes'."`
	Limit          int    `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	Offset         int    `json:"offset,omitempty"          jsonschema:"Skip this many rows of the result before returning any. The result carries the total, so this is how you continue a long list."`
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
		sec, err := journalSection(ctx, a, journalQuery{
			cid: cid, refType: in.RefType, offset: in.Offset,
			limit: limitOr(in.Limit, limitDefault), conciseMode: concise(in.ResponseFormat),
		})
		if err != nil {
			return nil, err
		}
		out["journal_section"] = sec
	}
	if kind == fTransactions || kind == vBoth {
		sec, err := transactionSection(ctx, a, txQuery{
			path: esiPath("characters", esiID(cid), "wallet", "transactions"), cid: cid,
			offset: in.Offset, limit: limitOr(in.Limit, limitDefault), conciseMode: concise(in.ResponseFormat),
		})
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

type journalQuery struct {
	cid           int
	refType       string
	offset, limit int
	conciseMode   bool
}

func journalSection(ctx context.Context, a *session.Session, in journalQuery) (map[string]any, error) {
	result, err := a.ESI.GetAllPages(ctx, esiPath("characters", esiID(in.cid), "wallet", "journal"), &in.cid, nil, pagesShort)
	if err != nil {
		return nil, wrap("journalSection", err)
	}

	return summarizeJournal(journalSummary{
		data: result.Data, stale: result.StaleNote(), truncated: result.Truncated,
		pageCap: pagesShort, refType: in.refType, offset: in.offset, limit: in.limit,
		conciseMode: in.conciseMode,
	})
}

type journalTot struct{ in, out, n float64 }

type journalSummary struct {
	data          any
	stale         string
	truncated     bool
	pageCap       int
	refType       string
	offset, limit int
	conciseMode   bool
	divisionNote  string
}

func summarizeJournal(in journalSummary) (map[string]any, error) {
	entries := j.Maps(in.data)
	available := journalRefTypes(entries)
	if in.refType != "" {
		filtered := filterJournalByRef(entries, in.refType)
		if len(filtered) == 0 {
			msg := fmt.Sprintf("No journal entries with ref_type %q in the window. Codes actually present: %v", in.refType, available)
			if in.divisionNote != "" {
				msg = fmt.Sprintf("No journal entries with ref_type %q in %s. Codes actually present: %v", in.refType, in.divisionNote, available)
			}

			return map[string]any{fJournal: []any{}, fError: msg}, nil
		}
		entries = filtered
	}
	tally := tallyJournal(entries)
	sort.Slice(entries, func(i, k int) bool { return j.Str(entries[i][fDate]) > j.Str(entries[k][fDate]) })
	rows := journalRows(entries)
	paged := pageByOffset(rows, in.offset, in.limit, "Pass offset to continue, or narrow with `ref_type`.")
	var gin, gout float64
	for _, b := range tally.totals {
		gin += b.in
		gout += b.out
	}
	out := merge(map[string]any{
		"total_income": isk(gin), "total_spending": isk(gout), "net": isk(gin + gout),
		"by_category": tally.cats, fDataAge: in.stale,
		fJournal: project(paged.Rows, []string{fDate, fRefType, "amount", fDescription}, in.conciseMode),
	}, paged.fields)
	if in.truncated {
		out["totals_caveat"] = fmt.Sprintf("Hit the %d-page read cap: the totals and by_category above cover the newest %s entries, not the full window.", in.pageCap, formatInt(len(entries)))
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

type journalTally struct {
	totals map[string]*journalTot
	cats   []map[string]any
}

func tallyJournal(entries []map[string]any) journalTally {
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

	return journalTally{totals: totals, cats: byCat}
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

type txQuery struct {
	path          string
	cid           int
	offset, limit int
	conciseMode   bool
}

func transactionSection(ctx context.Context, a *session.Session, in txQuery) (map[string]any, error) {
	result, err := a.ESI.GetCursorPages(ctx, in.path, esi.CursorQuery{
		CharacterID: &in.cid, CursorParam: "from_id", CursorKey: "transaction_id",
		BatchSize: txLookback, MaxPages: txPages,
	})
	if err != nil {
		return nil, wrap("transactionSection", err)
	}

	return summarizeTransactions(ctx, a, txSummary{
		cid: in.cid, data: result.Data, stale: result.StaleNote(), truncated: result.Truncated,
		offset: in.offset, limit: in.limit, conciseMode: in.conciseMode,
	})
}

type txSummary struct {
	cid           int
	data          any
	stale         string
	truncated     bool
	offset, limit int
	conciseMode   bool
}

func summarizeTransactions(ctx context.Context, a *session.Session, in txSummary) (map[string]any, error) {
	entries := j.Maps(in.data)
	if len(entries) == 0 {
		return map[string]any{fTransactions: []any{}, fNote: "No market trades in the retained window.", fDataAge: in.stale}, nil
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
	placeNames, err := a.Resolver.Names(ctx, setToList(placeSet), &in.cid)
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
	paged := pageByOffset(rows, in.offset, in.limit, "")
	out := merge(map[string]any{
		"total_bought": isk(bought), "total_sold": isk(sold), "gross_margin": isk(sold - bought),
		"covers":        fmt.Sprintf("%v to %v (%s trades)", rows[len(rows)-1][fDate], rows[0][fDate], formatInt(len(entries))),
		"margin_caveat": "Sold minus bought over the trades in `covers`, not per-item profit.",
		fDataAge:        in.stale,
		fTransactions:   project(paged.Rows, []string{fDate, fSide, fItem, fQuantity, fTotal}, in.conciseMode),
	}, paged.fields)
	if in.truncated {
		out["totals_caveat"] = fmt.Sprintf("Only the newest %s trades were read, so the totals cover `covers` and not the full retention window.", formatInt(len(entries)))
	}

	return out, nil
}

func formatInt(n int) string {
	return strconv.Itoa(n)
}
