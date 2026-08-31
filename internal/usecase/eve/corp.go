package eve

import (
	"context"
	"fmt"
	"log"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/domain/character"
	"github.com/truewebber/eve-online-mcp/internal/domain/j"
	"github.com/truewebber/eve-online-mcp/internal/domain/write"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func hangarDivision(flag string) (int, bool) {
	switch flag {
	case "CorpSAG1":
		return 1, true
	case "CorpSAG2":
		return 2, true
	case "CorpSAG3":
		return 3, true
	case "CorpSAG4":
		return 4, true
	case "CorpSAG5":
		return 5, true
	case "CorpSAG6":
		return 6, true
	case "CorpSAG7":
		return 7, true
	default:
		return 0, false
	}
}

func corpScope(key string) string {
	switch key {
	case "assets":
		return "esi-assets.read_corporation_assets.v1"
	case "blueprints":
		return "esi-corporations.read_blueprints.v1"
	case "wallets":
		return "esi-wallet.read_corporation_wallets.v1"
	case "jobs":
		return "esi-industry.read_corporation_jobs.v1"
	case "mining":
		return "esi-industry.read_corporation_mining.v1"
	case "orders":
		return "esi-markets.read_corporation_orders.v1"
	case "contracts":
		return "esi-contracts.read_corporation_contracts.v1"
	case "killmails":
		return "esi-killmails.read_corporation_killmails.v1"
	case "structures":
		return "esi-corporations.read_structures.v1"
	case "members":
		return "esi-corporations.read_corporation_membership.v1"
	case "divisions":
		return "esi-corporations.read_divisions.v1"
	default:
		return ""
	}
}

func corpRole(key string) []string {
	switch key {
	case "assets", "blueprints", "killmails", "divisions":
		return []string{"Director"}
	case "wallets":
		return []string{"Accountant", "Junior_Accountant"}
	case "jobs":
		return []string{"Factory_Manager"}
	case "orders":
		return []string{"Accountant", "Trader"}
	case "structures", "mining_extractions":
		return []string{"Station_Manager"}
	case "mining_ledger":
		return []string{"Accountant"}
	default:
		return nil
	}
}

func esiRole(name string) bool {
	switch name {
	case "Director", "Accountant", "Junior_Accountant", "Factory_Manager", "Station_Manager", "Trader":
		return true
	default:
		return false
	}
}

type corpCharIn struct {
	Character string `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
}

type corpAssetsListIn struct {
	Character      string  `json:"character,omitempty"       jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
	Location       string  `json:"location,omitempty"        jsonschema:"Case-insensitive substring of a station or structure name."`
	MinValue       float64 `json:"min_value,omitempty"       jsonschema:"Hide locations holding less than this many ISK.,minimum=0"`
	Limit          int     `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	Items          int     `json:"items,omitempty"           jsonschema:"Maximum items per location in detailed mode.,minimum=1,maximum=200"`
	ResponseFormat string  `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

type corpAssetsFindIn struct {
	Name           string `json:"name"                      jsonschema:"Case-insensitive substring of the item type name."`
	Character      string `json:"character,omitempty"       jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
	Limit          int    `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

type corpBlueprintsIn struct {
	Character      string `json:"character,omitempty"       jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
	Limit          int    `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

type corpWalletIn struct {
	Character      string `json:"character,omitempty"       jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
	Kind           string `json:"kind,omitempty"            jsonschema:"balances (default), journal, transactions, or both."`
	Division       int    `json:"division,omitempty"        jsonschema:"Corporation wallet division, 1 through 7. Division 1 is the master wallet. Named divisions (if this character is a Director) come back from eve_corp_overview."`
	RefType        string `json:"ref_type,omitempty"        jsonschema:"Journal only: keep just one reason code."`
	Limit          int    `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

type corpIndustryJobsIn struct {
	Character        string `json:"character,omitempty"         jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
	IncludeCompleted *bool  `json:"include_completed,omitempty" jsonschema:"Also return jobs that already delivered."`
	Limit            int    `json:"limit,omitempty"             jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	ResponseFormat   string `json:"response_format,omitempty"   jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

type corpMiningIn struct {
	Character      string `json:"character,omitempty"       jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
	Limit          int    `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

type corpOrdersIn struct {
	Character      string `json:"character,omitempty"       jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
	Limit          int    `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

type corpContractsIn struct {
	Character       string `json:"character,omitempty"        jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
	OutstandingOnly *bool  `json:"outstanding_only,omitempty" jsonschema:"Only contracts still awaiting action. Default true."`
	Limit           int    `json:"limit,omitempty"            jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	ResponseFormat  string `json:"response_format,omitempty"  jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

type corpKillmailsIn struct {
	Character      string `json:"character,omitempty"       jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
	Limit          int    `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

type corpStructuresIn struct {
	Character      string `json:"character,omitempty"       jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
	Limit          int    `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

type corpMembersIn struct {
	Character      string `json:"character,omitempty"       jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
	Limit          int    `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

func registerCorp(s *mcp.Server) {
	addTool(s, &mcp.Tool{
		Name:        "eve_corp_overview",
		Description: "The corporation this character is in: ticker, wallets, roles, what you can read.\n\nThe right first call before any other eve_corp_* tool. Location-specific roles do not unlock ESI.\n\nReturns: corporation, ticker, alliance, member_count, ceo, tax_pct, roles, wallets[], available_tools[].",
	}, sessionTool(eveCorpOverview))
	addTool(s, &mcp.Tool{
		Name:        "eve_corp_assets_list",
		Description: "Corporation assets grouped by station or structure, with an ISK estimate. Needs the Director role. Large corps are truncated after 80 ESI pages.",
	}, sessionTool(eveCorpAssetsList))
	addTool(s, &mcp.Tool{
		Name:        "eve_corp_assets_find",
		Description: "Locate a specific item across every corp hangar, container and ship hold. Needs the Director role. Same search as eve_assets_find, but against the shared hangar — personal assets stay on that tool.",
	}, sessionTool(eveCorpAssetsFind))
	addTool(s, &mcp.Tool{
		Name:        "eve_corp_blueprints",
		Description: "Corporation blueprints with material/time efficiency and remaining runs. Needs the Director role. Personal BPOs stay on eve_assets_blueprints.",
	}, sessionTool(eveCorpBlueprints))
	addTool(s, &mcp.Tool{
		Name:        "eve_corp_wallet",
		Description: "Corporation ISK: the seven wallet divisions, plus journal and market trades. Needs Accountant or Junior_Accountant. Personal wallet stays on eve_wallet_history.",
	}, sessionTool(eveCorpWallet))
	addTool(s, &mcp.Tool{
		Name:        "eve_corp_industry_jobs",
		Description: "Corporation manufacturing, research, invention and reaction jobs. Needs Factory_Manager. Each row names the installer. Personal jobs stay on eve_industry_jobs.",
	}, sessionTool(eveCorpIndustryJobs))
	addTool(s, &mcp.Tool{
		Name:        "eve_corp_mining",
		Description: "Corporation moon-mining ledger and extraction timers. Accountant unlocks the observer ledger; Station_Manager unlocks extraction timers.",
	}, sessionTool(eveCorpMining))
	addTool(s, &mcp.Tool{
		Name:        "eve_corp_orders",
		Description: "The corporation's open buy and sell orders. Needs Accountant or Trader. Personal market orders stay on eve_market_orders.",
	}, sessionTool(eveCorpOrders))
	addTool(s, &mcp.Tool{
		Name:        "eve_corp_contracts",
		Description: "Contracts issued by or assigned to the corporation. Any member with the corporation-contracts scope can read them. Use outstanding_only to hide finished ones.",
	}, sessionTool(eveCorpContracts))
	addTool(s, &mcp.Tool{
		Name:        "eve_corp_killmails",
		Description: "Recent kills and losses involving this corporation. Needs the Director role. Personal killmails stay on eve_social_killmails.",
	}, sessionTool(eveCorpKillmails))
	addTool(s, &mcp.Tool{
		Name:        "eve_corp_structures",
		Description: "Upwell structures this corporation owns: fuel, state and services. Needs Station_Manager. fuel_expires_in is the one to watch.",
	}, sessionTool(eveCorpStructures))
	addTool(s, &mcp.Tool{
		Name:        "eve_corp_members",
		Description: "Current corporation members, alphabetically. Any member can read the roster. detailed adds ESI roles when this character is a Director.",
	}, sessionTool(eveCorpMembers))
}

func eveCorpOverview(ctx context.Context, a *session.Session, in corpCharIn) (any, error) {
	corp, err := a.ResolveCorporation(ctx, in.Character)
	if err != nil {
		return nil, err
	}
	out := corpOverviewIdentity(ctx, a, corp)
	if corp.IsNPC() {
		out["note"] = "NPC corporations have no hangars, wallets or jobs on ESI. The other eve_corp_* tools will refuse this character."
		out["available_tools"] = []string{}

		return keepEmpty(out, "roles", "available_tools"), nil
	}
	divs := corpDivisions(ctx, a, corp)
	corpOverviewAttachDivisions(out, divs)
	corpOverviewAttachWallets(ctx, a, corp, divs, out)
	out["available_tools"] = availableCorpTools(corp)
	corpOverviewNextStep(a, corp, out)

	return keepEmpty(out, "roles", "available_tools"), nil
}

func corpOverviewIdentity(ctx context.Context, a *session.Session, corp *character.Corporation) map[string]any {
	public := corp.Public
	ids := idsFrom(public["alliance_id"], public["ceo_id"])
	n, _ := a.Resolver.Names(ctx, ids, &corp.Token.CharacterID)

	return merge(map[string]any{
		"character": corp.CharacterName(), "corporation": corp.CorporationName,
		"ticker": corp.Ticker, "corporation_id": corp.CorporationID,
		"corporation_kind": map[bool]string{true: "npc", false: "player"}[corp.IsNPC()],
		"member_count":     public["member_count"], "ceo": n[j.Int(public["ceo_id"])],
		"alliance": n[j.Int(public["alliance_id"])],
		"tax_pct":  mathRound(j.Float(public["tax_rate"])*100, 2),
	}, rolesForDisplay(corp))
}

func corpOverviewAttachDivisions(out map[string]any, divs map[string]map[int]string) {
	if len(divs["wallet"]) == 0 && len(divs["hangar"]) == 0 {
		return
	}
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

func corpOverviewAttachWallets(ctx context.Context, a *session.Session, corp *character.Corporation, divs map[string]map[int]string, out map[string]any) {
	if !corpCan(corp, "wallets", "wallets") {
		return
	}
	wallets, err := a.ESI.Get(ctx, fmt.Sprintf("/corporations/%d/wallets", corp.CorporationID), &corp.Token.CharacterID, nil, nil)
	if err != nil {
		out["wallets_note"] = err.Error()

		return
	}
	bal := corpWalletRows(wallets.Data, divs["wallet"])
	out["wallets"] = bal.rows
	out["wallet_total"] = isk(bal.total)
	out["wallet_age"] = wallets.StaleNote()
}

func corpOverviewNextStep(a *session.Session, corp *character.Corporation, out map[string]any) {
	var missing []string
	for _, sc := range write.CorpReadScopes() {
		if !a.HasGranted(corp.Token.Scopes, sc) {
			missing = append(missing, sc)
		}
	}
	if len(missing) > 0 {
		out["next_step"] = fmt.Sprintf("%s's token is missing %d corporation scopes. Add those permissions on the EVE developer application, then call eve_auth_login_url and re-authorize.", corp.CharacterName(), len(missing))
	} else if len(j.Slice(out["available_tools"])) <= 1 {
		out["next_step"] = "This character has no corp roles that ESI honours. Someone with Director / Accountant / Factory_Manager / Station_Manager granted everywhere has to authorize instead."
	}
}

func eveCorpAssetsList(ctx context.Context, a *session.Session, in corpAssetsListIn) (any, error) {
	corp, err := openCorp(ctx, a, in.Character, "assets", "assets", "corporation assets")
	if err != nil {
		return nil, err
	}
	result, err := a.ESI.GetAllPages(ctx, fmt.Sprintf("/corporations/%d/assets", corp.CorporationID), &corp.Token.CharacterID, nil, 80)
	if err != nil {
		return nil, err
	}
	assets := j.Maps(result.Data)
	if len(assets) == 0 {
		return merge(who(corp), map[string]any{"locations": []any{}, "note": "The corporation hangar is empty (or this character cannot see it)."}), nil
	}
	divs := corpDivisions(ctx, a, corp)
	roots := rootLocations(assets)
	prices, _ := a.Resolver.ReferencePrices(ctx)
	typeNames, _ := a.Resolver.Names(ctx, collectTypeIDs(assets), nil)
	placeNames, _ := a.Resolver.Names(ctx, valuesOf(roots), &corp.Token.CharacterID)
	buckets := corpAssetBuckets(assets, roots, prices)
	rows := corpAssetLocationRows(buckets, placeNames, typeNames, prices, in)
	sort.Slice(rows, func(i, k int) bool { return j.Float(rows[i]["value_isk"]) > j.Float(rows[k]["value_isk"]) })
	visible, meta := page(rows, limitOr(in.Limit, 10), "Raise `limit`, or filter with `location` / `min_value`.")
	out := merge(who(corp), merge(map[string]any{
		"total_estimated_value": isk(corpAssetBucketTotal(buckets)), "total_locations": len(buckets),
		"matching_locations": len(rows), "valuation_basis": "CCP global average price per type, not a hub quote",
		"data_age":  result.StaleNote(),
		"locations": project(visible, []string{"location", "value", "distinct_types", "units"}, concise(in.ResponseFormat)),
	}, meta))
	if result.Truncated {
		out["totals_caveat"] = fmt.Sprintf("Stopped after 80 pages; totals cover the first %d stacks, not the whole hangar.", len(assets))
	}
	if len(divs["hangar"]) > 0 {
		out["hangar_names"] = divs["hangar"]
	}

	return out, nil
}

func collectTypeIDs(items []map[string]any) []int {
	typeIDs := make([]int, 0, len(items))
	for _, i := range items {
		typeIDs = append(typeIDs, j.Int(i["type_id"]))
	}

	return typeIDs
}

type corpAssetBucket struct {
	value float64
	units int
	types map[int]int
}

func corpAssetBuckets(assets []map[string]any, roots map[int]int, prices map[int]map[string]float64) map[int]*corpAssetBucket {
	buckets := map[int]*corpAssetBucket{}
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
			b = &corpAssetBucket{types: map[int]int{}}
			buckets[root] = b
		}
		tid := j.Int(item["type_id"])
		b.value += unitPrice(prices, tid) * float64(qty)
		b.units += qty
		b.types[tid] += qty
	}

	return buckets
}

func corpAssetLocationRows(buckets map[int]*corpAssetBucket, placeNames, typeNames map[int]string, prices map[int]map[string]float64, in corpAssetsListIn) []map[string]any {
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
		topItems := topItemLines(b.types, typeNames, prices, itemsN)
		rows = append(rows, map[string]any{
			"location": place, "value": isk(b.value), "value_isk": mathRound(b.value, 2),
			"distinct_types": len(b.types), "units": b.units, "location_id": placeID, "top_items": topItems,
		})
	}

	return rows
}

func corpAssetBucketTotal(buckets map[int]*corpAssetBucket) float64 {
	total := 0.0
	for _, b := range buckets {
		total += b.value
	}

	return total
}

func eveCorpAssetsFind(ctx context.Context, a *session.Session, in corpAssetsFindIn) (any, error) {
	corp, err := openCorp(ctx, a, in.Character, "assets", "assets", "corporation assets")
	if err != nil {
		return nil, err
	}
	result, err := a.ESI.GetAllPages(ctx, fmt.Sprintf("/corporations/%d/assets", corp.CorporationID), &corp.Token.CharacterID, nil, 80)
	if err != nil {
		return nil, err
	}
	items := j.Maps(result.Data)
	typeNames, _ := a.Resolver.Names(ctx, collectTypeIDs(items), nil)
	matches := corpAssetFindMatches(items, typeNames, in.Name)
	if len(matches) == 0 {
		return merge(who(corp), map[string]any{"query": in.Name, "matches": []any{}, "note": "Nothing matching in corporation assets. Check the spelling with eve_universe_search, or look in personal hangars with eve_assets_find."}), nil
	}
	divs := corpDivisions(ctx, a, corp)
	roots := rootLocations(items)
	placeNames, _ := a.Resolver.Names(ctx, corpAssetFindPlaceIDs(matches, roots), &corp.Token.CharacterID)
	prices, _ := a.Resolver.ReferencePrices(ctx)
	rows := corpAssetFindRows(matches, itemsByID(items), roots, typeNames, placeNames, prices, divs["hangar"])
	sort.Slice(rows, func(i, k int) bool { return j.Int(rows[i]["quantity"]) > j.Int(rows[k]["quantity"]) })
	visible, meta := page(rows, limitOr(in.Limit, 20), "")
	out := merge(who(corp), merge(map[string]any{
		"query": in.Name, "total_units": sumIntField(rows, "quantity"), "total_stacks": len(rows),
		"data_age": result.StaleNote(),
		"matches":  project(visible, []string{"item", "quantity", "location", "hangar", "estimated_value"}, concise(in.ResponseFormat)),
	}, meta))
	if result.Truncated {
		out["totals_caveat"] = fmt.Sprintf("Search covered the first %d stacks only (80-page cap).", len(items))
	}

	return out, nil
}

func itemsByID(items []map[string]any) map[int]map[string]any {
	byID := map[int]map[string]any{}
	for _, i := range items {
		byID[j.Int(i["item_id"])] = i
	}

	return byID
}

func corpAssetFindMatches(items []map[string]any, typeNames map[int]string, name string) []map[string]any {
	needle := strings.ToLower(strings.TrimSpace(name))
	var matches []map[string]any
	for _, i := range items {
		if strings.Contains(strings.ToLower(typeNames[j.Int(i["type_id"])]), needle) {
			matches = append(matches, i)
		}
	}

	return matches
}

func corpAssetFindPlaceIDs(matches []map[string]any, roots map[int]int) []int {
	placeSet := map[int]struct{}{}
	for _, i := range matches {
		if r, ok := roots[j.Int(i["item_id"])]; ok {
			placeSet[r] = struct{}{}
		}
	}

	return setToList(placeSet)
}

func corpAssetFindRows(matches []map[string]any, byID map[int]map[string]any, roots map[int]int, typeNames, placeNames map[int]string, prices map[int]map[string]float64, hangars map[int]string) []map[string]any {
	rows := make([]map[string]any, 0, len(matches))
	for _, item := range matches {
		qty := j.Int(item["quantity"])
		if qty == 0 {
			qty = 1
		}
		container := byID[j.Int(item["location_id"])]
		var inside any
		if container != nil {
			inside = typeNames[j.Int(container["type_id"])]
		}
		rows = append(rows, map[string]any{
			"item": typeNames[j.Int(item["type_id"])], "quantity": qty,
			"location": nameOr(placeNames, roots[j.Int(item["item_id"])]), "hangar": hangarLabel(j.Str(item["location_flag"]), hangars),
			"estimated_value": isk(unitPrice(prices, j.Int(item["type_id"])) * float64(qty)),
			"inside":          inside, "slot": item["location_flag"],
			"packaged": !j.Bool(item["is_singleton"]), "item_id": item["item_id"],
		})
	}

	return rows
}

func sumIntField(rows []map[string]any, key string) int {
	total := 0
	for _, r := range rows {
		total += j.Int(r[key])
	}

	return total
}

func eveCorpBlueprints(ctx context.Context, a *session.Session, in corpBlueprintsIn) (any, error) {
	corp, err := openCorp(ctx, a, in.Character, "blueprints", "blueprints", "corporation blueprints")
	if err != nil {
		return nil, err
	}
	result, err := a.ESI.GetAllPages(ctx, fmt.Sprintf("/corporations/%d/blueprints", corp.CorporationID), &corp.Token.CharacterID, nil, 40)
	if err != nil {
		return nil, err
	}
	bps := j.Maps(result.Data)
	if len(bps) == 0 {
		return merge(who(corp), map[string]any{"blueprints": []any{}, "note": "The corporation holds no blueprints."}), nil
	}
	divs := corpDivisions(ctx, a, corp)
	named := corpBlueprintNames(ctx, a, corp, bps)
	listed := corpBlueprintRows(bps, named.types, named.places, divs["hangar"])
	sort.Slice(listed.rows, func(i, k int) bool {
		if j.Str(listed.rows[i]["kind"]) != j.Str(listed.rows[k]["kind"]) {
			return j.Str(listed.rows[i]["kind"]) == "original"
		}

		return j.Int(listed.rows[i]["material_efficiency"]) > j.Int(listed.rows[k]["material_efficiency"])
	})
	visible, meta := page(listed.rows, limitOr(in.Limit, 25), "")

	return merge(who(corp), merge(map[string]any{
		"originals": listed.originals, "copies": listed.copies, "data_age": result.StaleNote(),
		"blueprints": project(visible, []string{"blueprint", "kind", "material_efficiency", "time_efficiency", "runs_left", "hangar"}, concise(in.ResponseFormat)),
	}, meta)), nil
}

type corpNameMaps struct {
	types, places map[int]string
}

func corpBlueprintNames(ctx context.Context, a *session.Session, corp *character.Corporation, bps []map[string]any) corpNameMaps {
	typeIDs := make([]int, 0, len(bps))
	placeIDs := make([]int, 0, len(bps))
	for _, b := range bps {
		typeIDs = append(typeIDs, j.Int(b["type_id"]))
		placeIDs = append(placeIDs, j.Int(b["location_id"]))
	}
	typeNames, _ := a.Resolver.Names(ctx, typeIDs, nil)
	placeNames, _ := a.Resolver.Names(ctx, placeIDs, &corp.Token.CharacterID)

	return corpNameMaps{typeNames, placeNames}
}

type corpBlueprintList struct {
	rows              []map[string]any
	originals, copies int
}

func corpBlueprintRows(bps []map[string]any, typeNames, placeNames map[int]string, hangars map[int]string) corpBlueprintList {
	rows := make([]map[string]any, 0, len(bps))
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
			"hangar": hangarLabel(j.Str(b["location_flag"]), hangars), "quantity": b["quantity"],
		})
	}

	return corpBlueprintList{rows, orig, copies}
}

func eveCorpWallet(ctx context.Context, a *session.Session, in corpWalletIn) (any, error) {
	corp, err := openCorp(ctx, a, in.Character, "wallets", "wallets", "corporation wallets")
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
	divs := corpDivisions(ctx, a, corp)
	if kind == "balances" {
		return corpWalletBalances(ctx, a, corp, divs)
	}

	return corpWalletMovements(ctx, a, corp, in, kind, div, divs)
}

type walletBalanceRows struct {
	rows  []map[string]any
	total float64
}

func corpWalletRows(data any, names map[int]string) walletBalanceRows {
	wallets := j.Maps(data)
	rows := make([]map[string]any, 0, len(wallets))
	total := 0.0
	for _, w := range wallets {
		rows = append(rows, map[string]any{
			"division": w["division"], "name": walletLabel(j.Int(w["division"]), names),
			"balance": isk(w["balance"]), "balance_isk": w["balance"],
		})
		total += j.Float(w["balance"])
	}

	return walletBalanceRows{rows, total}
}

func corpWalletBalances(ctx context.Context, a *session.Session, corp *character.Corporation, divs map[string]map[int]string) (any, error) {
	wallets, err := a.ESI.Get(ctx, fmt.Sprintf("/corporations/%d/wallets", corp.CorporationID), &corp.Token.CharacterID, nil, nil)
	if err != nil {
		return nil, err
	}
	bal := corpWalletRows(wallets.Data, divs["wallet"])

	return merge(who(corp), map[string]any{
		"wallet_total": isk(bal.total), "data_age": wallets.StaleNote(), "wallets": bal.rows,
		"note": "Pass kind='journal' or kind='transactions' with a division (1-7) to see movements. ESI retains about 30 days.",
	}), nil
}

func corpWalletMovements(ctx context.Context, a *session.Session, corp *character.Corporation, in corpWalletIn, kind string, div int, divs map[string]map[int]string) (any, error) {
	out := merge(who(corp), map[string]any{
		"division": div, "division_name": walletLabel(div, divs["wallet"]),
		"period": "last ~30 days (ESI retention limit)",
	})
	if kind == "journal" || kind == "both" {
		sec, err := corpWalletJournal(ctx, a, corp, div, in)
		if err != nil {
			return nil, err
		}
		out["journal_section"] = sec
	}
	if kind == "transactions" || kind == "both" {
		sec, err := transactionSection(ctx, a, fmt.Sprintf("/corporations/%d/wallets/%d/transactions", corp.CorporationID, div), corp.Token.CharacterID, limitOr(in.Limit, 15), concise(in.ResponseFormat))
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
}

func corpWalletJournal(ctx context.Context, a *session.Session, corp *character.Corporation, div int, in corpWalletIn) (map[string]any, error) {
	res, err := a.ESI.GetAllPages(ctx, fmt.Sprintf("/corporations/%d/wallets/%d/journal", corp.CorporationID, div), &corp.Token.CharacterID, nil, 10)
	if err != nil {
		return nil, err
	}

	return summarizeJournal(res.Data, res.StaleNote(), res.Truncated, 10, in.RefType, limitOr(in.Limit, 15), concise(in.ResponseFormat), fmt.Sprintf("division %d", div))
}

func eveCorpIndustryJobs(ctx context.Context, a *session.Session, in corpIndustryJobsIn) (any, error) {
	corp, err := openCorp(ctx, a, in.Character, "jobs", "jobs", "corporation industry jobs")
	if err != nil {
		return nil, err
	}
	result, err := a.ESI.GetAllPages(ctx, fmt.Sprintf("/corporations/%d/industry/jobs", corp.CorporationID), &corp.Token.CharacterID, map[string]any{"include_completed": boolDef(in.IncludeCompleted, false)}, 40)
	if err != nil {
		return nil, err
	}
	out := industryJobsResult(ctx, a, corp.CharacterName(), corp.Token.CharacterID, result.Data, result.StaleNote(), limitOr(in.Limit, 20), concise(in.ResponseFormat), true)

	return merge(who(corp), out), nil
}

func eveCorpMining(ctx context.Context, a *session.Session, in corpMiningIn) (any, error) {
	corp, err := a.ResolveCorporation(ctx, in.Character)
	if err != nil {
		return nil, err
	}
	if err := a.RequirePlayerCorp(corp); err != nil {
		return nil, err
	}
	if err := a.RequireGranted(corp.CharacterName(), corp.Token.Scopes, corpScope("mining"), "the corporation mining ledger"); err != nil {
		return nil, err
	}
	canLedger := corp.HasRole(corpRole("mining_ledger")...)
	canExtract := corp.HasRole(corpRole("mining_extractions")...)
	if err := corpMiningRequireRole(a, corp, canLedger, canExtract); err != nil {
		return nil, err
	}
	out := merge(who(corp), map[string]any{"period": "last ~30 days"})
	corpMiningAttachExtractions(ctx, a, corp, canExtract, out)
	corpMiningAttachLedger(ctx, a, corp, in, canLedger, out)

	return out, nil
}

func corpMiningRequireRole(a *session.Session, corp *character.Corporation, canLedger, canExtract bool) error {
	if canLedger || canExtract {
		return nil
	}

	return a.RequireCorpRole(corp, []string{"Accountant", "Station_Manager"}, "corporation mining (ledger needs Accountant, extractions need Station_Manager)")
}

func corpMiningAttachExtractions(ctx context.Context, a *session.Session, corp *character.Corporation, canExtract bool, out map[string]any) {
	if !canExtract {
		out["extractions_note"] = "Extraction timers need Station_Manager (or Director) granted everywhere."

		return
	}
	ex, err := corpExtractions(ctx, a, corp)
	if err != nil {
		out["extractions_note"] = err.Error()

		return
	}
	out["extractions"] = ex
}

func corpMiningAttachLedger(ctx context.Context, a *session.Session, corp *character.Corporation, in corpMiningIn, canLedger bool, out map[string]any) {
	if !canLedger {
		out["ledger_note"] = "The observer ledger needs Accountant (or Director) granted everywhere."

		return
	}
	ledger, err := corpMiningLedger(ctx, a, corp, limitOr(in.Limit, 15), concise(in.ResponseFormat))
	if err != nil {
		out["ledger_note"] = err.Error()

		return
	}
	merge(out, ledger)
}

func eveCorpOrders(ctx context.Context, a *session.Session, in corpOrdersIn) (any, error) {
	corp, err := openCorp(ctx, a, in.Character, "orders", "orders", "corporation market orders")
	if err != nil {
		return nil, err
	}
	result, err := a.ESI.GetAllPages(ctx, fmt.Sprintf("/corporations/%d/orders", corp.CorporationID), &corp.Token.CharacterID, nil, 40)
	if err != nil {
		return nil, err
	}
	divs := corpDivisions(ctx, a, corp)
	out := formatOrders(ctx, a, corp.CharacterName(), corp.Token.CharacterID, result.Data, result.StaleNote(), limitOr(in.Limit, 25), concise(in.ResponseFormat), divs["wallet"])

	return merge(who(corp), out), nil
}

func eveCorpContracts(ctx context.Context, a *session.Session, in corpContractsIn) (any, error) {
	corp, err := openCorp(ctx, a, in.Character, "contracts", "", "corporation contracts")
	if err != nil {
		return nil, err
	}
	result, err := a.ESI.GetAllPages(ctx, fmt.Sprintf("/corporations/%d/contracts", corp.CorporationID), &corp.Token.CharacterID, nil, 40)
	if err != nil {
		return nil, err
	}
	out := formatContracts(ctx, a, corp.CharacterName(), corp.Token.CharacterID, result.Data, result.StaleNote(), boolDef(in.OutstandingOnly, true), limitOr(in.Limit, 15), concise(in.ResponseFormat), true)

	return merge(who(corp), out), nil
}

func eveCorpKillmails(ctx context.Context, a *session.Session, in corpKillmailsIn) (any, error) {
	corp, err := openCorp(ctx, a, in.Character, "killmails", "killmails", "corporation killmails")
	if err != nil {
		return nil, err
	}
	out, err := formatKillmails(ctx, a, corp.CharacterName(), corp.Token.CharacterID, corp.CorporationID, fmt.Sprintf("/corporations/%d/killmails/recent", corp.CorporationID), limitOr(in.Limit, 8), concise(in.ResponseFormat))
	if err != nil {
		return nil, err
	}

	return merge(who(corp), j.Map(out)), nil
}

func eveCorpStructures(ctx context.Context, a *session.Session, in corpStructuresIn) (any, error) {
	corp, err := openCorp(ctx, a, in.Character, "structures", "structures", "corporation structures")
	if err != nil {
		return nil, err
	}
	result, err := a.ESI.GetAllPages(ctx, fmt.Sprintf("/corporations/%d/structures", corp.CorporationID), &corp.Token.CharacterID, nil, 40)
	if err != nil {
		return nil, err
	}
	structures := j.Maps(result.Data)
	if len(structures) == 0 {
		return merge(who(corp), map[string]any{"structures": []any{}, "note": "This corporation owns no Upwell structures."}), nil
	}
	names, _ := a.Resolver.Names(ctx, corpStructureIDs(structures), &corp.Token.CharacterID)
	listed := corpStructureRows(structures, names)
	sort.Slice(listed.rows, func(i, k int) bool {
		return j.Str(listed.rows[i]["fuel_expires"]) < j.Str(listed.rows[k]["fuel_expires"])
	})
	visible, meta := page(listed.rows, limitOr(in.Limit, 15), "")

	return merge(who(corp), merge(map[string]any{
		"structure_count": len(listed.rows), "unfuelled": listed.unfuelled, "data_age": result.StaleNote(),
		"structures": project(visible, []string{"structure", "type", "system", "state", "fuel_expires_in"}, concise(in.ResponseFormat)),
	}, meta)), nil
}

func corpStructureIDs(structures []map[string]any) []int {
	idSet := map[int]struct{}{}
	for _, s := range structures {
		for _, k := range []string{"type_id", "system_id", "structure_id"} {
			if j.Int(s[k]) != 0 {
				idSet[j.Int(s[k])] = struct{}{}
			}
		}
	}

	return setToList(idSet)
}

type corpStructureList struct {
	rows      []map[string]any
	unfuelled int
}

func corpStructureRows(structures []map[string]any, names map[int]string) corpStructureList {
	now := time.Now().UTC()
	rows := make([]map[string]any, 0, len(structures))
	unfuelled := 0
	for _, s := range structures {
		expires, dead := structureFuelExpires(parseTime(j.Str(s["fuel_expires"])), now)
		if dead {
			unfuelled++
		}
		rows = append(rows, map[string]any{
			"structure": names[j.Int(s["structure_id"])], "type": names[j.Int(s["type_id"])],
			"system": names[j.Int(s["system_id"])], "state": s["state"],
			"fuel_expires_in": expires, "fuel_expires": s["fuel_expires"],
			"reinforce_hour": s["reinforce_hour"], "services": structureServices(s), "structure_id": s["structure_id"],
		})
	}

	return corpStructureList{rows, unfuelled}
}

func structureFuelExpires(fuel *time.Time, now time.Time) (string, bool) {
	if fuel != nil && !fuel.After(now) {
		return "UNFUELLED", true
	}
	if fuel != nil {
		return humanDelta(fuel.Sub(now)), false
	}

	return "unknown", false
}

func structureServices(s map[string]any) any {
	listed := j.Maps(s["services"])
	services := make([]string, 0, len(listed))
	for _, svc := range listed {
		services = append(services, fmt.Sprintf("%v (%v)", svc["name"], svc["state"]))
	}
	if len(services) == 0 {
		return nil
	}

	return services
}

func eveCorpMembers(ctx context.Context, a *session.Session, in corpMembersIn) (any, error) {
	corp, err := openCorp(ctx, a, in.Character, "members", "", "corporation membership")
	if err != nil {
		return nil, err
	}
	result, err := a.ESI.GetAllPages(ctx, fmt.Sprintf("/corporations/%d/members", corp.CorporationID), &corp.Token.CharacterID, nil, 40)
	if err != nil {
		return nil, err
	}
	memberIDs := corpMemberIDs(result.Data)
	if len(memberIDs) == 0 {
		return merge(who(corp), map[string]any{"members": []any{}, "note": "ESI returned an empty roster."}), nil
	}
	names, _ := a.Resolver.Names(ctx, memberIDs, nil)
	rows := corpMemberRows(memberIDs, names, corpMemberRoleMap(ctx, a, corp, concise(in.ResponseFormat)))
	sort.Slice(rows, func(i, k int) bool {
		return strings.ToLower(j.Str(rows[i]["name"])) < strings.ToLower(j.Str(rows[k]["name"]))
	})
	visible, meta := page(rows, limitOr(in.Limit, 25), "")

	return merge(who(corp), merge(map[string]any{
		"member_count": len(rows), "data_age": result.StaleNote(),
		"members": project(visible, []string{"name"}, concise(in.ResponseFormat)),
	}, meta)), nil
}

func corpMemberIDs(data any) []int {
	var memberIDs []int
	for _, v := range j.Slice(data) {
		if id := j.Int(v); id != 0 {
			memberIDs = append(memberIDs, id)
		}
	}

	return memberIDs
}

func corpMemberRoleMap(ctx context.Context, a *session.Session, corp *character.Corporation, conciseMode bool) map[int][]string {
	roleMap := map[int][]string{}
	if conciseMode || !corp.HasRole("Director") {
		return roleMap
	}
	rolesRes, err := a.ESI.Get(ctx, fmt.Sprintf("/corporations/%d/roles", corp.CorporationID), &corp.Token.CharacterID, nil, nil)
	if err != nil {
		log.Printf("could not read corporation roles roster: %v", err)

		return roleMap
	}
	for _, row := range j.Maps(rolesRes.Data) {
		roleMap[j.Int(row["character_id"])] = corpRoleStrings(row["roles"])
	}

	return roleMap
}

func corpRoleStrings(roles any) []string {
	var rs []string
	for _, r := range j.Slice(roles) {
		if s, ok := r.(string); ok {
			rs = append(rs, s)
		}
	}

	return rs
}

func corpMemberRows(memberIDs []int, names map[int]string, roleMap map[int][]string) []map[string]any {
	rows := make([]map[string]any, 0, len(memberIDs))
	for _, mid := range memberIDs {
		var roles any
		if r := roleMap[mid]; len(r) > 0 {
			roles = r
		}
		rows = append(rows, map[string]any{"name": nameOr(names, mid), "character_id": mid, "roles": roles})
	}

	return rows
}

func openCorp(ctx context.Context, a *session.Session, character, scopeKey, roleKey, what string) (*character.Corporation, error) {
	corp, err := a.ResolveCorporation(ctx, character)
	if err != nil {
		return nil, err
	}
	if err := a.RequirePlayerCorp(corp); err != nil {
		return nil, err
	}
	if err := a.RequireGranted(corp.CharacterName(), corp.Token.Scopes, corpScope(scopeKey), what); err != nil {
		return nil, err
	}
	if roleKey != "" {
		err := a.RequireCorpRole(corp, corpRole(roleKey), what)
		if err != nil {
			return nil, err
		}
	}

	return corp, nil
}

func who(corp *character.Corporation) map[string]any {
	var ticker any
	if corp.Ticker != "" {
		ticker = corp.Ticker
	}

	return map[string]any{"character": corp.CharacterName(), "corporation": corp.CorporationName, "ticker": ticker}
}

func corpCan(corp *character.Corporation, scopeKey, roleKey string) bool {
	have := slices.Contains(corp.Token.Scopes, corpScope(scopeKey))

	return have && corp.HasRole(corpRole(roleKey)...)
}

func rolesForDisplay(corp *character.Corporation) map[string]any {
	if _, ok := corp.Roles["Director"]; ok {
		return map[string]any{
			"roles":     []string{"Director"},
			"role_note": "Director unlocks every eve_corp_* endpoint. Only roles granted everywhere count; HQ/base/other grants do not.",
		}
	}
	var esi []string
	for r := range corp.Roles {
		if esiRole(r) {
			esi = append(esi, r)
		}
	}
	sort.Strings(esi)
	out := map[string]any{
		"roles":     esi,
		"role_note": "Only roles granted everywhere unlock ESI. HQ/base/other grants do not.",
	}
	if len(esi) == 0 {
		out["roles_note"] = fmt.Sprintf("No ESI-relevant roles granted everywhere (Director, Accountant, Factory_Manager, Station_Manager, Trader). Raw role count: %d.", len(corp.Roles))
	}
	addLoc := func(key string, src map[string]struct{}) {
		var extra []string
		for r := range src {
			if !esiRole(r) {
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

func availableCorpTools(corp *character.Corporation) []string {
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
		if _, ok := have[corpScope(c.scope)]; !ok {
			continue
		}
		if c.role != "" && !corp.HasRole(corpRole(c.role)...) {
			continue
		}
		out = append(out, c.name)
	}
	if _, ok := have[corpScope("mining")]; ok && (corp.HasRole(corpRole("mining_ledger")...) || corp.HasRole(corpRole("mining_extractions")...)) {
		out = append(out, "eve_corp_mining")
	}

	return out
}

func corpDivisions(ctx context.Context, a *session.Session, corp *character.Corporation) map[string]map[int]string {
	empty := map[string]map[int]string{"wallet": {}, "hangar": {}}
	if !corpCan(corp, "divisions", "divisions") {
		return empty
	}
	result, err := a.ESI.Get(ctx, fmt.Sprintf("/corporations/%d/divisions", corp.CorporationID), &corp.Token.CharacterID, nil, nil)
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
	if n, ok := hangarDivision(flag); ok {
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

func corpExtractions(ctx context.Context, a *session.Session, corp *character.Corporation) ([]map[string]any, error) {
	result, err := a.ESI.Get(ctx, fmt.Sprintf("/corporation/%d/mining/extractions", corp.CorporationID), &corp.Token.CharacterID, nil, nil)
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
	names, _ := a.Resolver.Names(ctx, setToList(idSet), &corp.Token.CharacterID)
	now := time.Now().UTC()
	var out []map[string]any
	for _, e := range rows {
		out = append(out, corpExtractionRow(e, names, now))
	}
	sort.Slice(out, func(i, k int) bool { return j.Str(out[i]["chunk_arrival_time"]) < j.Str(out[k]["chunk_arrival_time"]) })

	return out, nil
}

func corpExtractionRow(e map[string]any, names map[int]string, now time.Time) map[string]any {
	arrival, decay := parseTime(j.Str(e["chunk_arrival_time"])), parseTime(j.Str(e["natural_decay_time"]))

	return map[string]any{
		"structure": names[j.Int(e["structure_id"])], "moon": names[j.Int(e["moon_id"])],
		"chunk_arrives_in": extractionDelta(arrival, now, "arrived"), "decays_in": extractionDelta(decay, now, "decayed"),
		"chunk_arrival_time": e["chunk_arrival_time"], "natural_decay_time": e["natural_decay_time"],
	}
}

func extractionDelta(t *time.Time, now time.Time, past string) string {
	if t == nil {
		return "unknown"
	}
	if !t.After(now) {
		return past
	}

	return humanDelta(t.Sub(now))
}

type miningLedgerAgg struct {
	totals, byMiner, byObserver map[int]int
	oldest                      float64
	failed                      int
	truncated                   bool
}

func corpMiningLedger(ctx context.Context, a *session.Session, corp *character.Corporation, limit int, conciseMode bool) (map[string]any, error) {
	observersRes, err := a.ESI.GetAllPages(ctx, fmt.Sprintf("/corporation/%d/mining/observers", corp.CorporationID), &corp.Token.CharacterID, nil, 40)
	if err != nil {
		return nil, err
	}
	observers := j.Maps(observersRes.Data)
	if len(observers) == 0 {
		return map[string]any{"ores": []any{}, "note": "No mining observers with recorded events (idle refineries are hidden).", "data_age": observersRes.StaleNote()}, nil
	}
	agg := fetchCorpMiningObservers(ctx, a, corp, observers, observersRes)
	names, _ := a.Resolver.Names(ctx, setToList(miningAggIDs(agg)), &corp.Token.CharacterID)
	prices, _ := a.Resolver.ReferencePrices(ctx)
	rows, grand := miningOreRows(agg.totals, names, prices)
	visible, meta := page(rows, limit, "")
	out := merge(map[string]any{
		"total_estimated_value": isk(grand), "observer_count": len(observers),
		"top_miners": topN(agg.byMiner, names, "miner"), "top_observers": topN(agg.byObserver, names, "observer"),
		"valuation_basis": "CCP global average price per type, not a hub quote",
		"data_age":        miningLedgerAge(observersRes, agg.oldest), "ores": visible,
	}, meta)
	if agg.failed > 0 {
		out["unavailable_observers"] = agg.failed
	}
	if agg.truncated {
		out["totals_caveat"] = "Ledger walk was capped (25 observers, 10 pages each); totals may be short."
	}
	_ = conciseMode

	return out, nil
}

func miningLedgerAge(observersRes esi.Result, oldest float64) string {
	if oldest < 60 {
		return fmt.Sprintf("%ds old", int(oldest))
	}

	return observersRes.StaleNote()
}

func miningAggIDs(agg miningLedgerAgg) map[int]struct{} {
	idSet := map[int]struct{}{}
	for k := range agg.totals {
		idSet[k] = struct{}{}
	}
	for k := range agg.byMiner {
		idSet[k] = struct{}{}
	}
	for k := range agg.byObserver {
		idSet[k] = struct{}{}
	}

	return idSet
}

func fetchCorpMiningObservers(ctx context.Context, a *session.Session, corp *character.Corporation, observers []map[string]any, observersRes esi.Result) miningLedgerAgg {
	capped := observers
	if len(capped) > 25 {
		capped = capped[:25]
	}
	ch := miningObserverPages(ctx, a, corp, capped)
	agg := miningLedgerAgg{
		totals: map[int]int{}, byMiner: map[int]int{}, byObserver: map[int]int{},
		oldest: observersRes.AgeSeconds, truncated: observersRes.Truncated || len(observers) > 25,
	}
	for range capped {
		absorbMiningObserver(&agg, <-ch)
	}

	return agg
}

type miningObsBox struct {
	obs map[string]any
	r   esi.Result
	err error
}

func miningObserverPages(ctx context.Context, a *session.Session, corp *character.Corporation, capped []map[string]any) <-chan miningObsBox {
	ch := make(chan miningObsBox, len(capped))
	for _, obs := range capped {
		go func(obs map[string]any) {
			if j.Int(obs["observer_id"]) == 0 {
				ch <- miningObsBox{obs, esi.Result{}, nil}

				return
			}
			r, err := a.ESI.GetAllPages(ctx, fmt.Sprintf("/corporation/%d/mining/observers/%d", corp.CorporationID, j.Int(obs["observer_id"])), &corp.Token.CharacterID, nil, 10)
			ch <- miningObsBox{obs, r, err}
		}(obs)
	}

	return ch
}

func absorbMiningObserver(agg *miningLedgerAgg, b miningObsBox) {
	if b.err != nil {
		agg.failed++
		log.Printf("mining observer %v failed: %v", b.obs["observer_id"], b.err)

		return
	}
	if b.r.AgeSeconds > agg.oldest {
		agg.oldest = b.r.AgeSeconds
	}
	agg.truncated = agg.truncated || b.r.Truncated
	oid := j.Int(b.obs["observer_id"])
	for _, entry := range j.Maps(b.r.Data) {
		qty := j.Int(entry["quantity"])
		agg.totals[j.Int(entry["type_id"])] += qty
		if j.Int(entry["character_id"]) != 0 {
			agg.byMiner[j.Int(entry["character_id"])] += qty
		}
		if oid != 0 {
			agg.byObserver[oid] += qty
		}
	}
}

func topN(m map[int]int, names map[int]string, label string) []map[string]any {
	type kv struct{ id, q int }
	var list []kv
	for id, q := range m {
		list = append(list, kv{id, q})
	}
	sort.Slice(list, func(i, k int) bool { return list[i].q > list[k].q })
	if len(list) > 5 {
		list = list[:5]
	}
	out := make([]map[string]any, 0, len(list))
	for _, x := range list {
		out = append(out, map[string]any{label: nameOr(names, x.id), "units": x.q})
	}

	return out
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
