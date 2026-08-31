package eve

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/domain/j"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func searchCategories() []string {
	return []string{
		"agent", "alliance", "character", "constellation", "corporation", "faction",
		"inventory_type", "region", "solar_system", "station", "structure",
	}
}

func routePref(key string) (string, bool) {
	switch key {
	case "shorter":
		return "Shorter", true
	case "safer":
		return "Safer", true
	case "less_secure":
		return "LessSecure", true
	default:
		return "", false
	}
}

type universeSearchIn struct {
	Query      string `json:"query"                jsonschema:"At least 3 characters. Prefix match by default, so 'Trit' finds 'Tritanium'."`
	Categories string `json:"categories,omitempty" jsonschema:"Comma-separated subset of: agent, alliance, character, constellation, corporation, faction, inventory_type, region, solar_system, station, structure."`
	Strict     *bool  `json:"strict,omitempty"     jsonschema:"Exact-match instead of prefix match."`
	Character  string `json:"character,omitempty"  jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
	Limit      int    `json:"limit,omitempty"      jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
}

type universeItemIn struct {
	Item string `json:"item" jsonschema:"Exact item type name, e.g. 'Rifter'."`
}

type universeSystemIn struct {
	System string `json:"system" jsonschema:"Exact solar system name, e.g. 'Jita'."`
}

type universeRouteIn struct {
	Origin      string `json:"origin"               jsonschema:"Exact origin system name."`
	Destination string `json:"destination"          jsonschema:"Exact destination system name."`
	Preference  string `json:"preference,omitempty" jsonschema:"shorter (default), safer, or less_secure."`
	Avoid       string `json:"avoid,omitempty"      jsonschema:"Comma-separated exact system names to route around, e.g. 'Uedama,Niarja'."`
	ShowHops    *bool  `json:"show_hops,omitempty"  jsonschema:"Include the full system-by-system list."`
}

type universeHotspotsIn struct {
	Limit int `json:"limit,omitempty" jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
}

func registerUniverse(s *mcp.Server) {
	addTool(s, &mcp.Tool{
		Name:        "eve_universe_search",
		Description: "Resolve a partial or misspelled name to the exact EVE name and its id.\n\nCall this first whenever you are not certain of a name. ESI matches on prefix, not fuzzily — this tool shortens the prefix and retries.\n\nReturns: one section per requested category, each with total and results[] of {id, name}.",
	}, sessionTool(eveUniverseSearch))
	addTool(s, &mcp.Tool{
		Name:        "eve_universe_item",
		Description: "Item type reference: group, volume, mass, capacity and description.\n\npackaged_volume_m3 is what hauling maths should use unless the item is assembled. For live cost use eve_market_price.",
	}, sessionTool(eveUniverseItem))
	addTool(s, &mcp.Tool{
		Name:        "eve_universe_system",
		Description: "Security status, region, and the last hour of kills and jumps for one system.\n\nReturns: system, region, security_status, security_class, kills and jumps in the last hour.",
	}, sessionTool(eveUniverseSystem))
	addTool(s, &mcp.Tool{
		Name:        "eve_universe_route",
		Description: "Gate-to-gate route between two systems, with the danger profile of each hop.\n\nsafe means the whole route stays in high-security space. Suicide ganking still happens in high-sec — mention avoid for Uedama/Niarja when hauling valuables.",
	}, sessionTool(eveUniverseRoute))
	addTool(s, &mcp.Tool{
		Name:        "eve_universe_hotspots",
		Description: "Systems with the most ship and pod kills in the last hour, by name.\n\nHigh npc_kills with low ship kills just means busy ratting. Returns: window, systems[] sorted by player kills.",
	}, sessionTool(eveUniverseHotspots))
}

func eveUniverseSearch(ctx context.Context, a *session.Session, in universeSearchIn) (any, error) {
	if len(strings.TrimSpace(in.Query)) < 3 {
		return map[string]any{"error": "query must be at least 3 characters"}, nil
	}
	wanted := universeSearchWanted(in.Categories)
	if invalid := universeSearchInvalid(wanted); len(invalid) > 0 {
		return map[string]any{"error": fmt.Sprintf("Unknown categories %v. Valid values: %s", invalid, strings.Join(searchCategories(), ", "))}, nil
	}
	token, err := a.ResolveCharacter(ctx, in.Character)
	if err != nil {
		return nil, err
	}
	if err := a.RequireScope(token, "esi-search.search_structures.v1", "the search index"); err != nil {
		return nil, err
	}
	raw, used, err := searchWithFallback(ctx, a, token.CharacterID, wanted, in.Query, boolDef(in.Strict, false))
	if err != nil {
		return nil, err
	}

	return universeSearchAssemble(ctx, a, token.CharacterID, in, raw, used)
}

func universeSearchWanted(categories string) []string {
	wanted := []string{"inventory_type", "solar_system", "station", "region"}
	if strings.TrimSpace(categories) == "" {
		return wanted
	}
	wanted = nil
	for c := range strings.SplitSeq(categories, ",") {
		c = strings.TrimSpace(c)
		if c != "" {
			wanted = append(wanted, c)
		}
	}

	return wanted
}

func universeSearchInvalid(wanted []string) []string {
	valid := map[string]struct{}{}
	for _, c := range searchCategories() {
		valid[c] = struct{}{}
	}
	var invalid []string
	for _, c := range wanted {
		if _, ok := valid[c]; !ok {
			invalid = append(invalid, c)
		}
	}

	return invalid
}

func universeSearchAssemble(ctx context.Context, a *session.Session, characterID int, in universeSearchIn, raw map[string][]int, used string) (map[string]any, error) {
	limit := limitOr(in.Limit, 10)
	pool := min(max(4*limit, 50), 200)
	names, err := a.Resolver.Names(ctx, setToList(universeSearchIDSet(raw, pool)), &characterID)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"query": in.Query, "strict": boolDef(in.Strict, false)}
	if used != in.Query {
		out["matched_on_prefix"] = used
		out["note"] = fmt.Sprintf("Nothing matched %q exactly. ESI matches on prefix, not fuzzily, so the search was retried with the shorter prefix %q. Check that the result below is really what was meant.", in.Query, used)
	}
	anyHit := false
	for cat, ids := range raw {
		if len(ids) > 0 {
			anyHit = true
		}
		out[cat] = map[string]any{"total": len(ids), "results": universeSearchRanked(ids, names, pool, limit)}
	}
	if !anyHit {
		out["note"] = fmt.Sprintf("No matches for %q even after shortening the prefix. ESI searches by prefix, so a typo in the first few characters cannot be recovered. Try a different part of the name, or widen `categories`.", in.Query)
	}

	return out, nil
}

func universeSearchIDSet(raw map[string][]int, pool int) map[int]struct{} {
	idSet := map[int]struct{}{}
	for _, ids := range raw {
		for i, id := range ids {
			if i >= pool {
				break
			}
			idSet[id] = struct{}{}
		}
	}

	return idSet
}

func universeSearchRanked(ids []int, names map[int]string, pool, limit int) []map[string]any {
	var ranked []map[string]any
	n := min(len(ids), pool)
	for _, id := range ids[:n] {
		ranked = append(ranked, map[string]any{"id": id, "name": nameOr(names, id)})
	}
	sort.Slice(ranked, func(i, k int) bool {
		ni, nk := j.Str(ranked[i]["name"]), j.Str(ranked[k]["name"])
		if len(ni) != len(nk) {
			return len(ni) < len(nk)
		}

		return ni < nk
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}

	return ranked
}

func eveUniverseItem(ctx context.Context, a *session.Session, in universeItemIn) (any, error) {
	resolved, err := a.Resolver.ResolveNames(ctx, []string{in.Item}, nil, []string{"inventory_types"})
	if err != nil {
		return nil, err
	}
	match := resolved[strings.ToLower(strings.TrimSpace(in.Item))]
	if match.Chosen == nil {
		return map[string]any{"error": fmt.Sprintf("No item type is named exactly %q. Call eve_universe_search with this text to find the real name.", in.Item)}, nil
	}
	info, err := a.Resolver.TypeInfo(ctx, match.Chosen.ID)
	if err != nil {
		return nil, err
	}
	desc := j.Str(info["description"])
	if len(desc) > 500 {
		desc = desc[:500]
	}
	out := map[string]any{
		"item": info["name"], "type_id": match.Chosen.ID,
		"group":     a.Resolver.GroupName(ctx, j.Int(info["group_id"])),
		"volume_m3": info["volume"], "packaged_volume_m3": info["packaged_volume"],
		"mass_kg": info["mass"], "capacity_m3": info["capacity"],
		"published": info["published"], "ccp_average_price": isk(a.Resolver.ReferencePrice(ctx, match.Chosen.ID)),
		"description": desc,
	}
	if match.Ambiguous() {
		var others []string
		for _, m := range match.Alternatives {
			others = append(others, fmt.Sprintf("#%d", m.ID))
		}
		out["ambiguity_note"] = fmt.Sprintf("%d item types are named %q; showing #%d. Others: %s.", len(match.Alternatives)+1, in.Item, match.Chosen.ID, strings.Join(others, ", "))
	}

	return out, nil
}

type universeSystemESI struct {
	info, kills, jumps esi.Result
}

func eveUniverseSystem(ctx context.Context, a *session.Session, in universeSystemIn) (any, error) {
	resolved, err := a.Resolver.ResolveNames(ctx, []string{in.System}, nil, []string{"systems"})
	if err != nil {
		return nil, err
	}
	match := resolved[strings.ToLower(strings.TrimSpace(in.System))]
	if match.Chosen == nil {
		return map[string]any{"error": fmt.Sprintf("No solar system is named exactly %q. Call eve_universe_search with categories='solar_system'.", in.System)}, nil
	}
	sid, name := match.Chosen.ID, match.Chosen.Name
	got, err := universeSystemLookups(ctx, a, sid)
	if err != nil {
		return nil, err
	}
	info := j.Map(got.info.Data)
	kills := universeSystemStat(got.kills.Data, sid)
	jumps := universeSystemStat(got.jumps.Data, sid)
	sec := j.Float(info["security_status"])

	return map[string]any{
		"system": name, "system_id": sid, "region": universeRegionName(ctx, a, info),
		"security_status": mathRound(sec, 2), "security_class": secBand(sec),
		"stations": len(j.Slice(info["stations"])), "stargates": len(j.Slice(info["stargates"])),
		"ship_kills_last_hour": j.Int(kills["ship_kills"]), "pod_kills_last_hour": j.Int(kills["pod_kills"]),
		"npc_kills_last_hour": j.Int(kills["npc_kills"]), "jumps_last_hour": j.Int(jumps["ship_jumps"]),
		"data_age": got.kills.StaleNote(),
	}, nil
}

func universeSystemLookups(ctx context.Context, a *session.Session, sid int) (universeSystemESI, error) {
	infoRes, err := a.ESI.Get(ctx, fmt.Sprintf("/universe/systems/%d", sid), nil, nil, nil)
	if err != nil {
		return universeSystemESI{}, err
	}
	killsRes, err := a.ESI.Get(ctx, "/universe/system_kills", nil, nil, nil)
	if err != nil {
		return universeSystemESI{}, err
	}
	jumpsRes, err := a.ESI.Get(ctx, "/universe/system_jumps", nil, nil, nil)
	if err != nil {
		return universeSystemESI{}, err
	}

	return universeSystemESI{infoRes, killsRes, jumpsRes}, nil
}

func universeSystemStat(data any, sid int) map[string]any {
	for _, row := range j.Maps(data) {
		if j.Int(row["system_id"]) == sid {
			return row
		}
	}

	return nil
}

func universeRegionName(ctx context.Context, a *session.Session, info map[string]any) any {
	if j.Int(info["constellation_id"]) == 0 {
		return nil
	}
	c, err := a.ESI.Get(ctx, fmt.Sprintf("/universe/constellations/%d", j.Int(info["constellation_id"])), nil, nil, nil)
	if err != nil {
		return nil
	}
	rid := j.Int(j.Map(c.Data)["region_id"])
	if rid == 0 {
		return nil
	}
	regionName, err := a.Resolver.Name(ctx, rid, nil)
	if err != nil {
		return nil
	}

	return regionName
}

type universeRouteAvoid struct {
	body    map[string]any
	avoided []string
}

type universeRouteWalk struct {
	steps           []map[string]any
	lowsec, nullsec int
}

func eveUniverseRoute(ctx context.Context, a *session.Session, in universeRouteIn) (any, error) {
	prefKey := strings.ToLower(strings.TrimSpace(in.Preference))
	if prefKey == "" {
		prefKey = "shorter"
	}
	pref, ok := routePref(prefKey)
	if !ok {
		return map[string]any{"error": fmt.Sprintf("preference must be one of %v", []string{"shorter", "safer", "less_secure"})}, nil
	}
	found, err := universeResolveSystems(ctx, a, in)
	if err != nil {
		return nil, err
	}
	oid, did := found[strings.ToLower(strings.TrimSpace(in.Origin))], found[strings.ToLower(strings.TrimSpace(in.Destination))]
	if oid == 0 || did == 0 {
		return universeRouteMissing(in, oid, did), nil
	}
	req := universeRouteBody(in, found, pref)
	route, err := a.ESI.Post(ctx, fmt.Sprintf("/route/%d/%d", oid, did), nil, nil, req.body)
	if err != nil {
		return nil, err
	}
	hops := universeRouteHops(route)
	if len(hops) == 0 {
		return map[string]any{"error": "No gate route exists between those systems. They may be in wormhole space, or every path is excluded by `avoid`."}, nil
	}

	return universeRouteSummary(in, pref, req.avoided, universeWalkHops(ctx, a, hops)), nil
}

func universeResolveSystems(ctx context.Context, a *session.Session, in universeRouteIn) (map[string]int, error) {
	wanted := []string{in.Origin, in.Destination}
	for part := range strings.SplitSeq(in.Avoid, ",") {
		if s := strings.TrimSpace(part); s != "" {
			wanted = append(wanted, s)
		}
	}
	lookup, err := a.Resolver.IDsFromNames(ctx, wanted)
	if err != nil {
		return nil, err
	}
	found := map[string]int{}
	for _, s := range j.Maps(lookup["systems"]) {
		found[strings.ToLower(j.Str(s["name"]))] = j.Int(s["id"])
	}

	return found, nil
}

func universeRouteMissing(in universeRouteIn, oid, did int) map[string]any {
	var missing []string
	if oid == 0 {
		missing = append(missing, in.Origin)
	}
	if did == 0 {
		missing = append(missing, in.Destination)
	}

	return map[string]any{"error": fmt.Sprintf("Unknown system name(s): %v. Names must be exact — call eve_universe_search with categories='solar_system'.", missing)}
}

func universeRouteBody(in universeRouteIn, found map[string]int, pref string) universeRouteAvoid {
	body := map[string]any{"preference": pref}
	var avoidIDs []int
	var avoided []string
	for name := range strings.SplitSeq(in.Avoid, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if id, ok := found[strings.ToLower(name)]; ok {
			avoidIDs = append(avoidIDs, id)
			avoided = append(avoided, name)
		}
	}
	if len(avoidIDs) > 0 {
		body["avoid_systems"] = avoidIDs
	}

	return universeRouteAvoid{body, avoided}
}

func universeRouteHops(route any) []int {
	var hops []int
	switch t := route.(type) {
	case map[string]any:
		for _, v := range j.Slice(t["route"]) {
			hops = append(hops, j.Int(v))
		}
	case []any:
		for _, v := range t {
			hops = append(hops, j.Int(v))
		}
	}

	return hops
}

func universeHopData(ctx context.Context, a *session.Session, hops []int) map[int]map[string]any {
	type box struct {
		sid int
		m   map[string]any
	}
	ch := make(chan box, len(hops))
	for _, sid := range hops {
		go func(sid int) {
			r, err := a.ESI.Get(ctx, fmt.Sprintf("/universe/systems/%d", sid), nil, nil, nil)
			if err != nil {
				ch <- box{sid, map[string]any{"name": strconv.Itoa(sid)}}

				return
			}
			ch <- box{sid, j.Map(r.Data)}
		}(sid)
	}
	byID := map[int]map[string]any{}
	for range hops {
		b := <-ch
		byID[b.sid] = b.m
	}

	return byID
}

func universeWalkHops(ctx context.Context, a *session.Session, hops []int) universeRouteWalk {
	byID := universeHopData(ctx, a, hops)
	steps := make([]map[string]any, 0, len(hops))
	lowsec, nullsec := 0, 0
	for _, sid := range hops {
		data := byID[sid]
		sec := j.Float(data["security_status"])
		band := secBand(sec)
		switch band {
		case "lowsec":
			lowsec++
		case "nullsec":
			nullsec++
		}
		n := j.Str(data["name"])
		if n == "" {
			n = strconv.Itoa(sid)
		}
		steps = append(steps, map[string]any{"system": n, "security": mathRound(sec, 1), "class": band})
	}

	return universeRouteWalk{steps, lowsec, nullsec}
}

func universeRouteSummary(in universeRouteIn, pref string, avoided []string, walk universeRouteWalk) map[string]any {
	out := compact(map[string]any{
		"origin": in.Origin, "destination": in.Destination, "preference": pref,
		"jumps": len(walk.steps) - 1, "lowsec_systems": walk.lowsec, "nullsec_systems": walk.nullsec,
		"safe": walk.lowsec == 0 && walk.nullsec == 0,
	})
	if len(avoided) > 0 {
		out["avoided"] = avoided
	}
	if boolDef(in.ShowHops, false) {
		out["route"] = walk.steps
	} else if walk.lowsec+walk.nullsec > 0 {
		out["dangerous_hops"] = universeDangerousHops(walk.steps)
	}

	return out
}

func universeDangerousHops(steps []map[string]any) []string {
	var dang []string
	for _, s := range steps {
		if j.Str(s["class"]) != "highsec" {
			dang = append(dang, j.Str(s["system"]))
		}
	}
	if len(dang) > 20 {
		dang = dang[:20]
	}

	return dang
}

func eveUniverseHotspots(ctx context.Context, a *session.Session, in universeHotspotsIn) (any, error) {
	result, err := a.ESI.Get(ctx, "/universe/system_kills", nil, nil, nil)
	if err != nil {
		return nil, err
	}
	rows := j.Maps(result.Data)
	sort.Slice(rows, func(i, k int) bool {
		return j.Int(rows[i]["ship_kills"])+j.Int(rows[i]["pod_kills"]) > j.Int(rows[k]["ship_kills"])+j.Int(rows[k]["pod_kills"])
	})
	limit := limitOr(in.Limit, 10)
	if len(rows) > limit {
		rows = rows[:limit]
	}
	idSet := map[int]struct{}{}
	for _, r := range rows {
		idSet[j.Int(r["system_id"])] = struct{}{}
	}
	names, err := a.Resolver.Names(ctx, setToList(idSet), nil)
	if err != nil {
		return nil, err
	}
	var outRows []map[string]any
	for _, r := range rows {
		outRows = append(outRows, map[string]any{
			"system":     nameOr(names, j.Int(r["system_id"])),
			"ship_kills": r["ship_kills"], "pod_kills": r["pod_kills"], "npc_kills": r["npc_kills"],
		})
	}
	visible, meta := page(outRows, limit, "")

	return merge(map[string]any{"window": "last hour", "data_age": result.StaleNote(), "systems": visible}, meta), nil
}

func searchWithFallback(ctx context.Context, a *session.Session, characterID int, categories []string, query string, strict bool) (map[string][]int, string, error) {
	attempt := strings.TrimSpace(query)
	for {
		result, err := a.ESI.Get(ctx, fmt.Sprintf("/characters/%d/search", characterID), &characterID, map[string]any{
			"categories": categories, "search": attempt, "strict": strict,
		}, nil)
		if err != nil {
			return nil, attempt, err
		}
		raw := map[string][]int{}
		hit := false
		for k, v := range j.Map(result.Data) {
			var ids []int
			for _, x := range j.Slice(v) {
				ids = append(ids, j.Int(x))
			}
			raw[k] = ids
			if len(ids) > 0 {
				hit = true
			}
		}
		if hit || strict || len(attempt) <= 3 {
			return raw, attempt, nil
		}
		attempt = attempt[:len(attempt)-1]
	}
}

func secBand(security float64) string {
	if security >= 0.45 {
		return "highsec"
	}
	if security > 0.0 {
		return "lowsec"
	}

	return "nullsec"
}
