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
	const prefix = "CorpSAG"
	if !strings.HasPrefix(flag, prefix) || len(flag) != len(prefix)+1 {
		return 0, false
	}
	n := int(flag[len(prefix)] - '0')
	if n < 1 || n > corpHangarCount {
		return 0, false
	}

	return n, true
}

func corpScope(key string) string {
	switch key {
	case fAssets:
		return "esi-assets.read_corporation_assets.v1"
	case fBlueprints:
		return "esi-corporations.read_blueprints.v1"
	case fWallets:
		return "esi-wallet.read_corporation_wallets.v1"
	case fJobs:
		return "esi-industry.read_corporation_jobs.v1"
	case "mining":
		return "esi-industry.read_corporation_mining.v1"
	case fOrders:
		return "esi-markets.read_corporation_orders.v1"
	case fContracts:
		return "esi-contracts.read_corporation_contracts.v1"
	case fKillmails:
		return "esi-killmails.read_corporation_killmails.v1"
	case fStructures:
		return "esi-corporations.read_structures.v1"
	case fMembers:
		return "esi-corporations.read_corporation_membership.v1"
	case "divisions":
		return "esi-corporations.read_divisions.v1"
	default:
		return ""
	}
}

func corpRole(key string) []string {
	switch key {
	case fAssets, fBlueprints, fKillmails, "divisions":
		return []string{roleDirector}
	case fWallets:
		return []string{roleAccountant, "Junior_Accountant"}
	case fJobs:
		return []string{"Factory_Manager"}
	case fOrders:
		return []string{roleAccountant, "Trader"}
	case fStructures, "mining_extractions":
		return []string{roleStationManager}
	case "mining_ledger":
		return []string{roleAccountant}
	default:
		return nil
	}
}

func esiRole(name string) bool {
	switch name {
	case roleDirector, roleAccountant, "Junior_Accountant", "Factory_Manager", roleStationManager, "Trader":
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
	out, err := corpOverviewIdentity(ctx, a, corp)
	if err != nil {
		return nil, err
	}
	if corp.IsNPC() {
		out[fNote] = "NPC corporations have no hangars, wallets or jobs on ESI. The other eve_corp_* tools will refuse this character."
		out["available_tools"] = []string{}

		return keepEmpty(out, fRoles, "available_tools"), nil
	}
	divs := corpDivisions(ctx, a, corp)
	corpOverviewAttachDivisions(out, divs)
	corpOverviewAttachWallets(ctx, a, corp, divs, out)
	out["available_tools"] = availableCorpTools(corp)
	corpOverviewNextStep(a, corp, out)

	return keepEmpty(out, fRoles, "available_tools"), nil
}

func corpOverviewIdentity(ctx context.Context, a *session.Session, corp *character.Corporation) (map[string]any, error) {
	public := corp.Public
	ids := idsFrom(public["alliance_id"], public["ceo_id"])
	n, err := a.Resolver.Names(ctx, ids, &corp.Token.CharacterID)
	if err != nil {
		return nil, err
	}

	return merge(map[string]any{
		fCharacter: corp.CharacterName(), fCorporation: corp.CorporationName,
		"ticker": corp.Ticker, "corporation_id": corp.CorporationID,
		"corporation_kind": map[bool]string{true: "npc", false: "player"}[corp.IsNPC()],
		"member_count":     public["member_count"], "ceo": n[j.Int(public["ceo_id"])],
		fAlliance: n[j.Int(public["alliance_id"])],
		"tax_pct": mathRound(j.Float(public["tax_rate"])*percentScale, decimalPlaces),
	}, rolesForDisplay(corp)), nil
}

func corpOverviewAttachDivisions(out map[string]any, divs map[string]map[int]string) {
	if len(divs[fWallet]) == 0 && len(divs[fHangar]) == 0 {
		return
	}
	var wdiv, hdiv []map[string]any
	for i := 1; i <= 7; i++ {
		wdiv = append(wdiv, map[string]any{fDivision: i, fName: walletLabel(i, divs[fWallet])})
		hn := divs[fHangar][i]
		if hn == "" {
			hn = fmt.Sprintf("Hangar %d", i)
		}
		hdiv = append(hdiv, map[string]any{fDivision: i, fName: hn})
	}
	out["wallet_divisions"] = wdiv
	out["hangar_divisions"] = hdiv
}

func corpOverviewAttachWallets(ctx context.Context, a *session.Session, corp *character.Corporation, divs map[string]map[int]string, out map[string]any) {
	if !corpCan(corp, fWallets, fWallets) {
		return
	}
	wallets, err := a.ESI.Get(ctx, fmt.Sprintf("/corporations/%d/wallets", corp.CorporationID), &corp.Token.CharacterID, nil, nil)
	if err != nil {
		out["wallets_note"] = err.Error()

		return
	}
	bal := corpWalletRows(wallets.Data, divs[fWallet])
	out[fWallets] = bal.rows
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
	corp, err := openCorp(ctx, a, in.Character, fAssets, fAssets, "corporation assets")
	if err != nil {
		return nil, err
	}
	result, err := a.ESI.GetAllPages(ctx, fmt.Sprintf("/corporations/%d/assets", corp.CorporationID), &corp.Token.CharacterID, nil, pagesCorpAssets)
	if err != nil {
		return nil, err
	}
	assets := j.Maps(result.Data)
	if len(assets) == 0 {
		return merge(who(corp), map[string]any{fLocations: []any{}, fNote: "The corporation hangar is empty (or this character cannot see it)."}), nil
	}
	divs := corpDivisions(ctx, a, corp)
	roots := rootLocations(assets)
	prices, err := a.Resolver.ReferencePrices(ctx)
	if err != nil {
		return nil, err
	}
	typeNames, err := a.Resolver.Names(ctx, collectTypeIDs(assets), nil)
	if err != nil {
		return nil, err
	}
	placeNames, err := a.Resolver.Names(ctx, valuesOf(roots), &corp.Token.CharacterID)
	if err != nil {
		return nil, err
	}
	buckets := corpAssetBuckets(assets, roots, prices)
	rows := corpAssetLocationRows(buckets, placeNames, typeNames, prices, in)
	sort.Slice(rows, func(i, k int) bool { return j.Float(rows[i]["value_isk"]) > j.Float(rows[k]["value_isk"]) })
	visible, meta := page(rows, limitOr(in.Limit, limitShort), "Raise `limit`, or filter with `location` / `min_value`.")
	out := merge(who(corp), merge(map[string]any{
		fTotalEstimatedValue: isk(corpAssetBucketTotal(buckets)), "total_locations": len(buckets),
		"matching_locations": len(rows), fValuationBasis: valuationCCPAvg,
		fDataAge:   result.StaleNote(),
		fLocations: project(visible, []string{fLocation, fValue, fDistinctTypes, fUnits}, concise(in.ResponseFormat)),
	}, meta))
	if result.Truncated {
		out["totals_caveat"] = fmt.Sprintf("Stopped after 80 pages; totals cover the first %d stacks, not the whole hangar.", len(assets))
	}
	if len(divs[fHangar]) > 0 {
		out["hangar_names"] = divs[fHangar]
	}

	return out, nil
}

func collectTypeIDs(items []map[string]any) []int {
	typeIDs := make([]int, 0, len(items))
	for _, i := range items {
		typeIDs = append(typeIDs, j.Int(i[fTypeID]))
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
		qty := j.Int(item[fQuantity])
		if qty == 0 {
			qty = 1
		}
		b := buckets[root]
		if b == nil {
			b = &corpAssetBucket{types: map[int]int{}}
			buckets[root] = b
		}
		tid := j.Int(item[fTypeID])
		b.value += unitPrice(prices, tid) * float64(qty)
		b.units += qty
		b.types[tid] += qty
	}

	return buckets
}

func corpAssetLocationRows(buckets map[int]*corpAssetBucket, placeNames, typeNames map[int]string, prices map[int]map[string]float64, in corpAssetsListIn) []map[string]any {
	needle := strings.ToLower(strings.TrimSpace(in.Location))
	itemsN := limitOr(in.Items, limitTopItems)
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
			fLocation: place, fValue: isk(b.value), "value_isk": mathRound(b.value, decimalPlaces),
			fDistinctTypes: len(b.types), fUnits: b.units, "location_id": placeID, "top_items": topItems,
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
	corp, err := openCorp(ctx, a, in.Character, fAssets, fAssets, "corporation assets")
	if err != nil {
		return nil, err
	}
	result, err := a.ESI.GetAllPages(ctx, fmt.Sprintf("/corporations/%d/assets", corp.CorporationID), &corp.Token.CharacterID, nil, pagesCorpAssets)
	if err != nil {
		return nil, err
	}
	items := j.Maps(result.Data)
	typeNames, err := a.Resolver.Names(ctx, collectTypeIDs(items), nil)
	if err != nil {
		return nil, err
	}
	matches := corpAssetFindMatches(items, typeNames, in.Name)
	if len(matches) == 0 {
		return merge(who(corp), map[string]any{fQuery: in.Name, fMatches: []any{}, fNote: "Nothing matching in corporation assets. Check the spelling with eve_universe_search, or look in personal hangars with eve_assets_find."}), nil
	}
	divs := corpDivisions(ctx, a, corp)
	roots := rootLocations(items)
	placeNames, err := a.Resolver.Names(ctx, corpAssetFindPlaceIDs(matches, roots), &corp.Token.CharacterID)
	if err != nil {
		return nil, err
	}
	prices, err := a.Resolver.ReferencePrices(ctx)
	if err != nil {
		return nil, err
	}
	rows := corpAssetFindRows(matches, itemsByID(items), roots, typeNames, placeNames, prices, divs[fHangar])
	sort.Slice(rows, func(i, k int) bool { return j.Int(rows[i][fQuantity]) > j.Int(rows[k][fQuantity]) })
	visible, meta := page(rows, limitOr(in.Limit, limitMedium), "")
	out := merge(who(corp), merge(map[string]any{
		fQuery: in.Name, "total_units": sumIntField(rows, fQuantity), "total_stacks": len(rows),
		fDataAge: result.StaleNote(),
		fMatches: project(visible, []string{fItem, fQuantity, fLocation, fHangar, fEstimatedValue}, concise(in.ResponseFormat)),
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
		if strings.Contains(strings.ToLower(typeNames[j.Int(i[fTypeID])]), needle) {
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
		qty := j.Int(item[fQuantity])
		if qty == 0 {
			qty = 1
		}
		container := byID[j.Int(item["location_id"])]
		var inside any
		if container != nil {
			inside = typeNames[j.Int(container[fTypeID])]
		}
		rows = append(rows, map[string]any{
			fItem: typeNames[j.Int(item[fTypeID])], fQuantity: qty,
			fLocation: nameOr(placeNames, roots[j.Int(item["item_id"])]), fHangar: hangarLabel(j.Str(item["location_flag"]), hangars),
			fEstimatedValue: isk(unitPrice(prices, j.Int(item[fTypeID])) * float64(qty)),
			"inside":        inside, "slot": item["location_flag"],
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
	corp, err := openCorp(ctx, a, in.Character, fBlueprints, fBlueprints, "corporation blueprints")
	if err != nil {
		return nil, err
	}
	result, err := a.ESI.GetAllPages(ctx, fmt.Sprintf("/corporations/%d/blueprints", corp.CorporationID), &corp.Token.CharacterID, nil, pagesESI)
	if err != nil {
		return nil, err
	}
	bps := j.Maps(result.Data)
	if len(bps) == 0 {
		return merge(who(corp), map[string]any{fBlueprints: []any{}, fNote: "The corporation holds no blueprints."}), nil
	}
	divs := corpDivisions(ctx, a, corp)
	named, err := corpBlueprintNames(ctx, a, corp, bps)
	if err != nil {
		return nil, err
	}
	listed := corpBlueprintRows(bps, named.types, named.places, divs[fHangar])
	sort.Slice(listed.rows, func(i, k int) bool {
		if j.Str(listed.rows[i][fKind]) != j.Str(listed.rows[k][fKind]) {
			return j.Str(listed.rows[i][fKind]) == vOriginal
		}

		return j.Int(listed.rows[i][fMaterialEfficiency]) > j.Int(listed.rows[k][fMaterialEfficiency])
	})
	visible, meta := page(listed.rows, limitOr(in.Limit, limitLong), "")

	return merge(who(corp), merge(map[string]any{
		"originals": listed.originals, "copies": listed.copies, fDataAge: result.StaleNote(),
		fBlueprints: project(visible, []string{fBlueprint, fKind, fMaterialEfficiency, fTimeEfficiency, fRunsLeft, fHangar}, concise(in.ResponseFormat)),
	}, meta)), nil
}

type corpNameMaps struct {
	types, places map[int]string
}

func corpBlueprintNames(ctx context.Context, a *session.Session, corp *character.Corporation, bps []map[string]any) (corpNameMaps, error) {
	typeIDs := make([]int, 0, len(bps))
	placeIDs := make([]int, 0, len(bps))
	for _, b := range bps {
		typeIDs = append(typeIDs, j.Int(b[fTypeID]))
		placeIDs = append(placeIDs, j.Int(b["location_id"]))
	}
	typeNames, err := a.Resolver.Names(ctx, typeIDs, nil)
	if err != nil {
		return corpNameMaps{}, err
	}
	placeNames, err := a.Resolver.Names(ctx, placeIDs, &corp.Token.CharacterID)
	if err != nil {
		return corpNameMaps{}, err
	}

	return corpNameMaps{typeNames, placeNames}, nil
}

type corpBlueprintList struct {
	rows              []map[string]any
	originals, copies int
}

func corpBlueprintRows(bps []map[string]any, typeNames, placeNames map[int]string, hangars map[int]string) corpBlueprintList {
	rows := make([]map[string]any, 0, len(bps))
	orig, copies := 0, 0
	for _, b := range bps {
		kind := vOriginal
		var runs any
		if j.Float(b["runs"]) != -1 {
			kind = "copy"
			runs = b["runs"]
			copies++
		} else {
			orig++
		}
		rows = append(rows, map[string]any{
			fBlueprint: typeNames[j.Int(b[fTypeID])], fKind: kind,
			fMaterialEfficiency: b[fMaterialEfficiency], fTimeEfficiency: b[fTimeEfficiency],
			fRunsLeft: runs, fLocation: nameOr(placeNames, j.Int(b["location_id"])),
			fHangar: hangarLabel(j.Str(b["location_flag"]), hangars), fQuantity: b[fQuantity],
		})
	}

	return corpBlueprintList{rows, orig, copies}
}

func eveCorpWallet(ctx context.Context, a *session.Session, in corpWalletIn) (any, error) {
	corp, err := openCorp(ctx, a, in.Character, fWallets, fWallets, "corporation wallets")
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
			fDivision: w[fDivision], fName: walletLabel(j.Int(w[fDivision]), names),
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
	bal := corpWalletRows(wallets.Data, divs[fWallet])

	return merge(who(corp), map[string]any{
		"wallet_total": isk(bal.total), fDataAge: wallets.StaleNote(), fWallets: bal.rows,
		fNote: "Pass kind='journal' or kind='transactions' with a division (1-7) to see movements. ESI retains about 30 days.",
	}), nil
}

func corpWalletMovements(ctx context.Context, a *session.Session, corp *character.Corporation, in corpWalletIn, kind string, div int, divs map[string]map[int]string) (any, error) {
	out := merge(who(corp), map[string]any{
		fDivision: div, "division_name": walletLabel(div, divs[fWallet]),
		fPeriod: "last ~30 days (ESI retention limit)",
	})
	if kind == fJournal || kind == vBoth {
		sec, err := corpWalletJournal(ctx, a, corp, div, in)
		if err != nil {
			return nil, err
		}
		out["journal_section"] = sec
	}
	if kind == fTransactions || kind == vBoth {
		sec, err := transactionSection(ctx, a, fmt.Sprintf("/corporations/%d/wallets/%d/transactions", corp.CorporationID, div), corp.Token.CharacterID, limitOr(in.Limit, limitDefault), concise(in.ResponseFormat))
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

func corpWalletJournal(ctx context.Context, a *session.Session, corp *character.Corporation, div int, in corpWalletIn) (map[string]any, error) {
	res, err := a.ESI.GetAllPages(ctx, fmt.Sprintf("/corporations/%d/wallets/%d/journal", corp.CorporationID, div), &corp.Token.CharacterID, nil, pagesShort)
	if err != nil {
		return nil, err
	}

	return summarizeJournal(res.Data, res.StaleNote(), res.Truncated, pagesShort, in.RefType, limitOr(in.Limit, limitDefault), concise(in.ResponseFormat), fmt.Sprintf("division %d", div))
}

func eveCorpIndustryJobs(ctx context.Context, a *session.Session, in corpIndustryJobsIn) (any, error) {
	corp, err := openCorp(ctx, a, in.Character, fJobs, fJobs, "corporation industry jobs")
	if err != nil {
		return nil, err
	}
	result, err := a.ESI.GetAllPages(ctx, fmt.Sprintf("/corporations/%d/industry/jobs", corp.CorporationID), &corp.Token.CharacterID, map[string]any{"include_completed": boolDef(in.IncludeCompleted, false)}, pagesESI)
	if err != nil {
		return nil, err
	}
	out, err := industryJobsResult(ctx, a, corp.CharacterName(), corp.Token.CharacterID, result.Data, result.StaleNote(), limitOr(in.Limit, limitMedium), concise(in.ResponseFormat), true)
	if err != nil {
		return nil, err
	}

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
	out := merge(who(corp), map[string]any{fPeriod: "last ~30 days"})
	corpMiningAttachExtractions(ctx, a, corp, canExtract, out)
	corpMiningAttachLedger(ctx, a, corp, in, canLedger, out)

	return out, nil
}

func corpMiningRequireRole(a *session.Session, corp *character.Corporation, canLedger, canExtract bool) error {
	if canLedger || canExtract {
		return nil
	}

	return a.RequireCorpRole(corp, []string{roleAccountant, roleStationManager}, "corporation mining (ledger needs Accountant, extractions need Station_Manager)")
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
	ledger, err := corpMiningLedger(ctx, a, corp, limitOr(in.Limit, limitDefault), concise(in.ResponseFormat))
	if err != nil {
		out["ledger_note"] = err.Error()

		return
	}
	merge(out, ledger)
}

func eveCorpOrders(ctx context.Context, a *session.Session, in corpOrdersIn) (any, error) {
	corp, err := openCorp(ctx, a, in.Character, fOrders, fOrders, "corporation market orders")
	if err != nil {
		return nil, err
	}
	result, err := a.ESI.GetAllPages(ctx, fmt.Sprintf("/corporations/%d/orders", corp.CorporationID), &corp.Token.CharacterID, nil, pagesESI)
	if err != nil {
		return nil, err
	}
	divs := corpDivisions(ctx, a, corp)
	out, err := formatOrders(ctx, a, corp.CharacterName(), corp.Token.CharacterID, result.Data, result.StaleNote(), limitOr(in.Limit, limitLong), concise(in.ResponseFormat), divs[fWallet])
	if err != nil {
		return nil, err
	}

	return merge(who(corp), out), nil
}

func eveCorpContracts(ctx context.Context, a *session.Session, in corpContractsIn) (any, error) {
	corp, err := openCorp(ctx, a, in.Character, fContracts, "", "corporation contracts")
	if err != nil {
		return nil, err
	}
	result, err := a.ESI.GetAllPages(ctx, fmt.Sprintf("/corporations/%d/contracts", corp.CorporationID), &corp.Token.CharacterID, nil, pagesESI)
	if err != nil {
		return nil, err
	}
	out, err := formatContracts(ctx, a, corp.CharacterName(), corp.Token.CharacterID, result.Data, result.StaleNote(), boolDef(in.OutstandingOnly, true), limitOr(in.Limit, limitDefault), concise(in.ResponseFormat), true)
	if err != nil {
		return nil, err
	}

	return merge(who(corp), out), nil
}

func eveCorpKillmails(ctx context.Context, a *session.Session, in corpKillmailsIn) (any, error) {
	corp, err := openCorp(ctx, a, in.Character, fKillmails, fKillmails, "corporation killmails")
	if err != nil {
		return nil, err
	}
	out, err := formatKillmails(ctx, a, corp.CharacterName(), corp.Token.CharacterID, corp.CorporationID, fmt.Sprintf("/corporations/%d/killmails/recent", corp.CorporationID), limitOr(in.Limit, limitKillmails), concise(in.ResponseFormat))
	if err != nil {
		return nil, err
	}

	return merge(who(corp), j.Map(out)), nil
}

func eveCorpStructures(ctx context.Context, a *session.Session, in corpStructuresIn) (any, error) {
	corp, err := openCorp(ctx, a, in.Character, fStructures, fStructures, "corporation structures")
	if err != nil {
		return nil, err
	}
	result, err := a.ESI.GetAllPages(ctx, fmt.Sprintf("/corporations/%d/structures", corp.CorporationID), &corp.Token.CharacterID, nil, pagesESI)
	if err != nil {
		return nil, err
	}
	structures := j.Maps(result.Data)
	if len(structures) == 0 {
		return merge(who(corp), map[string]any{fStructures: []any{}, fNote: "This corporation owns no Upwell structures."}), nil
	}
	names, err := a.Resolver.Names(ctx, corpStructureIDs(structures), &corp.Token.CharacterID)
	if err != nil {
		return nil, err
	}
	listed := corpStructureRows(structures, names)
	sort.Slice(listed.rows, func(i, k int) bool {
		return j.Str(listed.rows[i]["fuel_expires"]) < j.Str(listed.rows[k]["fuel_expires"])
	})
	visible, meta := page(listed.rows, limitOr(in.Limit, limitDefault), "")

	return merge(who(corp), merge(map[string]any{
		"structure_count": len(listed.rows), "unfuelled": listed.unfuelled, fDataAge: result.StaleNote(),
		fStructures: project(visible, []string{fStructure, fType, fSystem, fState, "fuel_expires_in"}, concise(in.ResponseFormat)),
	}, meta)), nil
}

func corpStructureIDs(structures []map[string]any) []int {
	idSet := map[int]struct{}{}
	for _, s := range structures {
		for _, k := range []string{fTypeID, "system_id", "structure_id"} {
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
			fStructure: names[j.Int(s["structure_id"])], fType: names[j.Int(s[fTypeID])],
			fSystem: names[j.Int(s["system_id"])], fState: s[fState],
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

	return vUnknown, false
}

func structureServices(s map[string]any) any {
	listed := j.Maps(s["services"])
	services := make([]string, 0, len(listed))
	for _, svc := range listed {
		services = append(services, fmt.Sprintf("%v (%v)", svc[fName], svc[fState]))
	}
	if len(services) == 0 {
		return nil
	}

	return services
}

func eveCorpMembers(ctx context.Context, a *session.Session, in corpMembersIn) (any, error) {
	corp, err := openCorp(ctx, a, in.Character, fMembers, "", "corporation membership")
	if err != nil {
		return nil, err
	}
	result, err := a.ESI.GetAllPages(ctx, fmt.Sprintf("/corporations/%d/members", corp.CorporationID), &corp.Token.CharacterID, nil, pagesESI)
	if err != nil {
		return nil, err
	}
	memberIDs := corpMemberIDs(result.Data)
	if len(memberIDs) == 0 {
		return merge(who(corp), map[string]any{fMembers: []any{}, fNote: "ESI returned an empty roster."}), nil
	}
	names, err := a.Resolver.Names(ctx, memberIDs, nil)
	if err != nil {
		return nil, err
	}
	rows := corpMemberRows(memberIDs, names, corpMemberRoleMap(ctx, a, corp, concise(in.ResponseFormat)))
	sort.Slice(rows, func(i, k int) bool {
		return strings.ToLower(j.Str(rows[i][fName])) < strings.ToLower(j.Str(rows[k][fName]))
	})
	visible, meta := page(rows, limitOr(in.Limit, limitLong), "")

	return merge(who(corp), merge(map[string]any{
		"member_count": len(rows), fDataAge: result.StaleNote(),
		fMembers: project(visible, []string{fName}, concise(in.ResponseFormat)),
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
	if conciseMode || !corp.HasRole(roleDirector) {
		return roleMap
	}
	rolesRes, err := a.ESI.Get(ctx, fmt.Sprintf("/corporations/%d/roles", corp.CorporationID), &corp.Token.CharacterID, nil, nil)
	if err != nil {
		log.Printf("could not read corporation roles roster: %v", err)

		return roleMap
	}
	for _, row := range j.Maps(rolesRes.Data) {
		roleMap[j.Int(row[fCharacterID])] = corpRoleStrings(row[fRoles])
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
		rows = append(rows, map[string]any{fName: nameOr(names, mid), fCharacterID: mid, fRoles: roles})
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

	return map[string]any{fCharacter: corp.CharacterName(), fCorporation: corp.CorporationName, "ticker": ticker}
}

func corpCan(corp *character.Corporation, scopeKey, roleKey string) bool {
	have := slices.Contains(corp.Token.Scopes, corpScope(scopeKey))

	return have && corp.HasRole(corpRole(roleKey)...)
}

func rolesForDisplay(corp *character.Corporation) map[string]any {
	if _, ok := corp.Roles[roleDirector]; ok {
		return map[string]any{
			fRoles:      []string{roleDirector},
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
		fRoles:      esi,
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
		{"eve_corp_assets_list", fAssets, fAssets},
		{"eve_corp_assets_find", fAssets, fAssets},
		{"eve_corp_blueprints", fBlueprints, fBlueprints},
		{"eve_corp_wallet", fWallets, fWallets},
		{"eve_corp_industry_jobs", fJobs, fJobs},
		{"eve_corp_orders", fOrders, fOrders},
		{"eve_corp_contracts", fContracts, ""},
		{"eve_corp_killmails", fKillmails, fKillmails},
		{"eve_corp_structures", fStructures, fStructures},
		{"eve_corp_members", fMembers, ""},
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
	empty := map[string]map[int]string{fWallet: {}, fHangar: {}}
	if !corpCan(corp, "divisions", "divisions") {
		return empty
	}
	result, err := a.ESI.Get(ctx, fmt.Sprintf("/corporations/%d/divisions", corp.CorporationID), &corp.Token.CharacterID, nil, nil)
	if err != nil {
		log.Printf("could not read corporation divisions: %v", err)

		return empty
	}
	out := map[string]map[int]string{fWallet: {}, fHangar: {}}
	data := j.Map(result.Data)
	for _, kind := range []string{fWallet, fHangar} {
		for _, row := range j.Maps(data[kind]) {
			if j.Int(row[fDivision]) == 0 {
				continue
			}
			if n := strings.TrimSpace(j.Str(row[fName])); n != "" {
				out[kind][j.Int(row[fDivision])] = n
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
	names, err := a.Resolver.Names(ctx, setToList(idSet), &corp.Token.CharacterID)
	if err != nil {
		return nil, err
	}
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
		fStructure: names[j.Int(e["structure_id"])], "moon": names[j.Int(e["moon_id"])],
		"chunk_arrives_in": extractionDelta(arrival, now, "arrived"), "decays_in": extractionDelta(decay, now, "decayed"),
		"chunk_arrival_time": e["chunk_arrival_time"], "natural_decay_time": e["natural_decay_time"],
	}
}

func extractionDelta(t *time.Time, now time.Time, past string) string {
	if t == nil {
		return vUnknown
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
	observersRes, err := a.ESI.GetAllPages(ctx, fmt.Sprintf("/corporation/%d/mining/observers", corp.CorporationID), &corp.Token.CharacterID, nil, pagesESI)
	if err != nil {
		return nil, err
	}
	observers := j.Maps(observersRes.Data)
	if len(observers) == 0 {
		return map[string]any{fOres: []any{}, fNote: "No mining observers with recorded events (idle refineries are hidden).", fDataAge: observersRes.StaleNote()}, nil
	}
	agg := fetchCorpMiningObservers(ctx, a, corp, observers, observersRes)
	names, err := a.Resolver.Names(ctx, setToList(miningAggIDs(agg)), &corp.Token.CharacterID)
	if err != nil {
		return nil, err
	}
	prices, err := a.Resolver.ReferencePrices(ctx)
	if err != nil {
		return nil, err
	}
	rows, grand := miningOreRows(agg.totals, names, prices)
	visible, meta := page(rows, limit, "")
	out := merge(map[string]any{
		fTotalEstimatedValue: isk(grand), "observer_count": len(observers),
		"top_miners": topN(agg.byMiner, names, "miner"), "top_observers": topN(agg.byObserver, names, "observer"),
		fValuationBasis: valuationCCPAvg,
		fDataAge:        miningLedgerAge(observersRes, agg.oldest), fOres: visible,
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
	if oldest < float64(secondsPerMinute) {
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
	if len(capped) > miningObserverCap {
		capped = capped[:miningObserverCap]
	}
	ch := miningObserverPages(ctx, a, corp, capped)
	agg := miningLedgerAgg{
		totals: map[int]int{}, byMiner: map[int]int{}, byObserver: map[int]int{},
		oldest: observersRes.AgeSeconds, truncated: observersRes.Truncated || len(observers) > miningObserverCap,
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
			r, err := a.ESI.GetAllPages(ctx, fmt.Sprintf("/corporation/%d/mining/observers/%d", corp.CorporationID, j.Int(obs["observer_id"])), &corp.Token.CharacterID, nil, pagesShort)
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
		qty := j.Int(entry[fQuantity])
		agg.totals[j.Int(entry[fTypeID])] += qty
		if j.Int(entry[fCharacterID]) != 0 {
			agg.byMiner[j.Int(entry[fCharacterID])] += qty
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
	if len(list) > limitTopItems {
		list = list[:limitTopItems]
	}
	out := make([]map[string]any, 0, len(list))
	for _, x := range list {
		out = append(out, map[string]any{label: nameOr(names, x.id), fUnits: x.q})
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
