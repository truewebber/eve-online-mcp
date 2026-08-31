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

type assetsListIn struct {
	Character      string  `json:"character,omitempty"       jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
	Location       string  `json:"location,omitempty"        jsonschema:"Case-insensitive substring of a station or structure name, e.g. 'Jita' or 'Amarr VIII'. Empty means every location."`
	MinValue       float64 `json:"min_value,omitempty"       jsonschema:"Hide locations holding less than this many ISK.,minimum=0"`
	Limit          int     `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	Items          int     `json:"items,omitempty"           jsonschema:"Maximum items to list inside each location in detailed mode.,minimum=1,maximum=200"`
	ResponseFormat string  `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

type assetsFindIn struct {
	Name           string `json:"name"                      jsonschema:"Case-insensitive substring of the item type name, e.g. 'Drake' or 'Tritanium'."`
	Character      string `json:"character,omitempty"       jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
	Limit          int    `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

type assetsBlueprintsIn struct {
	Character      string `json:"character,omitempty"       jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
	Limit          int    `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

type assetBucket struct {
	value float64
	units int
	types map[int]int
}

func registerAssets(s *mcp.Server) {
	addTool(s, &mcp.Tool{
		Name:        "eve_assets_list",
		Description: "Assets grouped by the station or structure they sit in, with an ISK estimate.\n\nItems nested inside containers and ship holds are rolled up into the station that ultimately holds them. Valuation uses CCP's global average price per type, not a hub quote. ESI caches assets for a full hour.\n\nReturns: total_estimated_value, total_locations, locations[] sorted by value.",
	}, sessionTool(eveAssetsList))
	addTool(s, &mcp.Tool{
		Name:        "eve_assets_find",
		Description: "Locate a specific item across every hangar, container and ship hold.\n\nAnswers \"where did I leave my Orca\" or \"do I still have any Tritanium\". Searches personal assets only. Corporation hangars are eve_corp_assets_find.\n\nReturns: total_units, total_stacks, matches[].",
	}, sessionTool(eveAssetsFind))
	addTool(s, &mcp.Tool{
		Name:        "eve_assets_blueprints",
		Description: "Blueprints with material/time efficiency and remaining runs.\n\nOriginals (BPO) can be used forever and report runs_left absent; copies (BPC) are consumed. Material efficiency (0-10) cuts input materials; time efficiency (0-20) cuts job duration.\n\nReturns: originals, copies, blueprints[].",
	}, sessionTool(eveAssetsBlueprints))
}

func eveAssetsList(ctx context.Context, a *session.Session, in assetsListIn) (any, error) {
	token, err := a.ResolveCharacter(ctx, in.Character)
	if err != nil {
		return nil, wrap("eveAssetsList", err)
	}
	if err := a.RequireScope(token, "esi-assets.read_assets.v1", fAssets); err != nil {
		return nil, wrap("eveAssetsList", err)
	}
	cid := token.CharacterID
	result, err := a.ESI.GetAllPages(ctx, esiPath("characters", esiID(cid), "assets"), &cid, nil, pagesESI)
	if err != nil {
		return nil, wrap("eveAssetsList", err)
	}
	assets := j.Maps(result.Data)
	if len(assets) == 0 {
		return map[string]any{fCharacter: token.CharacterName, fLocations: []any{}, fNote: "This character holds no personal assets (corp hangars are separate)."}, nil
	}
	roots := rootLocations(assets)
	prices, err := a.Resolver.ReferencePrices(ctx)
	if err != nil {
		return nil, wrap("eveAssetsList", err)
	}
	var typeIDs []int
	for _, i := range assets {
		typeIDs = append(typeIDs, j.Int(i[fTypeID]))
	}
	typeNames, err := a.Resolver.Names(ctx, typeIDs, nil)
	if err != nil {
		return nil, wrap("eveAssetsList", err)
	}
	placeNames, err := a.Resolver.Names(ctx, valuesOf(roots), &cid)
	if err != nil {
		return nil, wrap("eveAssetsList", err)
	}
	buckets := assetBuckets(assets, roots, prices)
	rows := assetLocationRows(buckets, placeNames, typeNames, prices, in.Location, in.MinValue, limitOr(in.Items, limitTopItems))
	sort.Slice(rows, func(i, k int) bool { return j.Float(rows[i]["value_isk"]) > j.Float(rows[k]["value_isk"]) })
	visible, meta := page(rows, limitOr(in.Limit, limitShort), "Raise `limit`, or filter with `location` / `min_value`.")
	total := 0.0
	for _, b := range buckets {
		total += b.value
	}

	return merge(map[string]any{
		fCharacter: token.CharacterName, fTotalEstimatedValue: isk(total),
		"total_locations": len(buckets), "matching_locations": len(rows),
		fValuationBasis: valuationCCPAvg,
		fDataAge:        result.StaleNote(),
		fLocations:      project(visible, []string{fLocation, fValue, fDistinctTypes, fUnits}, concise(in.ResponseFormat)),
	}, meta), nil
}

func assetBuckets(assets []map[string]any, roots map[int]int, prices map[int]map[string]float64) map[int]*assetBucket {
	buckets := map[int]*assetBucket{}
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
			b = &assetBucket{types: map[int]int{}}
			buckets[root] = b
		}
		tid := j.Int(item[fTypeID])
		b.value += unitPrice(prices, tid) * float64(qty)
		b.units += qty
		b.types[tid] += qty
	}

	return buckets
}

func assetLocationRows(buckets map[int]*assetBucket, placeNames, typeNames map[int]string, prices map[int]map[string]float64, location string, minValue float64, itemsN int) []map[string]any {
	needle := strings.ToLower(strings.TrimSpace(location))
	var rows []map[string]any
	for placeID, b := range buckets {
		place := placeNames[placeID]
		if place == "" {
			place = fmt.Sprintf("Unknown #%d", placeID)
		}
		if needle != "" && !strings.Contains(strings.ToLower(place), needle) {
			continue
		}
		if b.value < minValue {
			continue
		}
		rows = append(rows, map[string]any{
			fLocation: place, fValue: isk(b.value), "value_isk": mathRound(b.value, decimalPlaces),
			fDistinctTypes: len(b.types), fUnits: b.units, "location_id": placeID, "top_items": topItemLines(b.types, typeNames, prices, itemsN),
		})
	}

	return rows
}

func topItemLines(types map[int]int, typeNames map[int]string, prices map[int]map[string]float64, itemsN int) []string {
	type kv struct{ t, q int }
	var top []kv
	for t, q := range types {
		top = append(top, kv{t, q})
	}
	sort.Slice(top, func(i, k int) bool {
		return lineValue(prices, top[i].t, top[i].q) > lineValue(prices, top[k].t, top[k].q)
	})
	if len(top) > itemsN {
		top = top[:itemsN]
	}
	topItems := make([]string, 0, len(top))
	for _, x := range top {
		topItems = append(topItems, fmt.Sprintf("%v x%d (~%s)", nameOr(typeNames, x.t), x.q, isk(unitPrice(prices, x.t)*float64(x.q))))
	}

	return topItems
}

func eveAssetsFind(ctx context.Context, a *session.Session, in assetsFindIn) (any, error) {
	if strings.TrimSpace(in.Name) == "" {
		return map[string]any{fError: "name is required"}, nil
	}
	token, err := a.ResolveCharacter(ctx, in.Character)
	if err != nil {
		return nil, wrap("eveAssetsFind", err)
	}
	if err := a.RequireScope(token, "esi-assets.read_assets.v1", fAssets); err != nil {
		return nil, wrap("eveAssetsFind", err)
	}
	cid := token.CharacterID
	result, err := a.ESI.GetAllPages(ctx, esiPath("characters", esiID(cid), "assets"), &cid, nil, pagesESI)
	if err != nil {
		return nil, wrap("eveAssetsFind", err)
	}
	items := j.Maps(result.Data)
	var typeIDs []int
	for _, i := range items {
		typeIDs = append(typeIDs, j.Int(i[fTypeID]))
	}
	typeNames, err := a.Resolver.Names(ctx, typeIDs, nil)
	if err != nil {
		return nil, wrap("eveAssetsFind", err)
	}
	matches := assetFindMatches(items, typeNames, in.Name)
	if len(matches) == 0 {
		return map[string]any{
			fCharacter: token.CharacterName, fQuery: in.Name, fMatches: []any{},
			fNote: "Nothing matching in personal assets. Check the spelling with eve_universe_search, or the item may be in a corp hangar (eve_corp_assets_find).",
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
	placeNames, err := a.Resolver.Names(ctx, setToList(placeSet), &cid)
	if err != nil {
		return nil, wrap("eveAssetsFind", err)
	}
	prices, err := a.Resolver.ReferencePrices(ctx)
	if err != nil {
		return nil, wrap("eveAssetsFind", err)
	}
	rows := assetFindRows(matches, roots, byID, typeNames, placeNames, prices)
	sort.Slice(rows, func(i, k int) bool { return j.Int(rows[i][fQuantity]) > j.Int(rows[k][fQuantity]) })
	visible, meta := page(rows, limitOr(in.Limit, limitMedium), "")
	total := 0
	for _, r := range rows {
		total += j.Int(r[fQuantity])
	}

	return merge(map[string]any{
		fCharacter: token.CharacterName, fQuery: in.Name,
		"total_units": total, "total_stacks": len(rows), fDataAge: result.StaleNote(),
		fMatches: project(visible, []string{fItem, fQuantity, fLocation, fEstimatedValue}, concise(in.ResponseFormat)),
	}, meta), nil
}

func assetFindMatches(items []map[string]any, typeNames map[int]string, name string) []map[string]any {
	needle := strings.ToLower(strings.TrimSpace(name))
	var matches []map[string]any
	for _, i := range items {
		if strings.Contains(strings.ToLower(typeNames[j.Int(i[fTypeID])]), needle) {
			matches = append(matches, i)
		}
	}

	return matches
}

func assetFindRows(matches []map[string]any, roots map[int]int, byID map[int]map[string]any, typeNames, placeNames map[int]string, prices map[int]map[string]float64) []map[string]any {
	rows := make([]map[string]any, 0, len(matches))
	for _, item := range matches {
		root := roots[j.Int(item["item_id"])]
		container := byID[j.Int(item["location_id"])]
		qty := j.Int(item[fQuantity])
		if qty == 0 {
			qty = 1
		}
		var inside any
		if container != nil {
			inside = typeNames[j.Int(container[fTypeID])]
		}
		rows = append(rows, map[string]any{
			fItem: typeNames[j.Int(item[fTypeID])], fQuantity: qty,
			fLocation: nameOr(placeNames, root), fEstimatedValue: isk(unitPrice(prices, j.Int(item[fTypeID])) * float64(qty)),
			"inside": inside, "slot": item["location_flag"],
			"packaged": !j.Bool(item["is_singleton"]), "item_id": item["item_id"],
		})
	}

	return rows
}

func eveAssetsBlueprints(ctx context.Context, a *session.Session, in assetsBlueprintsIn) (any, error) {
	token, err := a.ResolveCharacter(ctx, in.Character)
	if err != nil {
		return nil, wrap("eveAssetsBlueprints", err)
	}
	if err := a.RequireScope(token, "esi-characters.read_blueprints.v1", fBlueprints); err != nil {
		return nil, wrap("eveAssetsBlueprints", err)
	}
	cid := token.CharacterID
	result, err := a.ESI.GetAllPages(ctx, esiPath("characters", esiID(cid), "blueprints"), &cid, nil, pagesESI)
	if err != nil {
		return nil, wrap("eveAssetsBlueprints", err)
	}
	bps := j.Maps(result.Data)
	if len(bps) == 0 {
		return map[string]any{fCharacter: token.CharacterName, fBlueprints: []any{}, fNote: "None owned."}, nil
	}
	var typeIDs, placeIDs []int
	for _, b := range bps {
		typeIDs = append(typeIDs, j.Int(b[fTypeID]))
		placeIDs = append(placeIDs, j.Int(b["location_id"]))
	}
	typeNames, err := a.Resolver.Names(ctx, typeIDs, nil)
	if err != nil {
		return nil, wrap("eveAssetsBlueprints", err)
	}
	placeNames, err := a.Resolver.Names(ctx, placeIDs, &cid)
	if err != nil {
		return nil, wrap("eveAssetsBlueprints", err)
	}
	var rows []map[string]any
	orig, copies := 0, 0
	for _, b := range bps {
		kind := vOriginal
		var runs any
		if j.Float(b["runs"]) == -1 {
			orig++
		} else {
			kind = "copy"
			runs = b["runs"]
			copies++
		}
		rows = append(rows, map[string]any{
			fBlueprint: typeNames[j.Int(b[fTypeID])], fKind: kind,
			fMaterialEfficiency: b[fMaterialEfficiency], fTimeEfficiency: b[fTimeEfficiency],
			fRunsLeft: runs, fLocation: nameOr(placeNames, j.Int(b["location_id"])),
			fQuantity: b[fQuantity],
		})
	}
	sort.Slice(rows, func(i, k int) bool {
		if j.Str(rows[i][fKind]) != j.Str(rows[k][fKind]) {
			return j.Str(rows[i][fKind]) == vOriginal
		}

		return j.Int(rows[i][fMaterialEfficiency]) > j.Int(rows[k][fMaterialEfficiency])
	})
	visible, meta := page(rows, limitOr(in.Limit, limitLong), "")

	return merge(map[string]any{
		fCharacter: token.CharacterName, "originals": orig, "copies": copies,
		fDataAge:    result.StaleNote(),
		fBlueprints: project(visible, []string{fBlueprint, fKind, fMaterialEfficiency, fTimeEfficiency, fRunsLeft}, concise(in.ResponseFormat)),
	}, meta), nil
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
