package eve

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/j"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type industryJobsIn struct {
	IncludeCompleted *bool  `json:"include_completed,omitempty" jsonschema:"Also return jobs that already delivered. Default false."`
	Limit            int    `json:"limit,omitempty"             jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	ResponseFormat   string `json:"response_format,omitempty"   jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

type industryPlanetsIn struct {
	Detail *bool `json:"detail,omitempty" jsonschema:"Fetch each colony's layout to report extractor expiry and stored output. Default false."`
}

type industryMiningIn struct {
	Limit  int `json:"limit,omitempty"  jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	Offset int `json:"offset,omitempty" jsonschema:"Skip this many rows of the result before returning any. The result carries the total, so this is how you continue a long list."`
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
	if err := rejectUnknownFormat(in.ResponseFormat); err != nil {
		return nil, err
	}
	token, err := a.Character(ctx)
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

	return industryJobsResult(ctx, a, jobsView{
		character: token.CharacterName, cid: cid, data: result.Data, stale: result.StaleNote(),
		limit: limitOr(in.Limit, limitMedium), conciseMode: concise(in.ResponseFormat),
	})
}

func eveIndustryPlanets(ctx context.Context, a *session.Session, in industryPlanetsIn) (any, error) {
	token, err := a.Character(ctx)
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
		return map[string]any{fCharacter: token.CharacterName, "colonies": []any{}, fNote: "No PI colonies.", fDataAge: result.StaleNote()}, nil
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
	ages := []float64{result.AgeSeconds}
	if boolDef(in.Detail, false) {
		ages = append(ages, decorateColonyDetails(ctx, a, cid, colonies, rows)...)
	}

	return map[string]any{
		fCharacter: token.CharacterName, "colony_count": len(rows),
		fDataAge: staleNote(ages...), "colonies": rows,
	}, nil
}

func eveIndustryMining(ctx context.Context, a *session.Session, in industryMiningIn) (any, error) {
	token, err := a.Character(ctx)
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
		return map[string]any{fCharacter: token.CharacterName, fOres: []any{}, fNote: "Nothing mined recently.", fDataAge: result.StaleNote()}, nil
	}
	sums := sumMining(entries)
	names, err := a.Resolver.Names(ctx, append(keys(sums.totals), keys(sums.bySystem)...), nil)
	if err != nil {
		return nil, wrap("eveIndustryMining", err)
	}
	prices, err := a.Resolver.ReferencePrices(ctx)
	if err != nil {
		return nil, wrap("eveIndustryMining", err)
	}
	ores := miningOreRows(sums.totals, names, prices)
	rows, grand := ores.rows, ores.grand
	paged := pageByOffset(rows, in.Offset, limitOr(in.Limit, limitDefault), "")

	return merge(map[string]any{
		fCharacter: token.CharacterName, fPeriod: "last ~30 days",
		fTotalEstimatedValue: isk(grand), "top_systems": topMiningSystems(sums.bySystem, names, limitTopItems),
		fDataAge: result.StaleNote(), fOres: paged.Rows,
	}, paged.fields), nil
}

type miningSums struct {
	totals, bySystem map[int]int
}

func sumMining(entries []map[string]any) miningSums {
	out := miningSums{totals: map[int]int{}, bySystem: map[int]int{}}
	for _, e := range entries {
		out.totals[j.Int(e[fTypeID])] += j.Int(e[fQuantity])
		out.bySystem[j.Int(e["solar_system_id"])] += j.Int(e[fQuantity])
	}

	return out
}

type miningOreView struct {
	rows  []map[string]any
	grand float64
}

func miningOreRows(totals map[int]int, names map[int]string, prices map[int]map[string]float64) miningOreView {
	rows := make([]map[string]any, 0, len(totals))
	grand := 0.0
	for tid, qty := range totals {
		value := unitPrice(prices, tid) * float64(qty)
		grand += value
		rows = append(rows, map[string]any{"ore": nameOr(names, tid), fUnits: qty, fEstimatedValue: isk(value)})
	}
	sort.Slice(rows, func(i, k int) bool { return j.Int(rows[i][fUnits]) > j.Int(rows[k][fUnits]) })

	return miningOreView{rows: rows, grand: grand}
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

type jobsView struct {
	character                  string
	cid                        int
	data                       any
	stale                      string
	limit                      int
	conciseMode, withInstaller bool
}

func industryJobsResult(ctx context.Context, a *session.Session, in jobsView) (map[string]any, error) {
	jobs := j.Maps(in.data)
	if len(jobs) == 0 {
		return map[string]any{
			fCharacter: in.character, fJobs: []any{},
			fNote:    "No industry jobs. Pass include_completed=true to see finished ones.",
			fDataAge: in.stale,
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
		if in.withInstaller && j.Int(job["installer_id"]) != 0 {
			people[j.Int(job["installer_id"])] = struct{}{}
		}
	}
	names, err := a.Resolver.Names(ctx, append(setToList(idSet), setToList(people)...), nil)
	if err != nil {
		return nil, wrap("industryJobsResult", err)
	}
	places, err := a.Resolver.Names(ctx, setToList(placeSet), &in.cid)
	if err != nil {
		return nil, wrap("industryJobsResult", err)
	}
	rows := industryJobRows(jobs, names, places, in.withInstaller)
	paged := applyLimit(rows, in.limit, "")
	counts := industryJobCounts(rows)
	keep := []string{"activity", "product", "runs", "ends_in", fLocation}
	if in.withInstaller {
		keep = append(keep, "installer")
	}

	return merge(map[string]any{
		fCharacter: in.character, "active_jobs": counts.active, "ready_to_deliver": counts.ready,
		fDataAge: in.stale, fJobs: project(paged.Rows, keep, in.conciseMode),
	}, paged.fields), nil
}

func industryJobRows(jobs []map[string]any, names, places map[int]string, withInstaller bool) []map[string]any {
	now := time.Now().UTC()
	rows := make([]map[string]any, 0, len(jobs))
	for _, job := range jobs {
		rows = append(rows, industryJobRow(job, names, places, withInstaller, now))
	}
	sort.Slice(rows, func(i, k int) bool { return j.Str(rows[i]["end_date"]) < j.Str(rows[k]["end_date"]) })

	return rows
}

func industryJobRow(job map[string]any, names, places map[int]string, withInstaller bool, now time.Time) map[string]any {
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

	return row
}

type jobCounts struct {
	active, ready int
}

func industryJobCounts(rows []map[string]any) jobCounts {
	var out jobCounts
	for _, r := range rows {
		if j.Bool(r["ready"]) {
			out.ready++
		} else {
			out.active++
		}
	}

	return out
}

func decorateColonyDetails(ctx context.Context, a *session.Session, cid int, colonies, rows []map[string]any) []float64 {
	now := time.Now().UTC()
	var ages []float64
	for i, c := range colonies {
		if age, ok := decorateColonyDetail(ctx, a, colonyDecorate{cid: cid, colony: c, row: rows[i], now: now}); ok {
			ages = append(ages, age)
		}
	}

	return ages
}

type colonyDecorate struct {
	cid         int
	colony, row map[string]any
	now         time.Time
}

func decorateColonyDetail(ctx context.Context, a *session.Session, in colonyDecorate) (float64, bool) {
	layout, err := a.ESI.Get(ctx, esiPath("characters", esiID(in.cid), "planets", esiID(j.Int(in.colony["planet_id"]))), &in.cid, nil, nil)
	if err != nil {
		return 0, false
	}
	pins := j.Maps(j.Map(layout.Data)["pins"])
	if expiry := colonyExtractorExpiry(pins, in.now); expiry != "" {
		in.row["extractor_expires_in"] = expiry
	}
	stored, err := colonyStored(ctx, a, pins)
	if err != nil {
		return layout.AgeSeconds, true
	}
	if len(stored) > 0 {
		in.row["stored"] = stored
	}

	return layout.AgeSeconds, true
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
