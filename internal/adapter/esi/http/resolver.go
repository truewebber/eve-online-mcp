package http

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/truewebber/gopkg/log"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/j"
)

const (
	nameBatch        = 900
	idsBatch         = 500
	structureIDFloor = 1_000_000_000_000
	priceTTL         = 3600.0
	staticBlobAge    = 30 * 24 * time.Hour
	hubOrderPages    = 10
)

type blobEntry struct {
	value any
	at    time.Time
}

type blobCache struct {
	mu sync.Mutex
	m  map[string]blobEntry
}

type priceCache struct {
	mu     sync.Mutex
	prices map[int]map[string]float64
	at     time.Time
}

type Resolver struct {
	esi    esi.Client
	names  *nameCache
	blobs  *blobCache
	prices *priceCache
	logger log.Logger
}

func NewResolver(c esi.Client, logger log.Logger) (*Resolver, error) {
	if c == nil {
		return nil, errClientRequired
	}
	if logger == nil {
		return nil, errLoggerRequired
	}

	return &Resolver{
		esi:    c,
		names:  newNameCache(),
		blobs:  &blobCache{m: map[string]blobEntry{}},
		prices: &priceCache{},
		logger: logger,
	}, nil
}

func (r *Resolver) ForUser(c esi.Client) *Resolver {
	return &Resolver{esi: c, names: r.names, blobs: r.blobs, prices: r.prices, logger: r.logger}
}

func (r *Resolver) Names(ctx context.Context, ids []int, characterID *int) (map[int]string, error) {
	wanted := uniquePositive(ids)
	if len(wanted) == 0 {
		return map[int]string{}, nil
	}
	list := make([]int, 0, len(wanted))
	for id := range wanted {
		list = append(list, id)
	}
	hit := r.names.get(list)
	if len(hit.missing) == 0 {
		return hit.names, nil
	}
	r.fillMissingNames(ctx, hit.names, hit.missing, characterID)
	for id := range wanted {
		if _, ok := hit.names[id]; !ok {
			hit.names[id] = fmt.Sprintf("Unknown #%d", id)
		}
	}

	return hit.names, nil
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
			return nil, wrap("IDsFromNames", err)
		}
		for key, rows := range j.Map(part) {
			existing := j.Slice(out[key])
			out[key] = append(existing, j.Slice(rows)...)
		}
	}

	return out, nil
}

func (r *Resolver) ResolveNames(ctx context.Context, names []string, prefer, only []string) (map[string]esi.NameResolution, error) {
	lookup, err := r.IDsFromNames(ctx, names)
	if err != nil {
		return nil, err
	}

	return pickNameResolutions(names, collectNameBuckets(lookup, only), preferRank(prefer)), nil
}

func (r *Resolver) TypeInfo(ctx context.Context, typeID int) (map[string]any, error) {
	key := fmt.Sprintf("type:%d", typeID)
	if cached := r.blob(key, staticBlobAge); cached != nil {
		return j.Map(cached), nil
	}
	result, err := r.esi.Get(ctx, esi.Path("universe", "types", esi.ID(typeID)), nil, nil, nil)
	if err != nil {
		return nil, wrap("TypeInfo", err)
	}
	data := j.Map(result.Data)
	r.putBlob(key, data)

	return data, nil
}

func (r *Resolver) GroupName(ctx context.Context, groupID int) string {
	if groupID == 0 {
		return "unknown"
	}
	key := fmt.Sprintf("group:%d", groupID)
	cached := r.blob(key, staticBlobAge)
	if cached == nil {
		result, err := r.esi.Get(ctx, esi.Path("universe", "groups", esi.ID(groupID)), nil, nil, nil)
		if err != nil {
			return fmt.Sprintf("Group #%d", groupID)
		}
		cached = result.Data
		r.putBlob(key, cached)
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
	r.prices.mu.Lock()
	defer r.prices.mu.Unlock()
	if r.prices.prices != nil && time.Since(r.prices.at) < time.Duration(priceTTL*float64(time.Second)) {
		return r.prices.prices, nil
	}
	result, err := r.esi.Get(ctx, "/markets/prices", nil, nil, nil)
	if err != nil {
		return nil, wrap("ReferencePrices", err)
	}
	prices := map[int]map[string]float64{}
	for _, row := range j.Maps(result.Data) {
		id := j.Int(row["type_id"])
		prices[id] = map[string]float64{
			"average":  j.Float(row["average_price"]),
			"adjusted": j.Float(row["adjusted_price"]),
		}
	}
	r.prices.prices = prices
	r.prices.at = time.Now()

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
		esi.Path("markets", esi.ID(regionID), "orders"),
		nil,
		map[string]any{"type_id": typeID, "order_type": "all"},
		hubOrderPages,
	)
	if err != nil {
		return nil, wrap("HubQuotes", err)
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
			r.logger.Error("esi: bulk name lookup", "ids", len(chunk), "err", err)

			continue
		}
		for _, row := range j.Maps(result) {
			id := j.Int(row["id"])
			name := j.Str(row["name"])
			if id == 0 || name == "" {
				continue
			}
			r.names.put(id, name)
			out[id] = name
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
	for range structures {
		b := <-ch
		if !b.ok {
			continue
		}
		r.names.put(b.id, b.name)
		out[b.id] = b.name
	}
}

func collectNameBuckets(lookup map[string]any, only []string) map[string][]esi.NameMatch {
	onlySet := map[string]struct{}{}
	for _, k := range only {
		onlySet[k] = struct{}{}
	}
	buckets := map[string][]esi.NameMatch{}
	for key, entries := range lookup {
		if len(onlySet) > 0 {
			if _, ok := onlySet[key]; !ok {
				continue
			}
		}
		kind := esi.CategoryKind(key)
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
			buckets[k] = append(buckets[k], esi.NameMatch{ID: id, Name: name, Category: key, Kind: kind})
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

func pickNameResolutions(names []string, buckets map[string][]esi.NameMatch, rank map[string]int) map[string]esi.NameResolution {
	out := map[string]esi.NameResolution{}
	for _, asked := range names {
		wanted := strings.ToLower(strings.TrimSpace(asked))
		matches := append([]esi.NameMatch{}, buckets[wanted]...)
		sort.Slice(matches, lessNameMatch(matches, rank))
		res := esi.NameResolution{Query: strings.TrimSpace(asked)}
		if len(matches) > 0 {
			cp := matches[0]
			res.Chosen = &cp
			res.Alternatives = matches[1:]
		}
		out[wanted] = res
	}

	return out
}

func lessNameMatch(matches []esi.NameMatch, rank map[string]int) func(i, j int) bool {
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
	result, err := r.esi.Get(ctx, esi.Path("universe", "structures", esi.ID(structureID)), &characterID, nil, nil)
	if err != nil {
		return "", wrap("structureName", err)
	}
	name := j.Str(j.Map(result.Data)["name"])
	if name == "" {
		return fmt.Sprintf("Structure #%d", structureID), nil
	}

	return name, nil
}

func (r *Resolver) blob(key string, maxAge time.Duration) any {
	r.blobs.mu.Lock()
	defer r.blobs.mu.Unlock()
	entry, ok := r.blobs.m[key]
	if !ok || time.Since(entry.at) > maxAge {
		return nil
	}

	return entry.value
}

func (r *Resolver) putBlob(key string, value any) {
	r.blobs.mu.Lock()
	defer r.blobs.mu.Unlock()
	r.blobs.m[key] = blobEntry{value: value, at: time.Now()}
}
