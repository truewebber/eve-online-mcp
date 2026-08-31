package eve

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/domain/j"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type industryJobsIn struct {
	Character        string `json:"character,omitempty"         jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
	IncludeCompleted *bool  `json:"include_completed,omitempty" jsonschema:"Also return jobs that already delivered. Default false."`
	Limit            int    `json:"limit,omitempty"             jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	ResponseFormat   string `json:"response_format,omitempty"   jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

type industryPlanetsIn struct {
	Character string `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
	Detail    *bool  `json:"detail,omitempty"    jsonschema:"Fetch each colony's layout to report extractor expiry and stored output. Default false."`
}

type industryMiningIn struct {
	Character string `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
	Limit     int    `json:"limit,omitempty"     jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
}

func registerIndustry(s *mcp.Server) {
	addTool(s, &mcp.Tool{
		Name:        "eve_industry_jobs",
		Description: "Manufacturing, research, invention and reaction jobs with time remaining.\n\nJobs whose end time has passed show ready: true — they are finished but still need collecting in game.\n\nReturns: active_jobs, ready_to_deliver, jobs[] sorted by end time.",
	}, sessionTool(eveIndustryJobs))
	addTool(s, &mcp.Tool{
		Name:        "eve_industry_planets",
		Description: "Planetary interaction colonies: where they are and whether they have stalled.\n\nPass detail=true to get extractor_expires_in per colony — anything reading \"expired\" is currently earning nothing.\n\nReturns: colony_count, colonies[].",
	}, sessionTool(eveIndustryPlanets))
	addTool(s, &mcp.Tool{
		Name:        "eve_industry_mining",
		Description: "Mining ledger for the last ~30 days, aggregated by ore type and valued.\n\nValues use CCP's global average price. Returns: total_estimated_value, top_systems[], ores[] sorted by volume.",
	}, sessionTool(eveIndustryMining))
}

func eveIndustryJobs(ctx context.Context, a *session.Session, in industryJobsIn) (any, error) {
	token, err := a.ResolveCharacter(ctx, in.Character)
	if err != nil {
		return nil, wrap("eveIndustryJobs", err)
	}
	if err := a.RequireScope(token, "esi-industry.read_character_jobs.v1", "industry jobs"); err != nil {
		return nil, wrap("eveIndustryJobs", err)
	}
	cid := token.CharacterID
	result, err := a.ESI.Get(ctx, esiPath("characters", esiID(cid), "industry", "jobs"), &cid, map[string]any{"include_completed": boolDef(in.IncludeCompleted, false)}, nil)
	if err != nil {
		return nil, wrap("eveIndustryJobs", err)
	}

	return industryJobsResult(ctx, a, token.CharacterName, cid, result.Data, result.StaleNote(), limitOr(in.Limit, limitMedium), concise(in.ResponseFormat), false)
}

func eveIndustryPlanets(ctx context.Context, a *session.Session, in industryPlanetsIn) (any, error) {
	token, err := a.ResolveCharacter(ctx, in.Character)
	if err != nil {
		return nil, wrap("eveIndustryPlanets", err)
	}
	if err := a.RequireScope(token, "esi-planets.manage_planets.v1", "planetary colonies"); err != nil {
		return nil, wrap("eveIndustryPlanets", err)
	}
	cid := token.CharacterID
	result, err := a.ESI.Get(ctx, esiPath("characters", esiID(cid), "planets"), &cid, nil, nil)
	if err != nil {
		return nil, wrap("eveIndustryPlanets", err)
	}
	colonies := j.Maps(result.Data)
	if len(colonies) == 0 {
		return map[string]any{fCharacter: token.CharacterName, "colonies": []any{}, fNote: "No PI colonies."}, nil
	}
	idSet := map[int]struct{}{}
	for _, c := range colonies {
		idSet[j.Int(c["planet_id"])] = struct{}{}
		idSet[j.Int(c["solar_system_id"])] = struct{}{}
	}
	names, err := a.Resolver.Names(ctx, setToList(idSet), nil)
	if err != nil {
		return nil, wrap("eveIndustryPlanets", err)
	}
	var rows []map[string]any
	for _, c := range colonies {
		rows = append(rows, map[string]any{
			"planet": names[j.Int(c["planet_id"])], fSystem: names[j.Int(c["solar_system_id"])],
			fType: c["planet_type"], "upgrade_level": c["upgrade_level"],
			"pins": c["num_pins"], "planet_id": c["planet_id"],
		})
	}
	if boolDef(in.Detail, false) {
		decorateColonyDetails(ctx, a, cid, colonies, rows)
	}

	return map[string]any{
		fCharacter: token.CharacterName, "colony_count": len(rows),
		fDataAge: result.StaleNote(), "colonies": rows,
	}, nil
}

func eveIndustryMining(ctx context.Context, a *session.Session, in industryMiningIn) (any, error) {
	token, err := a.ResolveCharacter(ctx, in.Character)
	if err != nil {
		return nil, wrap("eveIndustryMining", err)
	}
	if err := a.RequireScope(token, "esi-industry.read_character_mining.v1", "the mining ledger"); err != nil {
		return nil, wrap("eveIndustryMining", err)
	}
	cid := token.CharacterID
	result, err := a.ESI.GetAllPages(ctx, esiPath("characters", esiID(cid), "mining"), &cid, nil, pagesESI)
	if err != nil {
		return nil, wrap("eveIndustryMining", err)
	}
	entries := j.Maps(result.Data)
	if len(entries) == 0 {
		return map[string]any{fCharacter: token.CharacterName, fOres: []any{}, fNote: "Nothing mined recently."}, nil
	}
	totals, bySystem := sumMining(entries)
	names, err := a.Resolver.Names(ctx, append(keys(totals), keys(bySystem)...), nil)
	if err != nil {
		return nil, wrap("eveIndustryMining", err)
	}
	prices, err := a.Resolver.ReferencePrices(ctx)
	if err != nil {
		return nil, wrap("eveIndustryMining", err)
	}
	rows, grand := miningOreRows(totals, names, prices)
	visible, meta := page(rows, limitOr(in.Limit, limitDefault), "")

	return merge(map[string]any{
		fCharacter: token.CharacterName, fPeriod: "last ~30 days",
		fTotalEstimatedValue: isk(grand), "top_systems": topMiningSystems(bySystem, names, limitTopItems),
		fDataAge: result.StaleNote(), fOres: visible,
	}, meta), nil
}

func sumMining(entries []map[string]any) (map[int]int, map[int]int) {
	totals := map[int]int{}
	bySystem := map[int]int{}
	for _, e := range entries {
		totals[j.Int(e[fTypeID])] += j.Int(e[fQuantity])
		bySystem[j.Int(e["solar_system_id"])] += j.Int(e[fQuantity])
	}

	return totals, bySystem
}

func miningOreRows(totals map[int]int, names map[int]string, prices map[int]map[string]float64) ([]map[string]any, float64) {
	rows := make([]map[string]any, 0, len(totals))
	grand := 0.0
	for tid, qty := range totals {
		value := unitPrice(prices, tid) * float64(qty)
		grand += value
		rows = append(rows, map[string]any{"ore": nameOr(names, tid), fUnits: qty, fEstimatedValue: isk(value)})
	}
	sort.Slice(rows, func(i, k int) bool { return j.Int(rows[i][fUnits]) > j.Int(rows[k][fUnits]) })

	return rows, grand
}

func topMiningSystems(bySystem map[int]int, names map[int]string, n int) []map[string]any {
	type kv struct{ id, q int }
	var sys []kv
	for id, q := range bySystem {
		sys = append(sys, kv{id, q})
	}
	sort.Slice(sys, func(i, k int) bool { return sys[i].q > sys[k].q })
	if len(sys) > n {
		sys = sys[:n]
	}
	top := make([]map[string]any, 0, len(sys))
	for _, s := range sys {
		top = append(top, map[string]any{fSystem: nameOr(names, s.id), fUnits: s.q})
	}

	return top
}

func industryJobsResult(ctx context.Context, a *session.Session, character string, cid int, data any, stale string, limit int, conciseMode, withInstaller bool) (map[string]any, error) {
	jobs := j.Maps(data)
	if len(jobs) == 0 {
		return map[string]any{
			fCharacter: character, fJobs: []any{},
			fNote: "No industry jobs. Pass include_completed=true to see finished ones.",
		}, nil
	}
	idSet := map[int]struct{}{}
	placeSet := map[int]struct{}{}
	people := map[int]struct{}{}
	for _, job := range jobs {
		idSet[j.Int(job["blueprint_type_id"])] = struct{}{}
		if j.Int(job["product_type_id"]) != 0 {
			idSet[j.Int(job["product_type_id"])] = struct{}{}
		}
		loc := j.Int(job["station_id"])
		if loc == 0 {
			loc = j.Int(job["output_location_id"])
		}
		if loc != 0 {
			placeSet[loc] = struct{}{}
		}
		if withInstaller && j.Int(job["installer_id"]) != 0 {
			people[j.Int(job["installer_id"])] = struct{}{}
		}
	}
	names, err := a.Resolver.Names(ctx, append(setToList(idSet), setToList(people)...), nil)
	if err != nil {
		return nil, wrap("industryJobsResult", err)
	}
	places, err := a.Resolver.Names(ctx, setToList(placeSet), &cid)
	if err != nil {
		return nil, wrap("industryJobsResult", err)
	}
	now := time.Now().UTC()
	var rows []map[string]any
	for _, job := range jobs {
		end := parseTime(j.Str(job["end_date"]))
		ready := end != nil && !end.After(now)
		ends := vUnknown
		if ready {
			ends = "ready to deliver"
		} else if end != nil {
			ends = humanDelta(end.Sub(now))
		}
		product := names[j.Int(job["product_type_id"])]
		if product == "" {
			product = names[j.Int(job["blueprint_type_id"])]
		}
		loc := j.Int(job["station_id"])
		if loc == 0 {
			loc = j.Int(job["output_location_id"])
		}
		row := map[string]any{
			"activity": activityName(j.Int(job["activity_id"])), "product": product,
			"runs": job["runs"], "ends_in": ends, fLocation: places[loc],
			"ready": ready, fStatus: job[fStatus],
			fBlueprint:     names[j.Int(job["blueprint_type_id"])],
			"install_cost": isk(job["cost"]), "end_date": job["end_date"],
		}
		if withInstaller {
			row["installer"] = names[j.Int(job["installer_id"])]
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, k int) bool { return j.Str(rows[i]["end_date"]) < j.Str(rows[k]["end_date"]) })
	visible, meta := page(rows, limit, "")
	active, readyN := 0, 0
	for _, r := range rows {
		if j.Bool(r["ready"]) {
			readyN++
		} else {
			active++
		}
	}
	keep := []string{"activity", "product", "runs", "ends_in", fLocation}
	if withInstaller {
		keep = append(keep, "installer")
	}

	return merge(map[string]any{
		fCharacter: character, "active_jobs": active, "ready_to_deliver": readyN,
		fDataAge: stale, fJobs: project(visible, keep, conciseMode),
	}, meta), nil
}

func decorateColonyDetails(ctx context.Context, a *session.Session, cid int, colonies, rows []map[string]any) {
	now := time.Now().UTC()
	for i, c := range colonies {
		decorateColonyDetail(ctx, a, cid, c, rows[i], now)
	}
}

func decorateColonyDetail(ctx context.Context, a *session.Session, cid int, colony, row map[string]any, now time.Time) {
	layout, err := a.ESI.Get(ctx, esiPath("characters", esiID(cid), "planets", esiID(j.Int(colony["planet_id"]))), &cid, nil, nil)
	if err != nil {
		return
	}
	pins := j.Maps(j.Map(layout.Data)["pins"])
	if expiry := colonyExtractorExpiry(pins, now); expiry != "" {
		row["extractor_expires_in"] = expiry
	}
	stored, err := colonyStored(ctx, a, pins)
	if err != nil {
		return
	}
	if len(stored) > 0 {
		row["stored"] = stored
	}
}

func colonyExtractorExpiry(pins []map[string]any, now time.Time) string {
	var soonest *time.Time
	for _, p := range pins {
		t := parseTime(j.Str(p["expiry_time"]))
		if t == nil {
			continue
		}
		if soonest == nil || t.Before(*soonest) {
			soonest = t
		}
	}
	if soonest == nil {
		return ""
	}
	if !soonest.After(now) {
		return "EXPIRED — producing nothing"
	}

	return humanDelta(soonest.Sub(now))
}

func colonyStored(ctx context.Context, a *session.Session, pins []map[string]any) (map[string]int, error) {
	stored := map[int]int{}
	for _, p := range pins {
		for _, content := range j.Maps(p["contents"]) {
			stored[j.Int(content[fTypeID])] += j.Int(content["amount"])
		}
	}
	if len(stored) == 0 {
		return map[string]int{}, nil
	}
	pn, err := a.Resolver.Names(ctx, keys(stored), nil)
	if err != nil {
		return nil, wrap("colonyStored", err)
	}
	type kv struct {
		n string
		q int
	}
	list := make([]kv, 0, len(stored))
	for t, q := range stored {
		list = append(list, kv{pn[t], q})
	}
	sort.Slice(list, func(i, k int) bool { return list[i].q > list[k].q })
	if len(list) > colonyStoredTop {
		list = list[:colonyStoredTop]
	}
	out := map[string]int{}
	for _, x := range list {
		out[x.n] = x.q
	}

	return out, nil
}

func activityName(id int) string {
	switch id {
	case activityManufacturing:
		return "Manufacturing"
	case activityResearchTE:
		return "Researching Time Efficiency"
	case activityResearchME:
		return "Researching Material Efficiency"
	case activityCopying:
		return "Copying"
	case activityReverseEng:
		return "Reverse Engineering"
	case activityInvention:
		return "Invention"
	case activityReactionA, activityReactionB:
		return "Reactions"
	default:
		return fmt.Sprintf("#%d", id)
	}
}
