package names

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/adapter/store"
	"github.com/truewebber/eve-online-mcp/internal/domain/j"
)

const (
	nameBatch        = 900
	idsBatch         = 500
	structureIDFloor = 1_000_000_000_000
	priceTTL         = 3600.0
	staticBlobAge    = 30 * 24 * time.Hour
	hubOrderPages    = 10
)

func categoryKind(key string) string {
	switch key {
	case "agents":
		return "agent"
	case "alliances":
		return "alliance"
	case "characters":
		return "character"
	case "constellations":
		return "constellation"
	case "corporations":
		return "corporation"
	case "factions":
		return "faction"
	case "inventory_types":
		return "item type"
	case "regions":
		return "region"
	case "stations":
		return "station"
	case "systems":
		return "solar system"
	default:
		return ""
	}
}

type NameMatch struct {
	ID       int
	Name     string
	Category string
	Kind     string
}

type NameResolution struct {
	Query        string
	Chosen       *NameMatch
	Alternatives []NameMatch
}

func (r NameResolution) Ambiguous() bool { return len(r.Alternatives) > 0 }

func (r NameResolution) Describe() string {
	if r.Chosen == nil {
		return fmt.Sprintf("%q matched nothing", r.Query)
	}
	others := make([]string, 0, len(r.Alternatives))
	for _, m := range r.Alternatives {
		others = append(others, article(m.Kind)+fmt.Sprintf(" (#%d)", m.ID))
	}
	chosen := article(r.Chosen.Kind) + fmt.Sprintf(" (#%d)", r.Chosen.ID)

	return fmt.Sprintf("%q is %s and also %s", r.Query, chosen, strings.Join(others, ", "))
}

func article(kind string) string {
	if kind == "" {
		return "a thing"
	}
	switch strings.ToLower(kind[:1]) {
	case "a", "e", "i", "o", "u":
		return "an " + kind
	}

	return "a " + kind
}

type Resolver struct {
	esi      *esi.Client
	store    *store.Store
	prices   map[int]map[string]float64
	pricesAt time.Time
	priceMu  sync.Mutex
}

func New(e *esi.Client, db *store.Store) *Resolver {
	return &Resolver{esi: e, store: db}
}

func (r *Resolver) Names(ctx context.Context, ids []int, characterID *int) (map[int]string, error) {
	wanted := uniquePositive(ids)
	if len(wanted) == 0 {
		return map[int]string{}, nil
	}
	out, missing, err := r.lookupCachedNames(ctx, wanted)
	if err != nil {
		return nil, err
	}
	if len(missing) == 0 {
		return out, nil
	}
	r.fillMissingNames(ctx, out, missing, characterID)
	for id := range wanted {
		if _, ok := out[id]; !ok {
			out[id] = fmt.Sprintf("Unknown #%d", id)
		}
	}

	return out, nil
}

func (r *Resolver) Name(ctx context.Context, id int, characterID *int) (string, error) {
	m, err := r.Names(ctx, []int{id}, characterID)
	if err != nil {
		return fmt.Sprintf("Unknown #%d", id), err
	}
	if n, ok := m[id]; ok {
		return n, nil
	}

	return fmt.Sprintf("Unknown #%d", id), nil
}

func (r *Resolver) IDsFromNames(ctx context.Context, names []string) (map[string]any, error) {
	seen := map[string]struct{}{}
	var unique []string
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		unique = append(unique, n)
	}
	if len(unique) == 0 {
		return map[string]any{}, nil
	}
	out := map[string]any{}
	for start := 0; start < len(unique); start += idsBatch {
		end := min(start+idsBatch, len(unique))
		part, err := r.esi.Post(ctx, "/universe/ids", nil, nil, unique[start:end])
		if err != nil {
			return nil, err
		}
		for key, rows := range j.Map(part) {
			existing := j.Slice(out[key])
			out[key] = append(existing, j.Slice(rows)...)
		}
	}

	return out, nil
}

func (r *Resolver) ResolveNames(ctx context.Context, names []string, prefer, only []string) (map[string]NameResolution, error) {
	lookup, err := r.IDsFromNames(ctx, names)
	if err != nil {
		return nil, err
	}

	return pickNameResolutions(names, collectNameBuckets(lookup, only), preferRank(prefer)), nil
}

func (r *Resolver) TypeInfo(ctx context.Context, typeID int) (map[string]any, error) {
	key := fmt.Sprintf("type:%d", typeID)
	maxAge := staticBlobAge
	cached, err := r.blob(ctx, key, &maxAge)
	if err != nil {
		return nil, err
	}
	if cached != nil {
		return j.Map(cached), nil
	}
	result, err := r.esi.Get(ctx, fmt.Sprintf("/universe/types/%d", typeID), nil, nil, nil)
	if err != nil {
		return nil, err
	}
	data := j.Map(result.Data)
	if err := r.putBlob(ctx, key, data); err != nil {
		log.Printf("names: put type %s: %v", key, err)
	}

	return data, nil
}

func (r *Resolver) GroupName(ctx context.Context, groupID int) string {
	if groupID == 0 {
		return "unknown"
	}
	key := fmt.Sprintf("group:%d", groupID)
	maxAge := staticBlobAge
	cached, err := r.blob(ctx, key, &maxAge)
	if err != nil || cached == nil {
		result, err := r.esi.Get(ctx, fmt.Sprintf("/universe/groups/%d", groupID), nil, nil, nil)
		if err != nil {
			return fmt.Sprintf("Group #%d", groupID)
		}
		cached = result.Data
		if err := r.putBlob(ctx, key, cached); err != nil {
			log.Printf("names: put group %s: %v", key, err)
		}
	}
	name := j.Str(j.Map(cached)["name"])
	if name == "" {
		return fmt.Sprintf("Group #%d", groupID)
	}

	return name
}

func (r *Resolver) TypeInfos(ctx context.Context, typeIDs []int) map[int]map[string]any {
	seen := map[int]struct{}{}
	var unique []int
	for _, t := range typeIDs {
		if t == 0 {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		unique = append(unique, t)
	}
	out := map[int]map[string]any{}
	type box struct {
		id   int
		info map[string]any
	}
	ch := make(chan box, len(unique))
	for _, t := range unique {
		go func(t int) {
			info, err := r.TypeInfo(ctx, t)
			if err == nil {
				ch <- box{t, info}
			} else {
				ch <- box{t, nil}
			}
		}(t)
	}
	for range unique {
		b := <-ch
		if b.info != nil {
			out[b.id] = b.info
		}
	}

	return out
}

func (r *Resolver) ReferencePrices(ctx context.Context) (map[int]map[string]float64, error) {
	r.priceMu.Lock()
	defer r.priceMu.Unlock()
	if r.prices != nil && time.Since(r.pricesAt) < time.Duration(priceTTL*float64(time.Second)) {
		return r.prices, nil
	}
	ttl := time.Duration(priceTTL * float64(time.Second))
	cached, err := r.blob(ctx, "markets:prices", &ttl)
	if err != nil {
		return nil, err
	}
	if cached == nil {
		result, err := r.esi.Get(ctx, "/markets/prices", nil, nil, nil)
		if err != nil {
			return nil, err
		}
		blob := map[string]any{}
		for _, row := range j.Maps(result.Data) {
			blob[strconv.Itoa(j.Int(row["type_id"]))] = map[string]any{
				"average":  j.Float(row["average_price"]),
				"adjusted": j.Float(row["adjusted_price"]),
			}
		}
		if err := r.putBlob(ctx, "markets:prices", blob); err != nil {
			log.Printf("names: put market prices: %v", err)
		}
		cached = blob
	}
	prices := map[int]map[string]float64{}
	for k, v := range j.Map(cached) {
		var id int
		if _, err := fmt.Sscanf(k, "%d", &id); err != nil {
			continue
		}
		m := j.Map(v)
		prices[id] = map[string]float64{"average": j.Float(m["average"]), "adjusted": j.Float(m["adjusted"])}
	}
	r.prices = prices
	r.pricesAt = time.Now()

	return prices, nil
}

func (r *Resolver) ReferencePrice(ctx context.Context, typeID int) float64 {
	prices, err := r.ReferencePrices(ctx)
	if err != nil {
		return 0
	}
	entry := prices[typeID]
	if entry["average"] != 0 {
		return entry["average"]
	}

	return entry["adjusted"]
}

func (r *Resolver) HubQuotes(ctx context.Context, typeID, regionID int, stationID *int) (map[string]any, error) {
	result, err := r.esi.GetAllPages(
		ctx,
		fmt.Sprintf("/markets/%d/orders", regionID),
		nil,
		map[string]any{"type_id": typeID, "order_type": "all"},
		hubOrderPages,
	)
	if err != nil {
		return nil, err
	}
	orders := j.Maps(result.Data)
	if stationID != nil {
		var atHub []map[string]any
		for _, o := range orders {
			if j.Int(o["location_id"]) == *stationID {
				atHub = append(atHub, o)
			}
		}
		if len(atHub) > 0 {
			orders = atHub
		}
	}
	var buys, sells []float64
	var sellVol, buyVol float64
	for _, o := range orders {
		price := j.Float(o["price"])
		vol := j.Float(o["volume_remain"])
		if j.Bool(o["is_buy_order"]) {
			buys = append(buys, price)
			buyVol += vol
		} else {
			sells = append(sells, price)
			sellVol += vol
		}
	}
	sort.Float64s(sells)
	sort.Sort(sort.Reverse(sort.Float64Slice(buys)))
	out := map[string]any{
		"type_id": typeID, "region_id": regionID,
		"best_sell": nil, "best_buy": nil,
		"sell_order_count": len(sells), "buy_order_count": len(buys),
		"sell_volume": sellVol, "buy_volume": buyVol,
		"data_age": result.StaleNote(),
	}
	if stationID != nil {
		out["station_id"] = *stationID
	}
	if len(sells) > 0 {
		out["best_sell"] = sells[0]
	}
	if len(buys) > 0 {
		out["best_buy"] = buys[0]
	}

	return out, nil
}

func uniquePositive(ids []int) map[int]struct{} {
	wanted := map[int]struct{}{}
	for _, id := range ids {
		if id != 0 {
			wanted[id] = struct{}{}
		}
	}

	return wanted
}

func (r *Resolver) lookupCachedNames(ctx context.Context, wanted map[int]struct{}) (map[int]string, []int, error) {
	list := make([]int, 0, len(wanted))
	for id := range wanted {
		list = append(list, id)
	}
	id64s := make([]int64, len(list))
	for i, id := range list {
		id64s[i] = int64(id)
	}
	cached, err := r.store.NameGet(ctx, id64s)
	if err != nil {
		return nil, nil, err
	}
	out := map[int]string{}
	var missing []int
	for id := range wanted {
		if row, ok := cached[int64(id)]; ok {
			out[id] = row.Name
		} else {
			missing = append(missing, id)
		}
	}

	return out, missing, nil
}

func (r *Resolver) fillMissingNames(ctx context.Context, out map[int]string, missing []int, characterID *int) {
	var universal, structures []int
	for _, id := range missing {
		if id >= structureIDFloor {
			structures = append(structures, id)
		} else {
			universal = append(universal, id)
		}
	}
	r.fillUniverseNames(ctx, out, universal)
	if len(structures) > 0 && characterID != nil {
		r.fillStructureNames(ctx, out, structures, *characterID)
	}
}

func (r *Resolver) fillUniverseNames(ctx context.Context, out map[int]string, universal []int) {
	for start := 0; start < len(universal); start += nameBatch {
		end := min(start+nameBatch, len(universal))
		chunk := universal[start:end]
		result, err := r.esi.Post(ctx, "/universe/names", nil, nil, chunk)
		if err != nil {
			log.Printf("bulk name lookup failed for %d ids: %v", len(chunk), err)

			continue
		}
		var entries []store.NameRow
		for _, row := range j.Maps(result) {
			id := j.Int(row["id"])
			name := j.Str(row["name"])
			if id == 0 || name == "" {
				continue
			}
			entries = append(entries, store.NameRow{ID: int64(id), Name: name, Category: j.Str(row["category"])})
			out[id] = name
		}
		if err := r.store.NamePut(ctx, entries); err != nil {
			log.Printf("names: put universe names: %v", err)
		}
	}
}

func (r *Resolver) fillStructureNames(ctx context.Context, out map[int]string, structures []int, characterID int) {
	type box struct {
		id   int
		name string
		ok   bool
	}
	ch := make(chan box, len(structures))
	for _, sid := range structures {
		go func(sid int) {
			name, err := r.structureName(ctx, sid, characterID)
			ch <- box{sid, name, err == nil}
		}(sid)
	}
	var entries []store.NameRow
	for range structures {
		b := <-ch
		if !b.ok {
			continue
		}
		entries = append(entries, store.NameRow{ID: int64(b.id), Name: b.name, Category: "structure"})
		out[b.id] = b.name
	}
	if err := r.store.NamePut(ctx, entries); err != nil {
		log.Printf("names: put structure names: %v", err)
	}
}

func collectNameBuckets(lookup map[string]any, only []string) map[string][]NameMatch {
	onlySet := map[string]struct{}{}
	for _, k := range only {
		onlySet[k] = struct{}{}
	}
	buckets := map[string][]NameMatch{}
	for key, entries := range lookup {
		if len(onlySet) > 0 {
			if _, ok := onlySet[key]; !ok {
				continue
			}
		}
		kind := categoryKind(key)
		if kind == "" {
			kind = key
		}
		for _, entry := range j.Maps(entries) {
			id := j.Int(entry["id"])
			name := j.Str(entry["name"])
			if id == 0 || name == "" {
				continue
			}
			k := strings.ToLower(strings.TrimSpace(name))
			buckets[k] = append(buckets[k], NameMatch{ID: id, Name: name, Category: key, Kind: kind})
		}
	}

	return buckets
}

func preferRank(prefer []string) map[string]int {
	rank := map[string]int{}
	for i, key := range prefer {
		rank[key] = i
	}

	return rank
}

func pickNameResolutions(names []string, buckets map[string][]NameMatch, rank map[string]int) map[string]NameResolution {
	out := map[string]NameResolution{}
	for _, asked := range names {
		wanted := strings.ToLower(strings.TrimSpace(asked))
		matches := append([]NameMatch{}, buckets[wanted]...)
		sort.Slice(matches, lessNameMatch(matches, rank))
		res := NameResolution{Query: strings.TrimSpace(asked)}
		if len(matches) > 0 {
			cp := matches[0]
			res.Chosen = &cp
			res.Alternatives = matches[1:]
		}
		out[wanted] = res
	}

	return out
}

func lessNameMatch(matches []NameMatch, rank map[string]int) func(i, j int) bool {
	return func(i, j int) bool {
		ri, okI := rank[matches[i].Category]
		if !okI {
			ri = len(rank)
		}
		rj, okJ := rank[matches[j].Category]
		if !okJ {
			rj = len(rank)
		}
		if ri != rj {
			return ri < rj
		}
		if matches[i].Category != matches[j].Category {
			return matches[i].Category < matches[j].Category
		}

		return matches[i].ID < matches[j].ID
	}
}

func (r *Resolver) structureName(ctx context.Context, structureID, characterID int) (string, error) {
	result, err := r.esi.Get(ctx, fmt.Sprintf("/universe/structures/%d", structureID), &characterID, nil, nil)
	if err != nil {
		return "", err
	}
	name := j.Str(j.Map(result.Data)["name"])
	if name == "" {
		return fmt.Sprintf("Structure #%d", structureID), nil
	}

	return name, nil
}

func (r *Resolver) blob(ctx context.Context, key string, maxAge *time.Duration) (any, error) {
	raw, err := r.store.BlobGet(ctx, key, maxAge)
	if err != nil || raw == nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}

	return v, nil
}

func (r *Resolver) putBlob(ctx context.Context, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}

	return r.store.BlobPut(ctx, key, raw)
}
