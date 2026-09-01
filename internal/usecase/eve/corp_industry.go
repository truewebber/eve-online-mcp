package eve

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/domain/character"
	"github.com/truewebber/eve-online-mcp/internal/j"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"
)

func eveCorpIndustryJobs(ctx context.Context, a *session.Session, in corpIndustryJobsIn) (any, error) {
	if err := rejectUnknownFormat(in.ResponseFormat); err != nil {
		return nil, err
	}
	corp, err := openCorp(ctx, a, fJobs, fJobs, "corporation industry jobs")
	if err != nil {
		return nil, err
	}
	result, err := a.ESI.Get(ctx, esiPath("corporations", esiID(corp.CorporationID), "industry", "jobs"), &corp.Token.CharacterID, esiPageQuery(in.Page, map[string]any{"include_completed": boolDef(in.IncludeCompleted, false)}), nil)
	if err != nil {
		return nil, wrap("eveCorpIndustryJobs", err)
	}
	out, err := industryJobsResult(ctx, a, jobsView{
		character: corp.CharacterName(), cid: corp.Token.CharacterID, data: result.Data, stale: result.StaleNote(),
		limit: limitOr(in.Limit, limitMedium), conciseMode: concise(in.ResponseFormat), withInstaller: true,
	})
	if err != nil {
		return nil, err
	}

	return merge(who(corp), merge(out, map[string]any{fPage: pageOr(in.Page), fTotalPages: result.PageCount()})), nil
}

func eveCorpMining(ctx context.Context, a *session.Session, in corpMiningIn) (any, error) {
	if err := rejectUnknownFormat(in.ResponseFormat); err != nil {
		return nil, err
	}
	corp, err := a.ResolveCorporation(ctx)
	if err != nil {
		return nil, wrap("eveCorpMining", err)
	}
	if err := a.RequirePlayerCorp(corp); err != nil {
		return nil, wrap("eveCorpMining", err)
	}
	if err := a.RequireGranted(corp.CharacterName(), corp.Token.Scopes, corpScope("mining"), "the corporation mining ledger"); err != nil {
		return nil, wrap("eveCorpMining", err)
	}
	canLedger := corp.HasRole(corpRole("mining_ledger")...)
	canExtract := corp.HasRole(corpRole("mining_extractions")...)
	if err := corpMiningRequireRole(a, corp, canLedger, canExtract); err != nil {
		return nil, err
	}
	out := merge(who(corp), map[string]any{fPeriod: "last ~30 days"})
	corpMiningAttachExtractions(ctx, a, corp, canExtract, out)
	corpMiningAttachLedger(ctx, a, corpMiningAttach{corp: corp, in: in, canLedger: canLedger, out: out})

	return out, nil
}

func corpMiningRequireRole(a *session.Session, corp *character.Corporation, canLedger, canExtract bool) error {
	if canLedger || canExtract {
		return nil
	}

	return wrap("corpMiningRequireRole", a.RequireCorpRole(corp, []string{roleAccountant, roleStationManager}, "corporation mining (ledger needs Accountant, extractions need Station_Manager)"))
}

func corpMiningAttachExtractions(ctx context.Context, a *session.Session, corp *character.Corporation, canExtract bool, out map[string]any) {
	if !canExtract {
		out["extractions_note"] = "Extraction timers need Station_Manager (or Director) granted everywhere."

		return
	}
	ex, err := corpExtractions(ctx, a, corp)
	if err != nil {
		out["extractions_note"] = sectionNote(a, "corp extractions", err)

		return
	}
	out["extractions"] = ex
}

type corpMiningAttach struct {
	corp      *character.Corporation
	in        corpMiningIn
	canLedger bool
	out       map[string]any
}

func corpMiningAttachLedger(ctx context.Context, a *session.Session, in corpMiningAttach) {
	if !in.canLedger {
		in.out["ledger_note"] = "The observer ledger needs Accountant (or Director) granted everywhere."

		return
	}
	ledger, err := corpMiningLedger(ctx, a, corpLedgerIn{
		corp: in.corp, offset: in.in.Offset, limit: limitOr(in.in.Limit, limitDefault),
		conciseMode: concise(in.in.ResponseFormat),
	})
	if err != nil {
		in.out["ledger_note"] = sectionNote(a, "corp ledger", err)

		return
	}
	merge(in.out, ledger)
}

func corpExtractions(ctx context.Context, a *session.Session, corp *character.Corporation) ([]map[string]any, error) {
	result, err := a.ESI.Get(ctx, esiPath("corporation", esiID(corp.CorporationID), "mining", "extractions"), &corp.Token.CharacterID, nil, nil)
	if err != nil {
		return nil, wrap("corpExtractions", err)
	}
	rows := j.Maps(result.Data)
	if len(rows) == 0 {
		return []map[string]any{}, nil
	}
	idSet := map[int]struct{}{}
	for _, e := range rows {
		if j.Int(e["structure_id"]) != 0 {
			idSet[j.Int(e["structure_id"])] = struct{}{}
		}
		if j.Int(e["moon_id"]) != 0 {
			idSet[j.Int(e["moon_id"])] = struct{}{}
		}
	}
	names, err := a.Resolver.Names(ctx, setToList(idSet), &corp.Token.CharacterID)
	if err != nil {
		return nil, wrap("corpExtractions", err)
	}
	now := time.Now().UTC()
	var out []map[string]any
	for _, e := range rows {
		out = append(out, corpExtractionRow(e, names, now))
	}
	sort.Slice(out, func(i, k int) bool { return j.Str(out[i]["chunk_arrival_time"]) < j.Str(out[k]["chunk_arrival_time"]) })

	return out, nil
}

func corpExtractionRow(e map[string]any, names map[int]string, now time.Time) map[string]any {
	arrival, decay := parseTime(j.Str(e["chunk_arrival_time"])), parseTime(j.Str(e["natural_decay_time"]))

	return map[string]any{
		fStructure: names[j.Int(e["structure_id"])], "moon": names[j.Int(e["moon_id"])],
		"chunk_arrives_in": extractionDelta(arrival, now, "arrived"), "decays_in": extractionDelta(decay, now, "decayed"),
		"chunk_arrival_time": e["chunk_arrival_time"], "natural_decay_time": e["natural_decay_time"],
	}
}

func extractionDelta(t *time.Time, now time.Time, past string) string {
	if t == nil {
		return vUnknown
	}
	if !t.After(now) {
		return past
	}

	return humanDelta(t.Sub(now))
}

type miningLedgerAgg struct {
	totals, byMiner, byObserver map[int]int
	oldest                      float64
	failed                      int
	truncated                   bool
}

type corpLedgerIn struct {
	corp          *character.Corporation
	offset, limit int
	conciseMode   bool
}

func corpMiningLedger(ctx context.Context, a *session.Session, in corpLedgerIn) (map[string]any, error) {
	observersRes, err := a.ESI.GetAllPages(ctx, esiPath("corporation", esiID(in.corp.CorporationID), "mining", "observers"), &in.corp.Token.CharacterID, nil, pagesESI)
	if err != nil {
		return nil, wrap("corpMiningLedger", err)
	}
	observers := j.Maps(observersRes.Data)
	if len(observers) == 0 {
		return map[string]any{fOres: []any{}, fNote: "No mining observers with recorded events (idle refineries are hidden).", fDataAge: observersRes.StaleNote()}, nil
	}
	agg := fetchCorpMiningObservers(ctx, a, in.corp, observers, observersRes)
	names, err := a.Resolver.Names(ctx, setToList(miningAggIDs(agg)), &in.corp.Token.CharacterID)
	if err != nil {
		return nil, wrap("corpMiningLedger", err)
	}
	prices, err := a.Resolver.ReferencePrices(ctx)
	if err != nil {
		return nil, wrap("corpMiningLedger", err)
	}
	ores := miningOreRows(agg.totals, names, prices)
	paged := pageByOffset(ores.rows, in.offset, in.limit, "")
	out := merge(map[string]any{
		fTotalEstimatedValue: isk(ores.grand), "observer_count": len(observers),
		"top_miners": topN(agg.byMiner, names, "miner"), "top_observers": topN(agg.byObserver, names, "observer"),
		fValuationBasis: valuationCCPAvg,
		fDataAge:        miningLedgerAge(observersRes, agg.oldest), fOres: paged.Rows,
	}, paged.fields)
	if agg.failed > 0 {
		out["unavailable_observers"] = agg.failed
	}
	if agg.truncated {
		out["totals_caveat"] = "Ledger walk was capped (25 observers, 10 pages each); totals may be short."
	}

	return out, nil
}

func miningLedgerAge(observersRes esi.Result, oldest float64) string {
	if oldest < float64(secondsPerMinute) {
		return fmt.Sprintf("%ds old", int(oldest))
	}

	return observersRes.StaleNote()
}

func miningAggIDs(agg miningLedgerAgg) map[int]struct{} {
	idSet := map[int]struct{}{}
	for k := range agg.totals {
		idSet[k] = struct{}{}
	}
	for k := range agg.byMiner {
		idSet[k] = struct{}{}
	}
	for k := range agg.byObserver {
		idSet[k] = struct{}{}
	}

	return idSet
}

func fetchCorpMiningObservers(ctx context.Context, a *session.Session, corp *character.Corporation, observers []map[string]any, observersRes esi.Result) miningLedgerAgg {
	capped := observers
	if len(capped) > miningObserverCap {
		capped = capped[:miningObserverCap]
	}
	ch := miningObserverPages(ctx, a, corp, capped)
	agg := miningLedgerAgg{
		totals: map[int]int{}, byMiner: map[int]int{}, byObserver: map[int]int{},
		oldest: observersRes.AgeSeconds, truncated: observersRes.Truncated || len(observers) > miningObserverCap,
	}
	for range capped {
		absorbMiningObserver(a, &agg, <-ch)
	}

	return agg
}

type miningObsBox struct {
	obs map[string]any
	r   esi.Result
	err error
}

func miningObserverPages(ctx context.Context, a *session.Session, corp *character.Corporation, capped []map[string]any) <-chan miningObsBox {
	ch := make(chan miningObsBox, len(capped))
	for _, obs := range capped {
		go func(obs map[string]any) {
			if j.Int(obs["observer_id"]) == 0 {
				ch <- miningObsBox{obs, esi.Result{}, nil}

				return
			}
			r, err := a.ESI.GetAllPages(ctx, esiPath("corporation", esiID(corp.CorporationID), "mining", "observers", esiID(j.Int(obs["observer_id"]))), &corp.Token.CharacterID, nil, pagesShort)
			ch <- miningObsBox{obs, r, err}
		}(obs)
	}

	return ch
}

func absorbMiningObserver(a *session.Session, agg *miningLedgerAgg, b miningObsBox) {
	if b.err != nil {
		agg.failed++
		a.Logger.Error("eve: mining observer", "observer_id", b.obs["observer_id"], "err", b.err)

		return
	}
	if b.r.AgeSeconds > agg.oldest {
		agg.oldest = b.r.AgeSeconds
	}
	agg.truncated = agg.truncated || b.r.Truncated
	oid := j.Int(b.obs["observer_id"])
	for _, entry := range j.Maps(b.r.Data) {
		qty := j.Int(entry[fQuantity])
		agg.totals[j.Int(entry[fTypeID])] += qty
		if j.Int(entry[fCharacterID]) != 0 {
			agg.byMiner[j.Int(entry[fCharacterID])] += qty
		}
		if oid != 0 {
			agg.byObserver[oid] += qty
		}
	}
}

func topN(m map[int]int, names map[int]string, label string) []map[string]any {
	type kv struct{ id, q int }
	var list []kv
	for id, q := range m {
		list = append(list, kv{id, q})
	}
	sort.Slice(list, func(i, k int) bool { return list[i].q > list[k].q })
	if len(list) > limitTopItems {
		list = list[:limitTopItems]
	}
	out := make([]map[string]any, 0, len(list))
	for _, x := range list {
		out = append(out, map[string]any{label: nameOr(names, x.id), fUnits: x.q})
	}

	return out
}

func keepEmpty(m map[string]any, keep ...string) map[string]any {
	keepSet := map[string]struct{}{}
	for _, k := range keep {
		keepSet[k] = struct{}{}
	}
	out := map[string]any{}
	for k, v := range m {
		if emptyVal(v) {
			if _, ok := keepSet[k]; !ok {
				continue
			}
		}
		out[k] = v
	}

	return out
}
