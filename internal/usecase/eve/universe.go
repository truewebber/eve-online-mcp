package eve

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"eve-mcp/internal/adapter/esi"
	"eve-mcp/internal/domain/j"
	"eve-mcp/internal/usecase/session"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var searchCategories = []string{
	"agent", "alliance", "character", "constellation", "corporation", "faction",
	"inventory_type", "region", "solar_system", "station", "structure",
}

var routePrefs = map[string]string{"shorter": "Shorter", "safer": "Safer", "less_secure": "LessSecure"}

func registerUniverse(s *mcp.Server, a *session.Session) {
	type searchIn struct {
		Query      string `json:"query" jsonschema:"At least 3 characters. Prefix match by default, so 'Trit' finds 'Tritanium'."`
		Categories string `json:"categories,omitempty" jsonschema:"Comma-separated subset of: agent, alliance, character, constellation, corporation, faction, inventory_type, region, solar_system, station, structure."`
		Strict     *bool  `json:"strict,omitempty" jsonschema:"Exact-match instead of prefix match."`
		Character  string `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
		Limit      int    `json:"limit,omitempty" jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	}
	addTool(s, &mcp.Tool{
		Name:        "eve_universe_search",
		Description: "Resolve a partial or misspelled name to the exact EVE name and its id.\n\nCall this first whenever you are not certain of a name. ESI matches on prefix, not fuzzily — this tool shortens the prefix and retries.\n\nReturns: one section per requested category, each with total and results[] of {id, name}.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in searchIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *session.Session) (any, error) {
			if len(strings.TrimSpace(in.Query)) < 3 {
				return map[string]any{"error": "query must be at least 3 characters"}, nil
			}
			wanted := []string{"inventory_type", "solar_system", "station", "region"}
			if strings.TrimSpace(in.Categories) != "" {
				wanted = nil
				for _, c := range strings.Split(in.Categories, ",") {
					c = strings.TrimSpace(c)
					if c != "" {
						wanted = append(wanted, c)
					}
				}
			}
			valid := map[string]struct{}{}
			for _, c := range searchCategories {
				valid[c] = struct{}{}
			}
			var invalid []string
			for _, c := range wanted {
				if _, ok := valid[c]; !ok {
					invalid = append(invalid, c)
				}
			}
			if len(invalid) > 0 {
				return map[string]any{"error": fmt.Sprintf("Unknown categories %v. Valid values: %s", invalid, strings.Join(searchCategories, ", "))}, nil
			}
			token, err := a.ResolveCharacter(in.Character)
			if err != nil {
				return nil, err
			}
			if err := a.RequireScope(token, "esi-search.search_structures.v1", "the search index"); err != nil {
				return nil, err
			}
			raw, used, err := searchWithFallback(a, token.CharacterID, wanted, in.Query, boolDef(in.Strict, false))
			if err != nil {
				return nil, err
			}
			limit := limitOr(in.Limit, 10)
			pool := 4 * limit
			if pool < 50 {
				pool = 50
			}
			if pool > 200 {
				pool = 200
			}
			idSet := map[int]struct{}{}
			for _, ids := range raw {
				for i, id := range ids {
					if i >= pool {
						break
					}
					idSet[id] = struct{}{}
				}
			}
			names, _ := a.Resolver.Names(setToList(idSet), &token.CharacterID)
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
				var ranked []map[string]any
				n := len(ids)
				if n > pool {
					n = pool
				}
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
				out[cat] = map[string]any{"total": len(ids), "results": ranked}
			}
			if !anyHit {
				out["note"] = fmt.Sprintf("No matches for %q even after shortening the prefix. ESI searches by prefix, so a typo in the first few characters cannot be recovered. Try a different part of the name, or widen `categories`.", in.Query)
			}
			return out, nil
		})
	})

	type itemIn struct {
		Item string `json:"item" jsonschema:"Exact item type name, e.g. 'Rifter'."`
	}
	addTool(s, &mcp.Tool{
		Name:        "eve_universe_item",
		Description: "Item type reference: group, volume, mass, capacity and description.\n\npackaged_volume_m3 is what hauling maths should use unless the item is assembled. For live cost use eve_market_price.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in itemIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *session.Session) (any, error) {
			resolved, err := a.Resolver.ResolveNames([]string{in.Item}, nil, []string{"inventory_types"})
			if err != nil {
				return nil, err
			}
			match := resolved[strings.ToLower(strings.TrimSpace(in.Item))]
			if match.Chosen == nil {
				return map[string]any{"error": fmt.Sprintf("No item type is named exactly %q. Call eve_universe_search with this text to find the real name.", in.Item)}, nil
			}
			info, err := a.Resolver.TypeInfo(match.Chosen.ID)
			if err != nil {
				return nil, err
			}
			desc := j.Str(info["description"])
			if len(desc) > 500 {
				desc = desc[:500]
			}
			out := map[string]any{
				"item": info["name"], "type_id": match.Chosen.ID,
				"group":     a.Resolver.GroupName(j.Int(info["group_id"])),
				"volume_m3": info["volume"], "packaged_volume_m3": info["packaged_volume"],
				"mass_kg": info["mass"], "capacity_m3": info["capacity"],
				"published": info["published"], "ccp_average_price": isk(a.Resolver.ReferencePrice(match.Chosen.ID)),
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
		})
	})

	type sysIn struct {
		System string `json:"system" jsonschema:"Exact solar system name, e.g. 'Jita'."`
	}
	addTool(s, &mcp.Tool{
		Name:        "eve_universe_system",
		Description: "Security status, region, and the last hour of kills and jumps for one system.\n\nReturns: system, region, security_status, security_class, kills and jumps in the last hour.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in sysIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *session.Session) (any, error) {
			resolved, err := a.Resolver.ResolveNames([]string{in.System}, nil, []string{"systems"})
			if err != nil {
				return nil, err
			}
			match := resolved[strings.ToLower(strings.TrimSpace(in.System))]
			if match.Chosen == nil {
				return map[string]any{"error": fmt.Sprintf("No solar system is named exactly %q. Call eve_universe_search with categories='solar_system'.", in.System)}, nil
			}
			sid, name := match.Chosen.ID, match.Chosen.Name
			type box struct {
				r   esi.Result
				err error
			}
			ch := make(chan box, 3)
			go func() {
				r, err := a.ESI.Get(fmt.Sprintf("/universe/systems/%d", sid), nil, nil, nil)
				ch <- box{r, err}
			}()
			go func() { r, err := a.ESI.Get("/universe/system_kills", nil, nil, nil); ch <- box{r, err} }()
			go func() { r, err := a.ESI.Get("/universe/system_jumps", nil, nil, nil); ch <- box{r, err} }()
			// Identify by fetching again in order — race-free:
			infoRes, err := a.ESI.Get(fmt.Sprintf("/universe/systems/%d", sid), nil, nil, nil)
			if err != nil {
				return nil, err
			}
			killsRes, err := a.ESI.Get("/universe/system_kills", nil, nil, nil)
			if err != nil {
				return nil, err
			}
			jumpsRes, err := a.ESI.Get("/universe/system_jumps", nil, nil, nil)
			if err != nil {
				return nil, err
			}
			_, _, _ = <-ch, <-ch, <-ch
			info := j.Map(infoRes.Data)
			var kills, jumps map[string]any
			for _, row := range j.Maps(killsRes.Data) {
				if j.Int(row["system_id"]) == sid {
					kills = row
					break
				}
			}
			for _, row := range j.Maps(jumpsRes.Data) {
				if j.Int(row["system_id"]) == sid {
					jumps = row
					break
				}
			}
			var regionName any
			if j.Int(info["constellation_id"]) != 0 {
				c, err := a.ESI.Get(fmt.Sprintf("/universe/constellations/%d", j.Int(info["constellation_id"])), nil, nil, nil)
				if err == nil {
					if rid := j.Int(j.Map(c.Data)["region_id"]); rid != 0 {
						regionName, _ = a.Resolver.Name(rid, nil)
					}
				}
			}
			sec := j.Float(info["security_status"])
			return map[string]any{
				"system": name, "system_id": sid, "region": regionName,
				"security_status": mathRound(sec, 2), "security_class": secBand(sec),
				"stations": len(j.Slice(info["stations"])), "stargates": len(j.Slice(info["stargates"])),
				"ship_kills_last_hour": j.Int(kills["ship_kills"]), "pod_kills_last_hour": j.Int(kills["pod_kills"]),
				"npc_kills_last_hour": j.Int(kills["npc_kills"]), "jumps_last_hour": j.Int(jumps["ship_jumps"]),
				"data_age": killsRes.StaleNote(),
			}, nil
		})
	})

	type routeIn struct {
		Origin      string `json:"origin" jsonschema:"Exact origin system name."`
		Destination string `json:"destination" jsonschema:"Exact destination system name."`
		Preference  string `json:"preference,omitempty" jsonschema:"shorter (default), safer, or less_secure."`
		Avoid       string `json:"avoid,omitempty" jsonschema:"Comma-separated exact system names to route around, e.g. 'Uedama,Niarja'."`
		ShowHops    *bool  `json:"show_hops,omitempty" jsonschema:"Include the full system-by-system list."`
	}
	addTool(s, &mcp.Tool{
		Name:        "eve_universe_route",
		Description: "Gate-to-gate route between two systems, with the danger profile of each hop.\n\nsafe means the whole route stays in high-security space. Suicide ganking still happens in high-sec — mention avoid for Uedama/Niarja when hauling valuables.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in routeIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *session.Session) (any, error) {
			prefKey := strings.ToLower(strings.TrimSpace(in.Preference))
			if prefKey == "" {
				prefKey = "shorter"
			}
			pref, ok := routePrefs[prefKey]
			if !ok {
				return map[string]any{"error": fmt.Sprintf("preference must be one of %v", []string{"shorter", "safer", "less_secure"})}, nil
			}
			wanted := []string{in.Origin, in.Destination}
			for _, a := range strings.Split(in.Avoid, ",") {
				if s := strings.TrimSpace(a); s != "" {
					wanted = append(wanted, s)
				}
			}
			lookup, err := a.Resolver.IDsFromNames(wanted)
			if err != nil {
				return nil, err
			}
			found := map[string]int{}
			for _, s := range j.Maps(lookup["systems"]) {
				found[strings.ToLower(j.Str(s["name"]))] = j.Int(s["id"])
			}
			oid, did := found[strings.ToLower(strings.TrimSpace(in.Origin))], found[strings.ToLower(strings.TrimSpace(in.Destination))]
			if oid == 0 || did == 0 {
				var missing []string
				if oid == 0 {
					missing = append(missing, in.Origin)
				}
				if did == 0 {
					missing = append(missing, in.Destination)
				}
				return map[string]any{"error": fmt.Sprintf("Unknown system name(s): %v. Names must be exact — call eve_universe_search with categories='solar_system'.", missing)}, nil
			}
			body := map[string]any{"preference": pref}
			var avoidIDs []int
			var avoided []string
			for _, name := range strings.Split(in.Avoid, ",") {
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
			route, err := a.ESI.Post(fmt.Sprintf("/route/%d/%d", oid, did), nil, nil, body)
			if err != nil {
				return nil, err
			}
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
			if len(hops) == 0 {
				return map[string]any{"error": "No gate route exists between those systems. They may be in wormhole space, or every path is excluded by `avoid`."}, nil
			}
			type box struct {
				sid int
				m   map[string]any
			}
			ch := make(chan box, len(hops))
			for _, sid := range hops {
				go func(sid int) {
					r, err := a.ESI.Get(fmt.Sprintf("/universe/systems/%d", sid), nil, nil, nil)
					if err != nil {
						ch <- box{sid, map[string]any{"name": fmt.Sprint(sid)}}
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
			var steps []map[string]any
			lowsec, nullsec := 0, 0
			for _, sid := range hops {
				data := byID[sid]
				sec := j.Float(data["security_status"])
				band := secBand(sec)
				if band == "lowsec" {
					lowsec++
				} else if band == "nullsec" {
					nullsec++
				}
				n := j.Str(data["name"])
				if n == "" {
					n = fmt.Sprint(sid)
				}
				steps = append(steps, map[string]any{"system": n, "security": mathRound(sec, 1), "class": band})
			}
			out := compact(map[string]any{
				"origin": in.Origin, "destination": in.Destination, "preference": pref,
				"jumps": len(hops) - 1, "lowsec_systems": lowsec, "nullsec_systems": nullsec,
				"safe": lowsec == 0 && nullsec == 0,
			})
			if len(avoided) > 0 {
				out["avoided"] = avoided
			}
			if boolDef(in.ShowHops, false) {
				out["route"] = steps
			} else if lowsec+nullsec > 0 {
				var dang []string
				for _, s := range steps {
					if j.Str(s["class"]) != "highsec" {
						dang = append(dang, j.Str(s["system"]))
					}
				}
				if len(dang) > 20 {
					dang = dang[:20]
				}
				out["dangerous_hops"] = dang
			}
			return out, nil
		})
	})

	type hotIn struct {
		Limit int `json:"limit,omitempty" jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	}
	addTool(s, &mcp.Tool{
		Name:        "eve_universe_hotspots",
		Description: "Systems with the most ship and pod kills in the last hour, by name.\n\nHigh npc_kills with low ship kills just means busy ratting. Returns: window, systems[] sorted by player kills.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in hotIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *session.Session) (any, error) {
			result, err := a.ESI.Get("/universe/system_kills", nil, nil, nil)
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
			names, _ := a.Resolver.Names(setToList(idSet), nil)
			var outRows []map[string]any
			for _, r := range rows {
				outRows = append(outRows, map[string]any{
					"system":     nameOr(names, j.Int(r["system_id"])),
					"ship_kills": r["ship_kills"], "pod_kills": r["pod_kills"], "npc_kills": r["npc_kills"],
				})
			}
			visible, meta := page(outRows, limit, "")
			return merge(map[string]any{"window": "last hour", "data_age": result.StaleNote(), "systems": visible}, meta), nil
		})
	})
}

func searchWithFallback(a *session.Session, characterID int, categories []string, query string, strict bool) (map[string][]int, string, error) {
	attempt := strings.TrimSpace(query)
	for {
		result, err := a.ESI.Get(fmt.Sprintf("/characters/%d/search", characterID), &characterID, map[string]any{
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
