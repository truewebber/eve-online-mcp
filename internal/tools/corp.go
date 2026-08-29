package tools

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"eve-mcp/internal/app"
	"eve-mcp/internal/config"
	"eve-mcp/internal/esi"
	"eve-mcp/internal/j"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var hangarFlags = map[string]int{
	"CorpSAG1": 1, "CorpSAG2": 2, "CorpSAG3": 3, "CorpSAG4": 4,
	"CorpSAG5": 5, "CorpSAG6": 6, "CorpSAG7": 7,
}

var corpScopes = map[string]string{
	"assets": "esi-assets.read_corporation_assets.v1",
	"blueprints": "esi-corporations.read_blueprints.v1",
	"wallets": "esi-wallet.read_corporation_wallets.v1",
	"jobs": "esi-industry.read_corporation_jobs.v1",
	"mining": "esi-industry.read_corporation_mining.v1",
	"orders": "esi-markets.read_corporation_orders.v1",
	"contracts": "esi-contracts.read_corporation_contracts.v1",
	"killmails": "esi-killmails.read_corporation_killmails.v1",
	"structures": "esi-corporations.read_structures.v1",
	"members": "esi-corporations.read_corporation_membership.v1",
	"divisions": "esi-corporations.read_divisions.v1",
}

var corpRoles = map[string][]string{
	"assets": {"Director"}, "blueprints": {"Director"},
	"wallets": {"Accountant", "Junior_Accountant"}, "jobs": {"Factory_Manager"},
	"orders": {"Accountant", "Trader"}, "killmails": {"Director"},
	"structures": {"Station_Manager"}, "divisions": {"Director"},
	"mining_ledger": {"Accountant"}, "mining_extractions": {"Station_Manager"},
}

var esiRoles = map[string]struct{}{
	"Director": {}, "Accountant": {}, "Junior_Accountant": {},
	"Factory_Manager": {}, "Station_Manager": {}, "Trader": {},
}

func registerCorp(s *mcp.Server, a *app.App) {
	type charIn struct {
		Character string `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
	}
	addTool(s, &mcp.Tool{
		Name: "eve_corp_overview",
		Description: "The corporation this character is in: ticker, wallets, roles, what you can read.\n\nThe right first call before any other eve_corp_* tool. Location-specific roles do not unlock ESI.\n\nReturns: corporation, ticker, alliance, member_count, ceo, tax_pct, roles, wallets[], available_tools[].",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in charIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *app.App) (any, error) {
			corp, err := a.ResolveCorporation(in.Character)
			if err != nil {
				return nil, err
			}
			public := corp.Public
			ids := idsFrom(public["alliance_id"], public["ceo_id"])
			n, _ := a.Resolver.Names(ids, &corp.Token.CharacterID)
			out := merge(map[string]any{
				"character": corp.CharacterName(), "corporation": corp.CorporationName,
				"ticker": corp.Ticker, "corporation_id": corp.CorporationID,
				"corporation_kind": map[bool]string{true: "npc", false: "player"}[corp.IsNPC()],
				"member_count": public["member_count"], "ceo": n[j.Int(public["ceo_id"])],
				"alliance": n[j.Int(public["alliance_id"])],
				"tax_pct": mathRound(j.Float(public["tax_rate"])*100, 2),
			}, rolesForDisplay(corp))
			if corp.IsNPC() {
				out["note"] = "NPC corporations have no hangars, wallets or jobs on ESI. The other eve_corp_* tools will refuse this character."
				out["available_tools"] = []string{}
				return keepEmpty(out, "roles", "available_tools"), nil
			}
			divs := corpDivisions(a, corp)
			if len(divs["wallet"]) > 0 || len(divs["hangar"]) > 0 {
				var wdiv, hdiv []map[string]any
				for i := 1; i <= 7; i++ {
					wdiv = append(wdiv, map[string]any{"division": i, "name": walletLabel(i, divs["wallet"])})
					hn := divs["hangar"][i]
					if hn == "" {
						hn = fmt.Sprintf("Hangar %d", i)
					}
					hdiv = append(hdiv, map[string]any{"division": i, "name": hn})
				}
				out["wallet_divisions"] = wdiv
				out["hangar_divisions"] = hdiv
			}
			if corpCan(corp, "wallets", "wallets") {
				wallets, err := a.ESI.Get(fmt.Sprintf("/corporations/%d/wallets", corp.CorporationID), &corp.Token.CharacterID, nil, nil)
				if err != nil {
					out["wallets_note"] = err.Error()
				} else {
					var rows []map[string]any
					total := 0.0
					for _, w := range j.Maps(wallets.Data) {
						rows = append(rows, map[string]any{
							"division": w["division"], "name": walletLabel(j.Int(w["division"]), divs["wallet"]),
							"balance": isk(w["balance"]), "balance_isk": w["balance"],
						})
						total += j.Float(w["balance"])
					}
					out["wallets"] = rows
					out["wallet_total"] = isk(total)
					out["wallet_age"] = wallets.StaleNote()
				}
			}
			out["available_tools"] = availableCorpTools(corp)
			var missing []string
			for _, sc := range config.CorpReadScopes {
				if !a.HasScope(corp.Token, sc) {
					missing = append(missing, sc)
				}
			}
			if len(missing) > 0 {
				out["next_step"] = fmt.Sprintf("%s's token is missing %d corporation scopes. Add those permissions on the EVE developer application, then call eve_auth_login_url and re-authorize.", corp.CharacterName(), len(missing))
			} else if len(j.Slice(out["available_tools"])) <= 1 {
				out["next_step"] = "This character has no corp roles that ESI honours. Someone with Director / Accountant / Factory_Manager / Station_Manager granted everywhere has to authorize instead."
			}
			return keepEmpty(out, "roles", "available_tools"), nil
		})
	})

	type assetsIn struct {
		Character      string  `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
		Location       string  `json:"location,omitempty" jsonschema:"Case-insensitive substring of a station or structure name."`
		MinValue       float64 `json:"min_value,omitempty" jsonschema:"Hide locations holding less than this many ISK.,minimum=0"`
		Limit          int     `json:"limit,omitempty" jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
		Items          int     `json:"items,omitempty" jsonschema:"Maximum items per location in detailed mode.,minimum=1,maximum=200"`
		ResponseFormat string  `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
	}
	addTool(s, &mcp.Tool{
		Name: "eve_corp_assets_list",
		Description: "Corporation assets grouped by station or structure, with an ISK estimate. Needs the Director role. Large corps are truncated after 80 ESI pages.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in assetsIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *app.App) (any, error) {
			corp, err := openCorp(a, in.Character, "assets", "assets", "corporation assets")
			if err != nil {
				return nil, err
			}
			result, err := a.ESI.GetAllPages(fmt.Sprintf("/corporations/%d/assets", corp.CorporationID), &corp.Token.CharacterID, nil, 80)
			if err != nil {
				return nil, err
			}
			assets := j.Maps(result.Data)
			if len(assets) == 0 {
				return merge(who(corp), map[string]any{"locations": []any{}, "note": "The corporation hangar is empty (or this character cannot see it)."}), nil
			}
			divs := corpDivisions(a, corp)
			roots := rootLocations(assets)
			prices, _ := a.Resolver.ReferencePrices()
			var typeIDs []int
			for _, i := range assets {
				typeIDs = append(typeIDs, j.Int(i["type_id"]))
			}
			typeNames, _ := a.Resolver.Names(typeIDs, nil)
			placeNames, _ := a.Resolver.Names(valuesOf(roots), &corp.Token.CharacterID)
			type bucket struct {
				value float64
				units int
				types map[int]int
			}
			buckets := map[int]*bucket{}
			for _, item := range assets {
				root, ok := roots[j.Int(item["item_id"])]
				if !ok {
					continue
				}
				qty := j.Int(item["quantity"])
				if qty == 0 {
					qty = 1
				}
				b := buckets[root]
				if b == nil {
					b = &bucket{types: map[int]int{}}
					buckets[root] = b
				}
				tid := j.Int(item["type_id"])
				b.value += unitPrice(prices, tid) * float64(qty)
				b.units += qty
				b.types[tid] += qty
			}
			needle := strings.ToLower(strings.TrimSpace(in.Location))
			itemsN := limitOr(in.Items, 5)
			var rows []map[string]any
			for placeID, b := range buckets {
				place := nameOr(placeNames, placeID)
				if needle != "" && !strings.Contains(strings.ToLower(place), needle) {
					continue
				}
				if b.value < in.MinValue {
					continue
				}
				type kv struct{ t, q int }
				var top []kv
				for t, q := range b.types {
					top = append(top, kv{t, q})
				}
				sort.Slice(top, func(i, k int) bool { return lineValue(prices, top[i].t, top[i].q) > lineValue(prices, top[k].t, top[k].q) })
				if len(top) > itemsN {
					top = top[:itemsN]
				}
				var topItems []string
				for _, x := range top {
					topItems = append(topItems, fmt.Sprintf("%v x%d (~%s)", nameOr(typeNames, x.t), x.q, isk(unitPrice(prices, x.t)*float64(x.q))))
				}
				rows = append(rows, map[string]any{
					"location": place, "value": isk(b.value), "value_isk": mathRound(b.value, 2),
					"distinct_types": len(b.types), "units": b.units, "location_id": placeID, "top_items": topItems,
				})
			}
			sort.Slice(rows, func(i, k int) bool { return j.Float(rows[i]["value_isk"]) > j.Float(rows[k]["value_isk"]) })
			visible, meta := page(rows, limitOr(in.Limit, 10), "Raise `limit`, or filter with `location` / `min_value`.")
			total := 0.0
			for _, b := range buckets {
				total += b.value
			}
			out := merge(who(corp), merge(map[string]any{
				"total_estimated_value": isk(total), "total_locations": len(buckets),
				"matching_locations": len(rows), "valuation_basis": "CCP global average price per type, not a hub quote",
				"data_age": result.StaleNote(),
				"locations": project(visible, []string{"location", "value", "distinct_types", "units"}, concise(in.ResponseFormat)),
			}, meta))
			if result.Truncated {
				out["totals_caveat"] = fmt.Sprintf("Stopped after 80 pages; totals cover the first %d stacks, not the whole hangar.", len(assets))
			}
			if len(divs["hangar"]) > 0 {
				out["hangar_names"] = divs["hangar"]
			}
			return out, nil
		})
	})

	type findIn struct {
		Name           string `json:"name" jsonschema:"Case-insensitive substring of the item type name."`
		Character      string `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
		Limit          int    `json:"limit,omitempty" jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
		ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
	}
	addTool(s, &mcp.Tool{
		Name: "eve_corp_assets_find",
		Description: "Locate a specific item across every corp hangar, container and ship hold. Needs the Director role. Same search as eve_assets_find, but against the shared hangar — personal assets stay on that tool.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in findIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *app.App) (any, error) {
			corp, err := openCorp(a, in.Character, "assets", "assets", "corporation assets")
			if err != nil {
				return nil, err
			}
			result, err := a.ESI.GetAllPages(fmt.Sprintf("/corporations/%d/assets", corp.CorporationID), &corp.Token.CharacterID, nil, 80)
			if err != nil {
				return nil, err
			}
			items := j.Maps(result.Data)
			var typeIDs []int
			for _, i := range items {
				typeIDs = append(typeIDs, j.Int(i["type_id"]))
			}
			typeNames, _ := a.Resolver.Names(typeIDs, nil)
			needle := strings.ToLower(strings.TrimSpace(in.Name))
			var matches []map[string]any
			for _, i := range items {
				if strings.Contains(strings.ToLower(typeNames[j.Int(i["type_id"])]), needle) {
					matches = append(matches, i)
				}
			}
			if len(matches) == 0 {
				return merge(who(corp), map[string]any{"query": in.Name, "matches": []any{}, "note": "Nothing matching in corporation assets. Check the spelling with eve_universe_search, or look in personal hangars with eve_assets_find."}), nil
			}
			divs := corpDivisions(a, corp)
			roots := rootLocations(items)
			byID := map[int]map[string]any{}
			for _, i := range items {
				byID[j.Int(i["item_id"])] = i
			}
			placeSet := map[int]struct{}{}
			for _, i := range matches {
				if r, ok := roots[j.Int(i["item_id"])]; ok {
					placeSet[r] = struct{}{}
				}
			}
			placeNames, _ := a.Resolver.Names(setToList(placeSet), &corp.Token.CharacterID)
			prices, _ := a.Resolver.ReferencePrices()
			var rows []map[string]any
			for _, item := range matches {
				root := roots[j.Int(item["item_id"])]
				container := byID[j.Int(item["location_id"])]
				qty := j.Int(item["quantity"])
				if qty == 0 {
					qty = 1
				}
				var inside any
				if container != nil {
					inside = typeNames[j.Int(container["type_id"])]
				}
				rows = append(rows, map[string]any{
					"item": typeNames[j.Int(item["type_id"])], "quantity": qty,
					"location": nameOr(placeNames, root), "hangar": hangarLabel(j.Str(item["location_flag"]), divs["hangar"]),
					"estimated_value": isk(unitPrice(prices, j.Int(item["type_id"]))*float64(qty)),
					"inside": inside, "slot": item["location_flag"],
					"packaged": !j.Bool(item["is_singleton"]), "item_id": item["item_id"],
				})
			}
			sort.Slice(rows, func(i, k int) bool { return j.Int(rows[i]["quantity"]) > j.Int(rows[k]["quantity"]) })
			visible, meta := page(rows, limitOr(in.Limit, 20), "")
			total := 0
			for _, r := range rows {
				total += j.Int(r["quantity"])
			}
			out := merge(who(corp), merge(map[string]any{
				"query": in.Name, "total_units": total, "total_stacks": len(rows),
				"data_age": result.StaleNote(),
				"matches": project(visible, []string{"item", "quantity", "location", "hangar", "estimated_value"}, concise(in.ResponseFormat)),
			}, meta))
			if result.Truncated {
				out["totals_caveat"] = fmt.Sprintf("Search covered the first %d stacks only (80-page cap).", len(items))
			}
			return out, nil
		})
	})

	type bpIn struct {
		Character      string `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
		Limit          int    `json:"limit,omitempty" jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
		ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
	}
	addTool(s, &mcp.Tool{
		Name: "eve_corp_blueprints",
		Description: "Corporation blueprints with material/time efficiency and remaining runs. Needs the Director role. Personal BPOs stay on eve_assets_blueprints.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in bpIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *app.App) (any, error) {
			corp, err := openCorp(a, in.Character, "blueprints", "blueprints", "corporation blueprints")
			if err != nil {
				return nil, err
			}
			result, err := a.ESI.GetAllPages(fmt.Sprintf("/corporations/%d/blueprints", corp.CorporationID), &corp.Token.CharacterID, nil, 40)
			if err != nil {
				return nil, err
			}
			bps := j.Maps(result.Data)
			if len(bps) == 0 {
				return merge(who(corp), map[string]any{"blueprints": []any{}, "note": "The corporation holds no blueprints."}), nil
			}
			divs := corpDivisions(a, corp)
			var typeIDs, placeIDs []int
			for _, b := range bps {
				typeIDs = append(typeIDs, j.Int(b["type_id"]))
				placeIDs = append(placeIDs, j.Int(b["location_id"]))
			}
			typeNames, _ := a.Resolver.Names(typeIDs, nil)
			placeNames, _ := a.Resolver.Names(placeIDs, &corp.Token.CharacterID)
			var rows []map[string]any
			orig, copies := 0, 0
			for _, b := range bps {
				kind := "original"
				var runs any
				if j.Float(b["runs"]) != -1 {
					kind = "copy"
					runs = b["runs"]
					copies++
				} else {
					orig++
				}
				rows = append(rows, map[string]any{
					"blueprint": typeNames[j.Int(b["type_id"])], "kind": kind,
					"material_efficiency": b["material_efficiency"], "time_efficiency": b["time_efficiency"],
					"runs_left": runs, "location": nameOr(placeNames, j.Int(b["location_id"])),
					"hangar": hangarLabel(j.Str(b["location_flag"]), divs["hangar"]), "quantity": b["quantity"],
				})
			}
			sort.Slice(rows, func(i, k int) bool {
				if j.Str(rows[i]["kind"]) != j.Str(rows[k]["kind"]) {
					return j.Str(rows[i]["kind"]) == "original"
				}
				return j.Int(rows[i]["material_efficiency"]) > j.Int(rows[k]["material_efficiency"])
			})
			visible, meta := page(rows, limitOr(in.Limit, 25), "")
			return merge(who(corp), merge(map[string]any{
				"originals": orig, "copies": copies, "data_age": result.StaleNote(),
				"blueprints": project(visible, []string{"blueprint", "kind", "material_efficiency", "time_efficiency", "runs_left", "hangar"}, concise(in.ResponseFormat)),
			}, meta)), nil
		})
	})

	type walletIn struct {
		Character      string `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
		Kind           string `json:"kind,omitempty" jsonschema:"balances (default), journal, transactions, or both."`
		Division       int    `json:"division,omitempty" jsonschema:"Corporation wallet division, 1 through 7. Division 1 is the master wallet. Named divisions (if this character is a Director) come back from eve_corp_overview."`
		RefType        string `json:"ref_type,omitempty" jsonschema:"Journal only: keep just one reason code."`
		Limit          int    `json:"limit,omitempty" jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
		ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
	}
	addTool(s, &mcp.Tool{
		Name: "eve_corp_wallet",
		Description: "Corporation ISK: the seven wallet divisions, plus journal and market trades. Needs Accountant or Junior_Accountant. Personal wallet stays on eve_wallet_history.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in walletIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *app.App) (any, error) {
			corp, err := openCorp(a, in.Character, "wallets", "wallets", "corporation wallets")
			if err != nil {
				return nil, err
			}
			kind := in.Kind
			if kind == "" {
				kind = "balances"
			}
			div := in.Division
			if div == 0 {
				div = 1
			}
			divs := corpDivisions(a, corp)
			if kind == "balances" {
				wallets, err := a.ESI.Get(fmt.Sprintf("/corporations/%d/wallets", corp.CorporationID), &corp.Token.CharacterID, nil, nil)
				if err != nil {
					return nil, err
				}
				var rows []map[string]any
				total := 0.0
				for _, w := range j.Maps(wallets.Data) {
					rows = append(rows, map[string]any{
						"division": w["division"], "name": walletLabel(j.Int(w["division"]), divs["wallet"]),
						"balance": isk(w["balance"]), "balance_isk": w["balance"],
					})
					total += j.Float(w["balance"])
				}
				return merge(who(corp), map[string]any{
					"wallet_total": isk(total), "data_age": wallets.StaleNote(), "wallets": rows,
					"note": "Pass kind='journal' or kind='transactions' with a division (1-7) to see movements. ESI retains about 30 days.",
				}), nil
			}
			out := merge(who(corp), map[string]any{
				"division": div, "division_name": walletLabel(div, divs["wallet"]),
				"period": "last ~30 days (ESI retention limit)",
			})
			if kind == "journal" || kind == "both" {
				res, err := a.ESI.GetAllPages(fmt.Sprintf("/corporations/%d/wallets/%d/journal", corp.CorporationID, div), &corp.Token.CharacterID, nil, 10)
				if err != nil {
					return nil, err
				}
				sec, err := summarizeJournal(res.Data, res.StaleNote(), res.Truncated, 10, in.RefType, limitOr(in.Limit, 15), concise(in.ResponseFormat), fmt.Sprintf("division %d", div))
				if err != nil {
					return nil, err
				}
				out["journal_section"] = sec
			}
			if kind == "transactions" || kind == "both" {
				sec, err := transactionSection(a, fmt.Sprintf("/corporations/%d/wallets/%d/transactions", corp.CorporationID, div), corp.Token.CharacterID, limitOr(in.Limit, 15), concise(in.ResponseFormat))
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

	type jobsIn struct {
		Character        string `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
		IncludeCompleted *bool  `json:"include_completed,omitempty" jsonschema:"Also return jobs that already delivered."`
		Limit            int    `json:"limit,omitempty" jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
		ResponseFormat   string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
	}
	addTool(s, &mcp.Tool{
		Name: "eve_corp_industry_jobs",
		Description: "Corporation manufacturing, research, invention and reaction jobs. Needs Factory_Manager. Each row names the installer. Personal jobs stay on eve_industry_jobs.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in jobsIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *app.App) (any, error) {
			corp, err := openCorp(a, in.Character, "jobs", "jobs", "corporation industry jobs")
			if err != nil {
				return nil, err
			}
			result, err := a.ESI.GetAllPages(fmt.Sprintf("/corporations/%d/industry/jobs", corp.CorporationID), &corp.Token.CharacterID, map[string]any{"include_completed": boolDef(in.IncludeCompleted, false)}, 40)
			if err != nil {
				return nil, err
			}
			out, err := industryJobsResult(a, corp.CharacterName(), corp.Token.CharacterID, result.Data, result.StaleNote(), limitOr(in.Limit, 20), concise(in.ResponseFormat), true)
			if err != nil {
				return nil, err
			}
			return merge(who(corp), out), nil
		})
	})

	type miningIn struct {
		Character      string `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
		Limit          int    `json:"limit,omitempty" jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
		ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
	}
	addTool(s, &mcp.Tool{
		Name: "eve_corp_mining",
		Description: "Corporation moon-mining ledger and extraction timers. Accountant unlocks the observer ledger; Station_Manager unlocks extraction timers.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in miningIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *app.App) (any, error) {
			corp, err := a.ResolveCorporation(in.Character)
			if err != nil {
				return nil, err
			}
			if err := a.RequirePlayerCorp(corp); err != nil {
				return nil, err
			}
			if err := a.RequireScope(corp.Token, corpScopes["mining"], "the corporation mining ledger"); err != nil {
				return nil, err
			}
			canLedger := corp.HasRole(corpRoles["mining_ledger"]...)
			canExtract := corp.HasRole(corpRoles["mining_extractions"]...)
			if !canLedger && !canExtract {
				if err := a.RequireCorpRole(corp, []string{"Accountant", "Station_Manager"}, "corporation mining (ledger needs Accountant, extractions need Station_Manager)"); err != nil {
					return nil, err
				}
			}
			out := merge(who(corp), map[string]any{"period": "last ~30 days"})
			if canExtract {
				ex, err := corpExtractions(a, corp)
				if err != nil {
					out["extractions_note"] = err.Error()
				} else {
					out["extractions"] = ex
				}
			} else {
				out["extractions_note"] = "Extraction timers need Station_Manager (or Director) granted everywhere."
			}
			if canLedger {
				ledger, err := corpMiningLedger(a, corp, limitOr(in.Limit, 15), concise(in.ResponseFormat))
				if err != nil {
					out["ledger_note"] = err.Error()
				} else {
					out = merge(out, ledger)
				}
			} else {
				out["ledger_note"] = "The observer ledger needs Accountant (or Director) granted everywhere."
			}
			return out, nil
		})
	})

	type ordersIn struct {
		Character      string `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
		Limit          int    `json:"limit,omitempty" jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
		ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
	}
	addTool(s, &mcp.Tool{
		Name: "eve_corp_orders",
		Description: "The corporation's open buy and sell orders. Needs Accountant or Trader. Personal market orders stay on eve_market_orders.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ordersIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *app.App) (any, error) {
			corp, err := openCorp(a, in.Character, "orders", "orders", "corporation market orders")
			if err != nil {
				return nil, err
			}
			result, err := a.ESI.GetAllPages(fmt.Sprintf("/corporations/%d/orders", corp.CorporationID), &corp.Token.CharacterID, nil, 40)
			if err != nil {
				return nil, err
			}
			divs := corpDivisions(a, corp)
			out, err := formatOrders(a, corp.CharacterName(), corp.Token.CharacterID, result.Data, result.StaleNote(), limitOr(in.Limit, 25), concise(in.ResponseFormat), divs["wallet"])
			if err != nil {
				return nil, err
			}
			return merge(who(corp), out), nil
		})
	})

	type contractsIn struct {
		Character       string `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
		OutstandingOnly *bool  `json:"outstanding_only,omitempty" jsonschema:"Only contracts still awaiting action. Default true."`
		Limit           int    `json:"limit,omitempty" jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
		ResponseFormat  string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
	}
	addTool(s, &mcp.Tool{
		Name: "eve_corp_contracts",
		Description: "Contracts issued by or assigned to the corporation. Any member with the corporation-contracts scope can read them. Use outstanding_only to hide finished ones.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in contractsIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *app.App) (any, error) {
			corp, err := openCorp(a, in.Character, "contracts", "", "corporation contracts")
			if err != nil {
				return nil, err
			}
			result, err := a.ESI.GetAllPages(fmt.Sprintf("/corporations/%d/contracts", corp.CorporationID), &corp.Token.CharacterID, nil, 40)
			if err != nil {
				return nil, err
			}
			out, err := formatContracts(a, corp.CharacterName(), corp.Token.CharacterID, result.Data, result.StaleNote(), boolDef(in.OutstandingOnly, true), limitOr(in.Limit, 15), concise(in.ResponseFormat), true)
			if err != nil {
				return nil, err
			}
			return merge(who(corp), out), nil
		})
	})

	type kmIn struct {
		Character      string `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
		Limit          int    `json:"limit,omitempty" jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
		ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
	}
	addTool(s, &mcp.Tool{
		Name: "eve_corp_killmails",
		Description: "Recent kills and losses involving this corporation. Needs the Director role. Personal killmails stay on eve_social_killmails.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in kmIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *app.App) (any, error) {
			corp, err := openCorp(a, in.Character, "killmails", "killmails", "corporation killmails")
			if err != nil {
				return nil, err
			}
			out, err := formatKillmails(a, corp.CharacterName(), corp.Token.CharacterID, corp.CorporationID, fmt.Sprintf("/corporations/%d/killmails/recent", corp.CorporationID), limitOr(in.Limit, 8), concise(in.ResponseFormat))
			if err != nil {
				return nil, err
			}
			return merge(who(corp), j.Map(out)), nil
		})
	})

	type stIn struct {
		Character      string `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
		Limit          int    `json:"limit,omitempty" jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
		ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
	}
	addTool(s, &mcp.Tool{
		Name: "eve_corp_structures",
		Description: "Upwell structures this corporation owns: fuel, state and services. Needs Station_Manager. fuel_expires_in is the one to watch.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in stIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *app.App) (any, error) {
			corp, err := openCorp(a, in.Character, "structures", "structures", "corporation structures")
			if err != nil {
				return nil, err
			}
			result, err := a.ESI.GetAllPages(fmt.Sprintf("/corporations/%d/structures", corp.CorporationID), &corp.Token.CharacterID, nil, 40)
			if err != nil {
				return nil, err
			}
			structures := j.Maps(result.Data)
			if len(structures) == 0 {
				return merge(who(corp), map[string]any{"structures": []any{}, "note": "This corporation owns no Upwell structures."}), nil
			}
			idSet := map[int]struct{}{}
			for _, s := range structures {
				for _, k := range []string{"type_id", "system_id", "structure_id"} {
					if j.Int(s[k]) != 0 {
						idSet[j.Int(s[k])] = struct{}{}
					}
				}
			}
			names, _ := a.Resolver.Names(setToList(idSet), &corp.Token.CharacterID)
			now := time.Now().UTC()
			var rows []map[string]any
			unfuelled := 0
			for _, s := range structures {
				fuel := parseTime(j.Str(s["fuel_expires"]))
				expires := "unknown"
				if fuel != nil && !fuel.After(now) {
					expires = "UNFUELLED"
					unfuelled++
				} else if fuel != nil {
					expires = humanDelta(fuel.Sub(now))
				}
				var services []string
				for _, svc := range j.Maps(s["services"]) {
					services = append(services, fmt.Sprintf("%v (%v)", svc["name"], svc["state"]))
				}
				var svc any
				if len(services) > 0 {
					svc = services
				}
				rows = append(rows, map[string]any{
					"structure": names[j.Int(s["structure_id"])], "type": names[j.Int(s["type_id"])],
					"system": names[j.Int(s["system_id"])], "state": s["state"],
					"fuel_expires_in": expires, "fuel_expires": s["fuel_expires"],
					"reinforce_hour": s["reinforce_hour"], "services": svc, "structure_id": s["structure_id"],
				})
			}
			sort.Slice(rows, func(i, k int) bool { return j.Str(rows[i]["fuel_expires"]) < j.Str(rows[k]["fuel_expires"]) })
			visible, meta := page(rows, limitOr(in.Limit, 15), "")
			return merge(who(corp), merge(map[string]any{
				"structure_count": len(rows), "unfuelled": unfuelled, "data_age": result.StaleNote(),
				"structures": project(visible, []string{"structure", "type", "system", "state", "fuel_expires_in"}, concise(in.ResponseFormat)),
			}, meta)), nil
		})
	})

	type memIn struct {
		Character      string `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
		Limit          int    `json:"limit,omitempty" jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
		ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
	}
	addTool(s, &mcp.Tool{
		Name: "eve_corp_members",
		Description: "Current corporation members, alphabetically. Any member can read the roster. detailed adds ESI roles when this character is a Director.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in memIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *app.App) (any, error) {
			corp, err := openCorp(a, in.Character, "members", "", "corporation membership")
			if err != nil {
				return nil, err
			}
			result, err := a.ESI.GetAllPages(fmt.Sprintf("/corporations/%d/members", corp.CorporationID), &corp.Token.CharacterID, nil, 40)
			if err != nil {
				return nil, err
			}
			var memberIDs []int
			for _, v := range j.Slice(result.Data) {
				if id := j.Int(v); id != 0 {
					memberIDs = append(memberIDs, id)
				}
			}
			if len(memberIDs) == 0 {
				return merge(who(corp), map[string]any{"members": []any{}, "note": "ESI returned an empty roster."}), nil
			}
			names, _ := a.Resolver.Names(memberIDs, nil)
			roleMap := map[int][]string{}
			if !concise(in.ResponseFormat) && corp.HasRole("Director") {
				rolesRes, err := a.ESI.Get(fmt.Sprintf("/corporations/%d/roles", corp.CorporationID), &corp.Token.CharacterID, nil, nil)
				if err != nil {
					log.Printf("could not read corporation roles roster: %v", err)
				} else {
					for _, row := range j.Maps(rolesRes.Data) {
						var rs []string
						for _, r := range j.Slice(row["roles"]) {
							if s, ok := r.(string); ok {
								rs = append(rs, s)
							}
						}
						roleMap[j.Int(row["character_id"])] = rs
					}
				}
			}
			var rows []map[string]any
			for _, mid := range memberIDs {
				var roles any
				if r := roleMap[mid]; len(r) > 0 {
					roles = r
				}
				rows = append(rows, map[string]any{"name": nameOr(names, mid), "character_id": mid, "roles": roles})
			}
			sort.Slice(rows, func(i, k int) bool { return strings.ToLower(j.Str(rows[i]["name"])) < strings.ToLower(j.Str(rows[k]["name"])) })
			visible, meta := page(rows, limitOr(in.Limit, 25), "")
			return merge(who(corp), merge(map[string]any{
				"member_count": len(rows), "data_age": result.StaleNote(),
				"members": project(visible, []string{"name"}, concise(in.ResponseFormat)),
			}, meta)), nil
		})
	})
}

func openCorp(a *app.App, character, scopeKey, roleKey, what string) (*app.Corporation, error) {
	corp, err := a.ResolveCorporation(character)
	if err != nil {
		return nil, err
	}
	if err := a.RequirePlayerCorp(corp); err != nil {
		return nil, err
	}
	if err := a.RequireScope(corp.Token, corpScopes[scopeKey], what); err != nil {
		return nil, err
	}
	if roleKey != "" {
		if err := a.RequireCorpRole(corp, corpRoles[roleKey], what); err != nil {
			return nil, err
		}
	}
	return corp, nil
}

func who(corp *app.Corporation) map[string]any {
	var ticker any
	if corp.Ticker != "" {
		ticker = corp.Ticker
	}
	return map[string]any{"character": corp.CharacterName(), "corporation": corp.CorporationName, "ticker": ticker}
}

func corpCan(corp *app.Corporation, scopeKey, roleKey string) bool {
	have := false
	for _, s := range corp.Token.Scopes {
		if s == corpScopes[scopeKey] {
			have = true
			break
		}
	}
	return have && corp.HasRole(corpRoles[roleKey]...)
}

func rolesForDisplay(corp *app.Corporation) map[string]any {
	if _, ok := corp.Roles["Director"]; ok {
		return map[string]any{
			"roles": []string{"Director"},
			"role_note": "Director unlocks every eve_corp_* endpoint. Only roles granted everywhere count; HQ/base/other grants do not.",
		}
	}
	var esi []string
	for r := range corp.Roles {
		if _, ok := esiRoles[r]; ok {
			esi = append(esi, r)
		}
	}
	sort.Strings(esi)
	out := map[string]any{
		"roles": esi,
		"role_note": "Only roles granted everywhere unlock ESI. HQ/base/other grants do not.",
	}
	if len(esi) == 0 {
		out["roles_note"] = fmt.Sprintf("No ESI-relevant roles granted everywhere (Director, Accountant, Factory_Manager, Station_Manager, Trader). Raw role count: %d.", len(corp.Roles))
	}
	addLoc := func(key string, src map[string]struct{}) {
		var extra []string
		for r := range src {
			if _, ok := esiRoles[r]; !ok {
				continue
			}
			if _, everywhere := corp.Roles[r]; !everywhere {
				extra = append(extra, r)
			}
		}
		if len(extra) > 0 {
			sort.Strings(extra)
			out[key] = extra
		}
	}
	addLoc("roles_at_hq", corp.RolesAtHQ)
	addLoc("roles_at_base", corp.RolesAtBase)
	addLoc("roles_at_other", corp.RolesAtOther)
	return out
}

func availableCorpTools(corp *app.Corporation) []string {
	catalog := []struct{ name, scope, role string }{
		{"eve_corp_assets_list", "assets", "assets"},
		{"eve_corp_assets_find", "assets", "assets"},
		{"eve_corp_blueprints", "blueprints", "blueprints"},
		{"eve_corp_wallet", "wallets", "wallets"},
		{"eve_corp_industry_jobs", "jobs", "jobs"},
		{"eve_corp_orders", "orders", "orders"},
		{"eve_corp_contracts", "contracts", ""},
		{"eve_corp_killmails", "killmails", "killmails"},
		{"eve_corp_structures", "structures", "structures"},
		{"eve_corp_members", "members", ""},
	}
	out := []string{"eve_corp_overview"}
	have := map[string]struct{}{}
	for _, s := range corp.Token.Scopes {
		have[s] = struct{}{}
	}
	for _, c := range catalog {
		if _, ok := have[corpScopes[c.scope]]; !ok {
			continue
		}
		if c.role != "" && !corp.HasRole(corpRoles[c.role]...) {
			continue
		}
		out = append(out, c.name)
	}
	if _, ok := have[corpScopes["mining"]]; ok && (corp.HasRole(corpRoles["mining_ledger"]...) || corp.HasRole(corpRoles["mining_extractions"]...)) {
		out = append(out, "eve_corp_mining")
	}
	return out
}

func corpDivisions(a *app.App, corp *app.Corporation) map[string]map[int]string {
	empty := map[string]map[int]string{"wallet": {}, "hangar": {}}
	if !corpCan(corp, "divisions", "divisions") {
		return empty
	}
	result, err := a.ESI.Get(fmt.Sprintf("/corporations/%d/divisions", corp.CorporationID), &corp.Token.CharacterID, nil, nil)
	if err != nil {
		log.Printf("could not read corporation divisions: %v", err)
		return empty
	}
	out := map[string]map[int]string{"wallet": {}, "hangar": {}}
	data := j.Map(result.Data)
	for _, kind := range []string{"wallet", "hangar"} {
		for _, row := range j.Maps(data[kind]) {
			if j.Int(row["division"]) == 0 {
				continue
			}
			if n := strings.TrimSpace(j.Str(row["name"])); n != "" {
				out[kind][j.Int(row["division"])] = n
			}
		}
	}
	return out
}

func hangarLabel(flag string, names map[int]string) any {
	if flag == "" {
		return nil
	}
	if n, ok := hangarFlags[flag]; ok {
		if names[n] != "" {
			return names[n]
		}
		return fmt.Sprintf("Hangar %d", n)
	}
	if flag == "CorpDeliveries" {
		return "Corp Deliveries"
	}
	if flag == "Impounded" {
		return "Impounded"
	}
	return flag
}

func corpExtractions(a *app.App, corp *app.Corporation) ([]map[string]any, error) {
	result, err := a.ESI.Get(fmt.Sprintf("/corporation/%d/mining/extractions", corp.CorporationID), &corp.Token.CharacterID, nil, nil)
	if err != nil {
		return nil, err
	}
	rows := j.Maps(result.Data)
	if len(rows) == 0 {
		return []map[string]any{}, nil
	}
	idSet := map[int]struct{}{}
	for _, e := range rows {
		if j.Int(e["structure_id"]) != 0 {
			idSet[j.Int(e["structure_id"])] = struct{}{}
		}
		if j.Int(e["moon_id"]) != 0 {
			idSet[j.Int(e["moon_id"])] = struct{}{}
		}
	}
	names, _ := a.Resolver.Names(setToList(idSet), &corp.Token.CharacterID)
	now := time.Now().UTC()
	var out []map[string]any
	for _, e := range rows {
		arrival, decay := parseTime(j.Str(e["chunk_arrival_time"])), parseTime(j.Str(e["natural_decay_time"]))
		arrives, decays := "unknown", "unknown"
		if arrival != nil && !arrival.After(now) {
			arrives = "arrived"
		} else if arrival != nil {
			arrives = humanDelta(arrival.Sub(now))
		}
		if decay != nil && !decay.After(now) {
			decays = "decayed"
		} else if decay != nil {
			decays = humanDelta(decay.Sub(now))
		}
		out = append(out, map[string]any{
			"structure": names[j.Int(e["structure_id"])], "moon": names[j.Int(e["moon_id"])],
			"chunk_arrives_in": arrives, "decays_in": decays,
			"chunk_arrival_time": e["chunk_arrival_time"], "natural_decay_time": e["natural_decay_time"],
		})
	}
	sort.Slice(out, func(i, k int) bool { return j.Str(out[i]["chunk_arrival_time"]) < j.Str(out[k]["chunk_arrival_time"]) })
	return out, nil
}

func corpMiningLedger(a *app.App, corp *app.Corporation, limit int, conciseMode bool) (map[string]any, error) {
	observersRes, err := a.ESI.GetAllPages(fmt.Sprintf("/corporation/%d/mining/observers", corp.CorporationID), &corp.Token.CharacterID, nil, 40)
	if err != nil {
		return nil, err
	}
	observers := j.Maps(observersRes.Data)
	if len(observers) == 0 {
		return map[string]any{"ores": []any{}, "note": "No mining observers with recorded events (idle refineries are hidden).", "data_age": observersRes.StaleNote()}, nil
	}
	capped := observers
	if len(capped) > 25 {
		capped = capped[:25]
	}
	type box struct {
		obs map[string]any
		r   esi.Result
		err error
	}
	ch := make(chan box, len(capped))
	for _, obs := range capped {
		go func(obs map[string]any) {
			if j.Int(obs["observer_id"]) == 0 {
				ch <- box{obs, esi.Result{}, nil}
				return
			}
			r, err := a.ESI.GetAllPages(fmt.Sprintf("/corporation/%d/mining/observers/%d", corp.CorporationID, j.Int(obs["observer_id"])), &corp.Token.CharacterID, nil, 10)
			ch <- box{obs, r, err}
		}(obs)
	}
	totals, byMiner, byObserver := map[int]int{}, map[int]int{}, map[int]int{}
	oldest := observersRes.AgeSeconds
	failed := 0
	truncated := observersRes.Truncated || len(observers) > 25
	for range capped {
		b := <-ch
		if b.err != nil {
			failed++
			log.Printf("mining observer %v failed: %v", b.obs["observer_id"], b.err)
			continue
		}
		if b.r.AgeSeconds > oldest {
			oldest = b.r.AgeSeconds
		}
		truncated = truncated || b.r.Truncated
		oid := j.Int(b.obs["observer_id"])
		for _, entry := range j.Maps(b.r.Data) {
			qty := j.Int(entry["quantity"])
			totals[j.Int(entry["type_id"])] += qty
			if j.Int(entry["character_id"]) != 0 {
				byMiner[j.Int(entry["character_id"])] += qty
			}
			if oid != 0 {
				byObserver[oid] += qty
			}
		}
	}
	idSet := map[int]struct{}{}
	for k := range totals {
		idSet[k] = struct{}{}
	}
	for k := range byMiner {
		idSet[k] = struct{}{}
	}
	for k := range byObserver {
		idSet[k] = struct{}{}
	}
	names, _ := a.Resolver.Names(setToList(idSet), &corp.Token.CharacterID)
	prices, _ := a.Resolver.ReferencePrices()
	var rows []map[string]any
	grand := 0.0
	for tid, qty := range totals {
		value := unitPrice(prices, tid) * float64(qty)
		grand += value
		rows = append(rows, map[string]any{"ore": nameOr(names, tid), "units": qty, "estimated_value": isk(value)})
	}
	sort.Slice(rows, func(i, k int) bool { return j.Int(rows[i]["units"]) > j.Int(rows[k]["units"]) })
	visible, meta := page(rows, limit, "")
	topN := func(m map[int]int, label string) []map[string]any {
		type kv struct{ id, q int }
		var list []kv
		for id, q := range m {
			list = append(list, kv{id, q})
		}
		sort.Slice(list, func(i, k int) bool { return list[i].q > list[k].q })
		if len(list) > 5 {
			list = list[:5]
		}
		var out []map[string]any
		for _, x := range list {
			out = append(out, map[string]any{label: nameOr(names, x.id), "units": x.q})
		}
		return out
	}
	age := observersRes.StaleNote()
	if oldest < 60 {
		age = fmt.Sprintf("%ds old", int(oldest))
	}
	out := merge(map[string]any{
		"total_estimated_value": isk(grand), "observer_count": len(observers),
		"top_miners": topN(byMiner, "miner"), "top_observers": topN(byObserver, "observer"),
		"valuation_basis": "CCP global average price per type, not a hub quote",
		"data_age": age, "ores": visible,
	}, meta)
	if failed > 0 {
		out["unavailable_observers"] = failed
	}
	if truncated {
		out["totals_caveat"] = "Ledger walk was capped (25 observers, 10 pages each); totals may be short."
	}
	_ = conciseMode
	return out, nil
}

func keepEmpty(m map[string]any, keep ...string) map[string]any {
	keepSet := map[string]struct{}{}
	for _, k := range keep {
		keepSet[k] = struct{}{}
	}
	out := map[string]any{}
	for k, v := range m {
		if emptyVal(v) {
			if _, ok := keepSet[k]; !ok {
				continue
			}
		}
		out[k] = v
	}
	return out
}
