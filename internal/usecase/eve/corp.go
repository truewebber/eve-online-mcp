package eve

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/truewebber/eve-online-mcp/internal/domain/character"
	"github.com/truewebber/eve-online-mcp/internal/domain/write"
	"github.com/truewebber/eve-online-mcp/internal/j"
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
	_, ok := map[string]struct{}{
		roleDirector: {}, roleAccountant: {}, "Junior_Accountant": {},
		"Factory_Manager": {}, roleStationManager: {}, "Trader": {},
	}[name]

	return ok
}

type corpAssetsListIn struct {
	Location       string  `json:"location,omitempty"        jsonschema:"Case-insensitive substring of a station or structure name."`
	MinValue       float64 `json:"min_value,omitempty"       jsonschema:"Hide locations holding less than this many ISK."`
	Limit          int     `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	Offset         int     `json:"offset,omitempty"          jsonschema:"Skip this many rows of the result before returning any. The result carries the total, so this is how you continue a long list."`
	Items          int     `json:"items,omitempty"           jsonschema:"Maximum items per location in detailed mode."`
	ResponseFormat string  `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

type corpAssetsFindIn struct {
	Name           string `json:"name"                      jsonschema:"Case-insensitive substring of the item type name."`
	Limit          int    `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	Offset         int    `json:"offset,omitempty"          jsonschema:"Skip this many rows of the result before returning any. The result carries the total, so this is how you continue a long list."`
	ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

type corpBlueprintsIn struct {
	Page           int    `json:"page,omitempty"            jsonschema:"Which page of results to fetch, starting at 1. The result says which page it is and how many exist. Only reach for page 2 if the user asked for more than page 1 showed."`
	Limit          int    `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

type corpWalletIn struct {
	Kind           string `json:"kind,omitempty"            jsonschema:"balances (default), journal, transactions, or both."`
	Division       int    `json:"division,omitempty"        jsonschema:"Corporation wallet division, 1 through 7. Division 1 is the master wallet. Named divisions (if this character is a Director) come back from eve_corp_overview."`
	RefType        string `json:"ref_type,omitempty"        jsonschema:"Journal only: keep just one reason code."`
	Limit          int    `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	Offset         int    `json:"offset,omitempty"          jsonschema:"Skip this many rows of the result before returning any. The result carries the total, so this is how you continue a long list."`
	ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

type corpIndustryJobsIn struct {
	IncludeCompleted *bool  `json:"include_completed,omitempty" jsonschema:"Also return jobs that already delivered."`
	Page             int    `json:"page,omitempty"              jsonschema:"Which page of results to fetch, starting at 1. The result says which page it is and how many exist. Only reach for page 2 if the user asked for more than page 1 showed."`
	Limit            int    `json:"limit,omitempty"             jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	ResponseFormat   string `json:"response_format,omitempty"   jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

type corpMiningIn struct {
	Limit          int    `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	Offset         int    `json:"offset,omitempty"          jsonschema:"Skip this many rows of the result before returning any. The result carries the total, so this is how you continue a long list."`
	ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

type corpOrdersIn struct {
	Page           int    `json:"page,omitempty"            jsonschema:"Which page of results to fetch, starting at 1. The result says which page it is and how many exist. Only reach for page 2 if the user asked for more than page 1 showed."`
	Limit          int    `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

type corpContractsIn struct {
	OutstandingOnly *bool  `json:"outstanding_only,omitempty" jsonschema:"Only contracts still awaiting action. Default true."`
	Page            int    `json:"page,omitempty"             jsonschema:"Which page of results to fetch, starting at 1. The result says which page it is and how many exist. Only reach for page 2 if the user asked for more than page 1 showed."`
	Limit           int    `json:"limit,omitempty"            jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	ResponseFormat  string `json:"response_format,omitempty"  jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

type corpKillmailsIn struct {
	Page           int    `json:"page,omitempty"            jsonschema:"Which page of results to fetch, starting at 1. The result says which page it is and how many exist. Only reach for page 2 if the user asked for more than page 1 showed."`
	Limit          int    `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

type corpStructuresIn struct {
	Page           int    `json:"page,omitempty"            jsonschema:"Which page of results to fetch, starting at 1. The result says which page it is and how many exist. Only reach for page 2 if the user asked for more than page 1 showed."`
	Limit          int    `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

type corpMembersIn struct {
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

func eveCorpOverview(ctx context.Context, a *session.Session, _ empty) (any, error) {
	corp, err := a.ResolveCorporation(ctx)
	if err != nil {
		return nil, wrap("eveCorpOverview", err)
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
		return nil, wrap("corpOverviewIdentity", err)
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
	wallets, err := a.ESI.Get(ctx, esiPath("corporations", esiID(corp.CorporationID), "wallets"), &corp.Token.CharacterID, nil, nil)
	if err != nil {
		out["wallets_note"] = sectionNote(a, "corp wallets", err)

		return
	}
	bal := corpWalletRows(wallets.Data, divs[fWallet])
	out[fWallets] = bal.rows
	out["wallet_total"] = isk(bal.total)
	out["wallet_age"] = wallets.StaleNote()
	out[fDataAge] = wallets.StaleNote()
}

func corpOverviewNextStep(a *session.Session, corp *character.Corporation, out map[string]any) {
	var missing []string
	for _, sc := range write.CorpReadScopes() {
		if !a.HasGranted(corp.Token.Scopes, sc) {
			missing = append(missing, sc)
		}
	}
	if len(missing) > 0 {
		out["next_step"] = fmt.Sprintf("%s's token is missing %d corporation scopes. Add those permissions on the EVE developer application and re-authenticate the MCP server.", corp.CharacterName(), len(missing))
	} else if len(j.Slice(out["available_tools"])) <= 1 {
		out["next_step"] = "This character has no corp roles that ESI honours. Someone with Director / Accountant / Factory_Manager / Station_Manager granted everywhere has to authorize instead."
	}
}

func openCorp(ctx context.Context, a *session.Session, scopeKey, roleKey, what string) (*character.Corporation, error) {
	corp, err := a.ResolveCorporation(ctx)
	if err != nil {
		return nil, wrap("openCorp", err)
	}
	if err := a.RequirePlayerCorp(corp); err != nil {
		return nil, wrap("openCorp", err)
	}
	if err := a.RequireGranted(corp.CharacterName(), corp.Token.Scopes, corpScope(scopeKey), what); err != nil {
		return nil, wrap("openCorp", err)
	}
	if roleKey != "" {
		err := a.RequireCorpRole(corp, corpRole(roleKey), what)
		if err != nil {
			return nil, wrap("openCorp", err)
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
	result, err := a.ESI.Get(ctx, esiPath("corporations", esiID(corp.CorporationID), "divisions"), &corp.Token.CharacterID, nil, nil)
	if err != nil {
		a.Logger.Error("eve: corporation divisions", "err", err)

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
