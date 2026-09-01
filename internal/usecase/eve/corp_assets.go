package eve

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/truewebber/eve-online-mcp/internal/domain/character"
	"github.com/truewebber/eve-online-mcp/internal/j"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"
)

func eveCorpAssetsList(ctx context.Context, a *session.Session, in corpAssetsListIn) (any, error) {
	if err := rejectUnknownFormat(in.ResponseFormat); err != nil {
		return nil, err
	}
	corp, err := openCorp(ctx, a, fAssets, fAssets, "corporation assets")
	if err != nil {
		return nil, err
	}
	result, err := a.ESI.GetAllPages(ctx, esiPath("corporations", esiID(corp.CorporationID), "assets"), &corp.Token.CharacterID, nil, pagesCorpAssets)
	if err != nil {
		return nil, wrap("eveCorpAssetsList", err)
	}
	assets := j.Maps(result.Data)
	if len(assets) == 0 {
		return merge(who(corp), map[string]any{fLocations: []any{}, fNote: "The corporation hangar is empty (or this character cannot see it)."}), nil
	}
	divs := corpDivisions(ctx, a, corp)
	roots := rootLocations(assets)
	prices, err := a.Resolver.ReferencePrices(ctx)
	if err != nil {
		return nil, wrap("eveCorpAssetsList", err)
	}
	typeNames, err := a.Resolver.Names(ctx, collectTypeIDs(assets), nil)
	if err != nil {
		return nil, wrap("eveCorpAssetsList", err)
	}
	placeNames, err := a.Resolver.Names(ctx, valuesOf(roots), &corp.Token.CharacterID)
	if err != nil {
		return nil, wrap("eveCorpAssetsList", err)
	}
	buckets := corpAssetBuckets(assets, roots, prices)
	rows := corpAssetLocationRows(corpLocIn{buckets: buckets, placeNames: placeNames, typeNames: typeNames, prices: prices, in: in})
	sort.Slice(rows, func(i, k int) bool { return j.Float(rows[i]["value_isk"]) > j.Float(rows[k]["value_isk"]) })
	paged := pageByOffset(rows, in.Offset, limitOr(in.Limit, limitShort), "Pass offset to continue, or filter with `location` / `min_value`.")
	out := merge(who(corp), merge(map[string]any{
		fTotalEstimatedValue: isk(corpAssetBucketTotal(buckets)), "total_locations": len(buckets),
		"matching_locations": len(rows), fValuationBasis: valuationCCPAvg,
		fDataAge:   result.StaleNote(),
		fLocations: project(paged.Rows, []string{fLocation, fValue, fDistinctTypes, fUnits}, concise(in.ResponseFormat)),
	}, paged.fields))
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

type corpLocIn struct {
	buckets               map[int]*corpAssetBucket
	placeNames, typeNames map[int]string
	prices                map[int]map[string]float64
	in                    corpAssetsListIn
}

func corpAssetLocationRows(in corpLocIn) []map[string]any {
	needle := strings.ToLower(strings.TrimSpace(in.in.Location))
	itemsN := limitOr(in.in.Items, limitTopItems)
	var rows []map[string]any
	for placeID, b := range in.buckets {
		place := nameOr(in.placeNames, placeID)
		if needle != "" && !strings.Contains(strings.ToLower(place), needle) {
			continue
		}
		if b.value < in.in.MinValue {
			continue
		}
		topItems := topItemLines(b.types, in.typeNames, in.prices, itemsN)
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
	if err := rejectUnknownFormat(in.ResponseFormat); err != nil {
		return nil, err
	}
	corp, err := openCorp(ctx, a, fAssets, fAssets, "corporation assets")
	if err != nil {
		return nil, err
	}
	result, err := a.ESI.GetAllPages(ctx, esiPath("corporations", esiID(corp.CorporationID), "assets"), &corp.Token.CharacterID, nil, pagesCorpAssets)
	if err != nil {
		return nil, wrap("eveCorpAssetsFind", err)
	}
	items := j.Maps(result.Data)
	typeNames, err := a.Resolver.Names(ctx, collectTypeIDs(items), nil)
	if err != nil {
		return nil, wrap("eveCorpAssetsFind", err)
	}
	matches := corpAssetFindMatches(items, typeNames, in.Name)
	if len(matches) == 0 {
		return merge(who(corp), map[string]any{fQuery: in.Name, fMatches: []any{}, fNote: "Nothing matching in corporation assets. Check the spelling with eve_universe_search, or look in personal hangars with eve_assets_find."}), nil
	}
	divs := corpDivisions(ctx, a, corp)
	roots := rootLocations(items)
	placeNames, err := a.Resolver.Names(ctx, corpAssetFindPlaceIDs(matches, roots), &corp.Token.CharacterID)
	if err != nil {
		return nil, wrap("eveCorpAssetsFind", err)
	}
	prices, err := a.Resolver.ReferencePrices(ctx)
	if err != nil {
		return nil, wrap("eveCorpAssetsFind", err)
	}
	rows := corpAssetFindRows(corpFindIn{
		matches: matches, byID: itemsByID(items), roots: roots,
		typeNames: typeNames, placeNames: placeNames, prices: prices, hangars: divs[fHangar],
	})
	sort.Slice(rows, func(i, k int) bool { return j.Int(rows[i][fQuantity]) > j.Int(rows[k][fQuantity]) })
	paged := pageByOffset(rows, in.Offset, limitOr(in.Limit, limitMedium), "")
	out := merge(who(corp), merge(map[string]any{
		fQuery: in.Name, "total_units": sumIntField(rows, fQuantity), "total_stacks": len(rows),
		fDataAge: result.StaleNote(),
		fMatches: project(paged.Rows, []string{fItem, fQuantity, fLocation, fHangar, fEstimatedValue}, concise(in.ResponseFormat)),
	}, paged.fields))
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

type corpFindIn struct {
	matches               []map[string]any
	byID                  map[int]map[string]any
	roots                 map[int]int
	typeNames, placeNames map[int]string
	prices                map[int]map[string]float64
	hangars               map[int]string
}

func corpAssetFindRows(in corpFindIn) []map[string]any {
	rows := make([]map[string]any, 0, len(in.matches))
	for _, item := range in.matches {
		qty := j.Int(item[fQuantity])
		if qty == 0 {
			qty = 1
		}
		container := in.byID[j.Int(item["location_id"])]
		var inside any
		if container != nil {
			inside = in.typeNames[j.Int(container[fTypeID])]
		}
		rows = append(rows, map[string]any{
			fItem: in.typeNames[j.Int(item[fTypeID])], fQuantity: qty,
			fLocation: nameOr(in.placeNames, in.roots[j.Int(item["item_id"])]), fHangar: hangarLabel(j.Str(item["location_flag"]), in.hangars),
			fEstimatedValue: isk(unitPrice(in.prices, j.Int(item[fTypeID])) * float64(qty)),
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
	if err := rejectUnknownFormat(in.ResponseFormat); err != nil {
		return nil, err
	}
	corp, err := openCorp(ctx, a, fBlueprints, fBlueprints, "corporation blueprints")
	if err != nil {
		return nil, err
	}
	result, err := a.ESI.Get(ctx, esiPath("corporations", esiID(corp.CorporationID), "blueprints"), &corp.Token.CharacterID, esiPageQuery(in.Page, nil), nil)
	if err != nil {
		return nil, wrap("eveCorpBlueprints", err)
	}
	bps := j.Maps(result.Data)
	if len(bps) == 0 {
		return merge(who(corp), merge(map[string]any{fBlueprints: []any{}, fNote: "The corporation holds no blueprints."}, pageByNumber(nil, in.Page, result.PageCount(), limitOr(in.Limit, limitLong)).fields)), nil
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
	paged := pageByNumber(listed.rows, in.Page, result.PageCount(), limitOr(in.Limit, limitLong))

	return merge(who(corp), merge(map[string]any{
		"originals": listed.originals, "copies": listed.copies, fDataAge: result.StaleNote(),
		fBlueprints: project(paged.Rows, []string{fBlueprint, fKind, fMaterialEfficiency, fTimeEfficiency, fRunsLeft, fHangar}, concise(in.ResponseFormat)),
	}, paged.fields)), nil
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
		return corpNameMaps{}, wrap("corpBlueprintNames", err)
	}
	placeNames, err := a.Resolver.Names(ctx, placeIDs, &corp.Token.CharacterID)
	if err != nil {
		return corpNameMaps{}, wrap("corpBlueprintNames", err)
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
