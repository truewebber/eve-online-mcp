package eve

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/adapter/names"
	"github.com/truewebber/eve-online-mcp/internal/domain/j"
	"github.com/truewebber/eve-online-mcp/internal/domain/universe"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type marketPriceIn struct {
	Item        string `json:"item"                   jsonschema:"Exact item type name, e.g. 'Tritanium' or 'Rifter'. Must match the in-game name exactly."`
	Region      string `json:"region,omitempty"       jsonschema:"Exact region name. Empty means The Forge / Jita 4-4."`
	WholeRegion *bool  `json:"whole_region,omitempty" jsonschema:"Price across every station in the region instead of just the main hub."`
	HistoryDays int    `json:"history_days,omitempty" jsonschema:"Summarise this many days of daily price history. 0 skips it.,minimum=0,maximum=365"`
}

type marketOrdersIn struct {
	Character      string `json:"character,omitempty"       jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
	Limit          int    `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

type marketContractsIn struct {
	Character       string `json:"character,omitempty"        jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
	OutstandingOnly *bool  `json:"outstanding_only,omitempty" jsonschema:"Only contracts still awaiting action. Default true."`
	Limit           int    `json:"limit,omitempty"            jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	ResponseFormat  string `json:"response_format,omitempty"  jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

func registerMarket(s *mcp.Server) {
	addTool(s, &mcp.Tool{
		Name:        "eve_market_price",
		Description: "Live best buy and sell price for an item, from real orders on the market.\n\nUse this — not the average price in asset or mining results — whenever the answer involves actually buying or selling something. best_sell is what you would pay to buy right now; best_buy is what you would get selling instantly.\n\nReturns: best_sell, best_buy, spread_pct, volumes, ccp_average_price, packaged_volume_m3.",
	}, sessionTool(eveMarketPrice))
	addTool(s, &mcp.Tool{
		Name:        "eve_market_orders",
		Description: "The character's own open buy and sell orders, with fill progress and expiry.\n\nReturns: open_orders, sell_side_value, buy_escrow_locked, orders[].",
	}, sessionTool(eveMarketOrders))
	addTool(s, &mcp.Tool{
		Name:        "eve_market_contracts",
		Description: "Contracts the character issued or was assigned, newest first.\n\nCourier contracts are the ones with a collateral and a reward. Returns: total, outstanding, contracts[].",
	}, sessionTool(eveMarketContracts))
}

func eveMarketPrice(ctx context.Context, a *session.Session, in marketPriceIn) (any, error) {
	if strings.TrimSpace(in.Item) == "" {
		return map[string]any{fError: "item is required"}, nil
	}
	match, err := resolveNamed(ctx, a, in.Item, []string{catInventoryTypes})
	if err != nil {
		return nil, err
	}
	if match.Chosen == nil {
		return map[string]any{fError: fmt.Sprintf("No item type is named exactly %q. EVE names are exact — call eve_universe_search with this text to find the real spelling.", in.Item)}, nil
	}
	place, err := marketPlace(ctx, a, in)
	if err != nil {
		return nil, err
	}
	if place.err != "" {
		return map[string]any{fError: place.err}, nil
	}
	quotes, err := a.Resolver.HubQuotes(ctx, match.Chosen.ID, place.regionID, place.station)
	if err != nil {
		return nil, err
	}
	out, err := marketQuoteView(ctx, a, match, quotes, place, in.Item)
	if err != nil {
		return nil, err
	}
	if in.HistoryDays > 0 {
		h, histErr := marketHistory(ctx, a, match.Chosen.ID, place.regionID, in.HistoryDays)
		if histErr == nil {
			out["history"] = h
		}
	}

	return out, nil
}

func resolveNamed(ctx context.Context, a *session.Session, name string, only []string) (names.NameResolution, error) {
	resolved, err := a.Resolver.ResolveNames(ctx, []string{name}, nil, only)
	if err != nil {
		return names.NameResolution{}, err
	}

	return resolved[strings.ToLower(strings.TrimSpace(name))], nil
}

type marketPlaceResult struct {
	regionID   int
	regionName string
	station    *int
	err        string
}

func marketPlace(ctx context.Context, a *session.Session, in marketPriceIn) (marketPlaceResult, error) {
	out := marketPlaceResult{regionID: universe.TheForgeRegionID, regionName: "The Forge"}
	if strings.TrimSpace(in.Region) != "" {
		r, err := resolveNamed(ctx, a, in.Region, []string{"regions"})
		if err != nil {
			return marketPlaceResult{}, err
		}
		if r.Chosen == nil {
			return marketPlaceResult{err: fmt.Sprintf("No region is named exactly %q. Call eve_universe_search with categories='region' to find it.", in.Region)}, nil
		}
		out.regionID, out.regionName = r.Chosen.ID, r.Chosen.Name
	}
	if !boolDef(in.WholeRegion, false) && out.regionID == universe.TheForgeRegionID {
		out.station = new(universe.Jita44StationID)
	}

	return out, nil
}

func marketQuoteView(ctx context.Context, a *session.Session, match names.NameResolution, quotes map[string]any, place marketPlaceResult, item string) (map[string]any, error) {
	typeID := match.Chosen.ID
	average := a.Resolver.ReferencePrice(ctx, typeID)
	info, err := a.Resolver.TypeInfo(ctx, typeID)
	if err != nil {
		return nil, err
	}
	priced := "all of " + place.regionName
	if place.station != nil {
		priced = "Jita IV-4"
	}
	vol := info["packaged_volume"]
	if vol == nil {
		vol = info["volume"]
	}
	out := compact(map[string]any{
		fItem: match.Chosen.Name, "priced_at": priced,
		"best_sell": isk(quotes["best_sell"]), "best_sell_isk": quotes["best_sell"],
		"best_buy": isk(quotes["best_buy"]), "best_buy_isk": quotes["best_buy"],
		"spread_pct": marketSpread(quotes), "sell_volume_available": quotes["sell_volume"],
		"buy_volume_wanted": quotes["buy_volume"], "ccp_average_price": isk(average),
		"packaged_volume_m3": vol, fDataAge: quotes[fDataAge],
	})
	if quotes["best_sell"] == nil && quotes["best_buy"] == nil {
		out[fNote] = "No orders at all here. Try whole_region=true, or a different region — not everything is traded outside the main hubs."
	}
	if match.Ambiguous() {
		others := make([]string, 0, len(match.Alternatives))
		for _, m := range match.Alternatives {
			others = append(others, fmt.Sprintf("#%d", m.ID))
		}
		out["ambiguity_note"] = fmt.Sprintf("%d item types are named %q; priced #%d. Others: %s. Call eve_universe_search with categories='inventory_type' to pick.", len(match.Alternatives)+1, item, typeID, strings.Join(others, ", "))
	}

	return out, nil
}

func marketSpread(quotes map[string]any) any {
	if quotes["best_sell"] == nil || quotes["best_buy"] == nil {
		return nil
	}
	bs, bb := j.Float(quotes["best_sell"]), j.Float(quotes["best_buy"])
	if bs == 0 {
		return nil
	}

	return mathRound(percentScale*(bs-bb)/bs, decimalPlaces)
}

func eveMarketOrders(ctx context.Context, a *session.Session, in marketOrdersIn) (any, error) {
	token, err := a.ResolveCharacter(ctx, in.Character)
	if err != nil {
		return nil, err
	}
	if err := a.RequireScope(token, "esi-markets.read_character_orders.v1", "market orders"); err != nil {
		return nil, err
	}
	cid := token.CharacterID
	result, err := a.ESI.Get(ctx, fmt.Sprintf("/characters/%d/orders", cid), &cid, nil, nil)
	if err != nil {
		return nil, err
	}

	return formatOrders(ctx, a, token.CharacterName, cid, result.Data, result.StaleNote(), limitOr(in.Limit, limitLong), concise(in.ResponseFormat), nil)
}

func eveMarketContracts(ctx context.Context, a *session.Session, in marketContractsIn) (any, error) {
	token, err := a.ResolveCharacter(ctx, in.Character)
	if err != nil {
		return nil, err
	}
	if err := a.RequireScope(token, "esi-contracts.read_character_contracts.v1", fContracts); err != nil {
		return nil, err
	}
	cid := token.CharacterID
	result, err := a.ESI.GetAllPages(ctx, fmt.Sprintf("/characters/%d/contracts", cid), &cid, nil, pagesESI)
	if err != nil {
		return nil, err
	}

	return formatContracts(ctx, a, token.CharacterName, cid, result.Data, result.StaleNote(), boolDef(in.OutstandingOnly, true), limitOr(in.Limit, limitDefault), concise(in.ResponseFormat), false)
}

func marketHistory(ctx context.Context, a *session.Session, typeID, regionID, days int) (map[string]any, error) {
	result, err := a.ESI.Get(ctx, fmt.Sprintf("/markets/%d/history", regionID), nil, map[string]any{fTypeID: typeID}, nil)
	if err != nil {
		return nil, err
	}
	history := j.Maps(result.Data)
	if days > len(history) {
		days = len(history)
	}
	recent := history[len(history)-days:]
	if len(recent) == 0 {
		return map[string]any{fNote: "No trade history for this item in this region."}, nil
	}
	var avg, volume, first float64
	low, high := j.Float(recent[0]["lowest"]), j.Float(recent[0]["highest"])
	for i, h := range recent {
		avg += j.Float(h["average"])
		volume += j.Float(h["volume"])
		if i == 0 {
			first = j.Float(h["average"])
		}
		if j.Float(h["lowest"]) < low {
			low = j.Float(h["lowest"])
		}
		if j.Float(h["highest"]) > high {
			high = j.Float(h["highest"])
		}
	}
	avg /= float64(len(recent))
	var trend any
	if first != 0 {
		trend = mathRound(percentScale*(j.Float(recent[len(recent)-1]["average"])-first)/first, decimalPlaces)
	}

	return map[string]any{
		"days": len(recent), "average_price": isk(avg), "daily_volume": int(volume / float64(len(recent))),
		"period_low": isk(low), "period_high": isk(high), "trend_pct": trend,
	}, nil
}

func formatOrders(ctx context.Context, a *session.Session, character string, cid int, data any, stale string, limit int, conciseMode bool, walletNames map[int]string) (map[string]any, error) {
	orders := j.Maps(data)
	if len(orders) == 0 {
		return map[string]any{fCharacter: character, fOrders: []any{}, fNote: "No open market orders."}, nil
	}
	typeSet, placeSet := map[int]struct{}{}, map[int]struct{}{}
	for _, o := range orders {
		typeSet[j.Int(o[fTypeID])] = struct{}{}
		placeSet[j.Int(o["location_id"])] = struct{}{}
	}
	names, err := a.Resolver.Names(ctx, setToList(typeSet), nil)
	if err != nil {
		return nil, err
	}
	places, err := a.Resolver.Names(ctx, setToList(placeSet), &cid)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	var rows []map[string]any
	var sellValue, buyEscrow float64
	for _, o := range orders {
		isBuy := j.Bool(o["is_buy_order"])
		remaining := j.Int(o["volume_remain"])
		if isBuy {
			buyEscrow += j.Float(o["escrow"])
		} else {
			sellValue += j.Float(o[fPrice]) * float64(remaining)
		}
		issued := parseTime(j.Str(o["issued"]))
		expiresIn := vUnknown
		if issued != nil {
			expires := issued.Add(time.Duration(j.Int(o["duration"])) * 24 * time.Hour)
			expiresIn = humanDelta(expires.Sub(now))
		}
		var filled any
		if tot := j.Float(o["volume_total"]); tot != 0 {
			filled = mathRound(percentScale*(1-float64(remaining)/tot), 1)
		}
		side := "sell"
		if isBuy {
			side = "buy"
		}
		row := map[string]any{
			fSide: side, fItem: names[j.Int(o[fTypeID])], fPrice: isk(o[fPrice]),
			"remaining": remaining, "filled_pct": filled,
			fLocation: nameOr(places, j.Int(o["location_id"])), "expires_in": expiresIn,
		}
		if isBuy {
			row["range"] = o["range"]
			row["escrow"] = isk(o["escrow"])
		}
		if walletNames != nil {
			row[fWallet] = walletLabel(j.Int(o["wallet_division"]), walletNames)
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, k int) bool {
		if j.Str(rows[i][fSide]) != j.Str(rows[k][fSide]) {
			return j.Str(rows[i][fSide]) < j.Str(rows[k][fSide])
		}

		return j.Str(rows[i][fItem]) < j.Str(rows[k][fItem])
	})
	visible, meta := page(rows, limit, "")
	keep := []string{fSide, fItem, fPrice, "remaining", "filled_pct", fLocation, "expires_in"}
	if walletNames != nil {
		keep = append(keep, fWallet)
	}

	return merge(map[string]any{
		fCharacter: character, "open_orders": len(rows),
		"sell_side_value": isk(sellValue), "buy_escrow_locked": isk(buyEscrow),
		fDataAge: stale, fOrders: project(visible, keep, conciseMode),
	}, meta), nil
}

func formatContracts(ctx context.Context, a *session.Session, character string, cid int, data any, stale string, outstandingOnly bool, limit int, conciseMode, corp bool) (map[string]any, error) {
	contracts := j.Maps(data)
	if outstandingOnly {
		var filtered []map[string]any
		for _, c := range contracts {
			if j.Str(c[fStatus]) == vOutstanding {
				filtered = append(filtered, c)
			}
		}
		contracts = filtered
	}
	if len(contracts) == 0 {
		note := "This character has no contracts at all, in any state."
		if outstandingOnly {
			note = "No outstanding contracts. Pass outstanding_only=false to include finished and expired ones."
		}
		if corp && outstandingOnly {
			note = "No outstanding corporation contracts. Pass outstanding_only=false to include finished and expired ones."
		}

		return map[string]any{fCharacter: character, fContracts: []any{}, fNote: note}, nil
	}
	idSet := map[int]struct{}{}
	for _, c := range contracts {
		for _, k := range []string{"issuer_id", "assignee_id", "issuer_corporation_id", "acceptor_id", "start_location_id", "end_location_id"} {
			if j.Int(c[k]) != 0 {
				idSet[j.Int(c[k])] = struct{}{}
			}
		}
	}
	names, err := a.Resolver.Names(ctx, setToList(idSet), &cid)
	if err != nil {
		return nil, err
	}
	sort.Slice(contracts, func(i, k int) bool { return j.Str(contracts[i]["date_issued"]) > j.Str(contracts[k]["date_issued"]) })
	var rows []map[string]any
	outstanding := 0
	for _, c := range contracts {
		if j.Str(c[fStatus]) == vOutstanding {
			outstanding++
		}
		issuer := names[j.Int(c["issuer_id"])]
		if issuer == "" {
			issuer = names[j.Int(c["issuer_corporation_id"])]
		}
		row := map[string]any{
			fType: c[fType], fStatus: c[fStatus], fTitle: c[fTitle], "issuer": issuer,
			fPrice: nilIfZero(c[fPrice]), fReward: nilIfZero(c[fReward]), fCollateral: nilIfZero(c[fCollateral]),
			fFrom: names[j.Int(c["start_location_id"])], "to": names[j.Int(c["end_location_id"])],
			fExpires: c["date_expired"], "volume_m3": c["volume"], "issued": c["date_issued"],
			"contract_id": c["contract_id"],
		}
		if corp {
			row["assignee"] = names[j.Int(c["assignee_id"])]
		}
		rows = append(rows, row)
	}
	visible, meta := page(rows, limit, "")
	keep := []string{fType, fStatus, fTitle, fPrice, fReward, fCollateral, fFrom, "to", fExpires}
	if corp {
		keep = []string{fType, fStatus, fTitle, "issuer", fPrice, fReward, fCollateral, fFrom, "to", fExpires}
	}

	return merge(map[string]any{
		fCharacter: character, fTotal: len(rows), vOutstanding: outstanding,
		fDataAge: stale, fContracts: project(visible, keep, conciseMode),
	}, meta), nil
}

func nilIfZero(v any) any {
	if j.Float(v) == 0 {
		return nil
	}

	return isk(v)
}

func walletLabel(division int, names map[int]string) string {
	if division == 0 {
		return vUnknown
	}
	if n, ok := names[division]; ok && n != "" {
		return n
	}

	return fmt.Sprintf("Division %d", division)
}
