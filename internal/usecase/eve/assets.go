package eve

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/truewebber/eve-online-mcp/internal/domain/j"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerAssets(s *mcp.Server) {
	type listIn struct {
		Character      string  `json:"character,omitempty"       jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
		Location       string  `json:"location,omitempty"        jsonschema:"Case-insensitive substring of a station or structure name, e.g. 'Jita' or 'Amarr VIII'. Empty means every location."`
		MinValue       float64 `json:"min_value,omitempty"       jsonschema:"Hide locations holding less than this many ISK.,minimum=0"`
		Limit          int     `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
		Items          int     `json:"items,omitempty"           jsonschema:"Maximum items to list inside each location in detailed mode.,minimum=1,maximum=200"`
		ResponseFormat string  `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
	}
	addTool(s, &mcp.Tool{
		Name:        "eve_assets_list",
		Description: "Assets grouped by the station or structure they sit in, with an ISK estimate.\n\nItems nested inside containers and ship holds are rolled up into the station that ultimately holds them. Valuation uses CCP's global average price per type, not a hub quote. ESI caches assets for a full hour.\n\nReturns: total_estimated_value, total_locations, locations[] sorted by value.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *session.Session) (any, error) {
			token, err := a.ResolveCharacter(in.Character)
			if err != nil {
				return nil, err
			}
			if err := a.RequireScope(token, "esi-assets.read_assets.v1", "assets"); err != nil {
				return nil, err
			}
			cid := token.CharacterID
			result, err := a.ESI.GetAllPages(fmt.Sprintf("/characters/%d/assets", cid), &cid, nil, 40)
			if err != nil {
				return nil, err
			}
			assets := j.Maps(result.Data)
			if len(assets) == 0 {
				return map[string]any{"character": token.CharacterName, "locations": []any{}, "note": "This character holds no personal assets (corp hangars are separate)."}, nil
			}
			roots := rootLocations(assets)
			prices, err := a.Resolver.ReferencePrices()
			if err != nil {
				return nil, err
			}
			var typeIDs []int
			for _, i := range assets {
				typeIDs = append(typeIDs, j.Int(i["type_id"]))
			}
			typeNames, _ := a.Resolver.Names(typeIDs, nil)
			placeNames, _ := a.Resolver.Names(valuesOf(roots), &cid)
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
				place := placeNames[placeID]
				if place == "" {
					place = fmt.Sprintf("Unknown #%d", placeID)
				}
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
				sort.Slice(top, func(i, k int) bool {
					return lineValue(prices, top[i].t, top[i].q) > lineValue(prices, top[k].t, top[k].q)
				})
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

			return merge(map[string]any{
				"character": token.CharacterName, "total_estimated_value": isk(total),
				"total_locations": len(buckets), "matching_locations": len(rows),
				"valuation_basis": "CCP global average price per type, not a hub quote",
				"data_age":        result.StaleNote(),
				"locations":       project(visible, []string{"location", "value", "distinct_types", "units"}, concise(in.ResponseFormat)),
			}, meta), nil
		})
	})

	type findIn struct {
		Name           string `json:"name"                      jsonschema:"Case-insensitive substring of the item type name, e.g. 'Drake' or 'Tritanium'."`
		Character      string `json:"character,omitempty"       jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
		Limit          int    `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
		ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
	}
	addTool(s, &mcp.Tool{
		Name:        "eve_assets_find",
		Description: "Locate a specific item across every hangar, container and ship hold.\n\nAnswers \"where did I leave my Orca\" or \"do I still have any Tritanium\". Searches personal assets only. Corporation hangars are eve_corp_assets_find.\n\nReturns: total_units, total_stacks, matches[].",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in findIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *session.Session) (any, error) {
			if strings.TrimSpace(in.Name) == "" {
				return map[string]any{"error": "name is required"}, nil
			}
			token, err := a.ResolveCharacter(in.Character)
			if err != nil {
				return nil, err
			}
			if err := a.RequireScope(token, "esi-assets.read_assets.v1", "assets"); err != nil {
				return nil, err
			}
			cid := token.CharacterID
			result, err := a.ESI.GetAllPages(fmt.Sprintf("/characters/%d/assets", cid), &cid, nil, 40)
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
				return map[string]any{
					"character": token.CharacterName, "query": in.Name, "matches": []any{},
					"note": "Nothing matching in personal assets. Check the spelling with eve_universe_search, or the item may be in a corp hangar (eve_corp_assets_find).",
				}, nil
			}
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
			placeNames, _ := a.Resolver.Names(setToList(placeSet), &cid)
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
					"location": nameOr(placeNames, root), "estimated_value": isk(unitPrice(prices, j.Int(item["type_id"])) * float64(qty)),
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

			return merge(map[string]any{
				"character": token.CharacterName, "query": in.Name,
				"total_units": total, "total_stacks": len(rows), "data_age": result.StaleNote(),
				"matches": project(visible, []string{"item", "quantity", "location", "estimated_value"}, concise(in.ResponseFormat)),
			}, meta), nil
		})
	})

	type bpIn struct {
		Character      string `json:"character,omitempty"       jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
		Limit          int    `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
		ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
	}
	addTool(s, &mcp.Tool{
		Name:        "eve_assets_blueprints",
		Description: "Blueprints with material/time efficiency and remaining runs.\n\nOriginals (BPO) can be used forever and report runs_left absent; copies (BPC) are consumed. Material efficiency (0-10) cuts input materials; time efficiency (0-20) cuts job duration.\n\nReturns: originals, copies, blueprints[].",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in bpIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *session.Session) (any, error) {
			token, err := a.ResolveCharacter(in.Character)
			if err != nil {
				return nil, err
			}
			if err := a.RequireScope(token, "esi-characters.read_blueprints.v1", "blueprints"); err != nil {
				return nil, err
			}
			cid := token.CharacterID
			result, err := a.ESI.GetAllPages(fmt.Sprintf("/characters/%d/blueprints", cid), &cid, nil, 40)
			if err != nil {
				return nil, err
			}
			bps := j.Maps(result.Data)
			if len(bps) == 0 {
				return map[string]any{"character": token.CharacterName, "blueprints": []any{}, "note": "None owned."}, nil
			}
			var typeIDs, placeIDs []int
			for _, b := range bps {
				typeIDs = append(typeIDs, j.Int(b["type_id"]))
				placeIDs = append(placeIDs, j.Int(b["location_id"]))
			}
			typeNames, _ := a.Resolver.Names(typeIDs, nil)
			placeNames, _ := a.Resolver.Names(placeIDs, &cid)
			var rows []map[string]any
			orig, copies := 0, 0
			for _, b := range bps {
				kind := "original"
				var runs any
				if j.Int(b["runs"]) != -1 || (b["runs"] != nil && j.Int(b["runs"]) != -1) {
					if j.Float(b["runs"]) != -1 {
						kind = "copy"
						runs = b["runs"]
						copies++
					} else {
						orig++
					}
				} else {
					orig++
				}
				// runs == -1 means original
				if j.Float(b["runs"]) == -1 {
					kind = "original"
					runs = nil
				} else {
					kind = "copy"
					runs = b["runs"]
				}
				rows = append(rows, map[string]any{
					"blueprint": typeNames[j.Int(b["type_id"])], "kind": kind,
					"material_efficiency": b["material_efficiency"], "time_efficiency": b["time_efficiency"],
					"runs_left": runs, "location": nameOr(placeNames, j.Int(b["location_id"])),
					"quantity": b["quantity"],
				})
			}
			orig, copies = 0, 0
			for _, r := range rows {
				if j.Str(r["kind"]) == "original" {
					orig++
				} else {
					copies++
				}
			}
			sort.Slice(rows, func(i, k int) bool {
				if j.Str(rows[i]["kind"]) != j.Str(rows[k]["kind"]) {
					return j.Str(rows[i]["kind"]) == "original"
				}

				return j.Int(rows[i]["material_efficiency"]) > j.Int(rows[k]["material_efficiency"])
			})
			visible, meta := page(rows, limitOr(in.Limit, 25), "")

			return merge(map[string]any{
				"character": token.CharacterName, "originals": orig, "copies": copies,
				"data_age":   result.StaleNote(),
				"blueprints": project(visible, []string{"blueprint", "kind", "material_efficiency", "time_efficiency", "runs_left"}, concise(in.ResponseFormat)),
			}, meta), nil
		})
	})
}

func valuesOf(m map[int]int) []int {
	seen := map[int]struct{}{}
	var out []int
	for _, v := range m {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}

	return out
}

func nameOr(names map[int]string, id int) string {
	if n, ok := names[id]; ok && n != "" {
		return n
	}

	return fmt.Sprintf("Unknown #%d", id)
}
