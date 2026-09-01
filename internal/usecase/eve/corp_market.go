package eve

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/j"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"
)

func eveCorpOrders(ctx context.Context, a *session.Session, in corpOrdersIn) (any, error) {
	if err := rejectUnknownFormat(in.ResponseFormat); err != nil {
		return nil, err
	}
	corp, err := openCorp(ctx, a, fOrders, fOrders, "corporation market orders")
	if err != nil {
		return nil, err
	}
	result, err := a.ESI.Get(ctx, esiPath("corporations", esiID(corp.CorporationID), "orders"), &corp.Token.CharacterID, esiPageQuery(in.Page, nil), nil)
	if err != nil {
		return nil, wrap("eveCorpOrders", err)
	}
	divs := corpDivisions(ctx, a, corp)
	out, err := formatOrders(ctx, a, orderView{
		character: corp.CharacterName(), cid: corp.Token.CharacterID, data: result.Data, stale: result.StaleNote(),
		limit: limitOr(in.Limit, limitLong), conciseMode: concise(in.ResponseFormat), walletNames: divs[fWallet],
	})
	if err != nil {
		return nil, err
	}

	return merge(who(corp), merge(out, map[string]any{fPage: pageOr(in.Page), fTotalPages: result.PageCount()})), nil
}

func eveCorpContracts(ctx context.Context, a *session.Session, in corpContractsIn) (any, error) {
	if err := rejectUnknownFormat(in.ResponseFormat); err != nil {
		return nil, err
	}
	corp, err := openCorp(ctx, a, fContracts, "", "corporation contracts")
	if err != nil {
		return nil, err
	}
	result, err := a.ESI.Get(ctx, esiPath("corporations", esiID(corp.CorporationID), "contracts"), &corp.Token.CharacterID, esiPageQuery(in.Page, nil), nil)
	if err != nil {
		return nil, wrap("eveCorpContracts", err)
	}
	out, err := formatContracts(ctx, a, contractView{
		character: corp.CharacterName(), cid: corp.Token.CharacterID, data: result.Data, stale: result.StaleNote(),
		outstandingOnly: boolDef(in.OutstandingOnly, true), page: in.Page, totalPages: result.PageCount(),
		limit: limitOr(in.Limit, limitDefault), conciseMode: concise(in.ResponseFormat), corp: true,
	})
	if err != nil {
		return nil, err
	}

	return merge(who(corp), out), nil
}

func eveCorpKillmails(ctx context.Context, a *session.Session, in corpKillmailsIn) (any, error) {
	if err := rejectUnknownFormat(in.ResponseFormat); err != nil {
		return nil, err
	}
	corp, err := openCorp(ctx, a, fKillmails, fKillmails, "corporation killmails")
	if err != nil {
		return nil, err
	}
	out, err := formatKillmails(ctx, a, killmailView{
		character: corp.CharacterName(), characterID: corp.Token.CharacterID, corpID: corp.CorporationID,
		path: esiPath("corporations", esiID(corp.CorporationID), "killmails", "recent"),
		page: in.Page, limit: limitOr(in.Limit, limitKillmails), conciseMode: concise(in.ResponseFormat),
	})
	if err != nil {
		return nil, err
	}

	return merge(who(corp), j.Map(out)), nil
}

func eveCorpStructures(ctx context.Context, a *session.Session, in corpStructuresIn) (any, error) {
	if err := rejectUnknownFormat(in.ResponseFormat); err != nil {
		return nil, err
	}
	corp, err := openCorp(ctx, a, fStructures, fStructures, "corporation structures")
	if err != nil {
		return nil, err
	}
	result, err := a.ESI.Get(ctx, esiPath("corporations", esiID(corp.CorporationID), "structures"), &corp.Token.CharacterID, esiPageQuery(in.Page, nil), nil)
	if err != nil {
		return nil, wrap("eveCorpStructures", err)
	}
	structures := j.Maps(result.Data)
	if len(structures) == 0 {
		return merge(who(corp), merge(map[string]any{fStructures: []any{}, fNote: "This corporation owns no Upwell structures."}, pageByNumber(nil, in.Page, result.PageCount(), limitOr(in.Limit, limitDefault)).fields)), nil
	}
	names, err := a.Resolver.Names(ctx, corpStructureIDs(structures), &corp.Token.CharacterID)
	if err != nil {
		return nil, wrap("eveCorpStructures", err)
	}
	listed := corpStructureRows(structures, names)
	sort.Slice(listed.rows, func(i, k int) bool {
		return j.Str(listed.rows[i]["fuel_expires"]) < j.Str(listed.rows[k]["fuel_expires"])
	})
	paged := pageByNumber(listed.rows, in.Page, result.PageCount(), limitOr(in.Limit, limitDefault))

	return merge(who(corp), merge(map[string]any{
		"structure_count": len(listed.rows), "unfuelled": listed.unfuelled, fDataAge: result.StaleNote(),
		fStructures: project(paged.Rows, []string{fStructure, fType, fSystem, fState, "fuel_expires_in"}, concise(in.ResponseFormat)),
	}, paged.fields)), nil
}

func corpStructureIDs(structures []map[string]any) []int {
	idSet := map[int]struct{}{}
	for _, s := range structures {
		for _, k := range []string{fTypeID, "system_id", "structure_id"} {
			if j.Int(s[k]) != 0 {
				idSet[j.Int(s[k])] = struct{}{}
			}
		}
	}

	return setToList(idSet)
}

type corpStructureList struct {
	rows      []map[string]any
	unfuelled int
}

func corpStructureRows(structures []map[string]any, names map[int]string) corpStructureList {
	now := time.Now().UTC()
	rows := make([]map[string]any, 0, len(structures))
	unfuelled := 0
	for _, s := range structures {
		expires, dead := structureFuelExpires(parseTime(j.Str(s["fuel_expires"])), now)
		if dead {
			unfuelled++
		}
		rows = append(rows, map[string]any{
			fStructure: names[j.Int(s["structure_id"])], fType: names[j.Int(s[fTypeID])],
			fSystem: names[j.Int(s["system_id"])], fState: s[fState],
			"fuel_expires_in": expires, "fuel_expires": s["fuel_expires"],
			"reinforce_hour": s["reinforce_hour"], "services": structureServices(s), "structure_id": s["structure_id"],
		})
	}

	return corpStructureList{rows, unfuelled}
}

func structureFuelExpires(fuel *time.Time, now time.Time) (string, bool) {
	if fuel != nil && !fuel.After(now) {
		return "UNFUELLED", true
	}
	if fuel != nil {
		return humanDelta(fuel.Sub(now)), false
	}

	return vUnknown, false
}

func structureServices(s map[string]any) any {
	listed := j.Maps(s["services"])
	services := make([]string, 0, len(listed))
	for _, svc := range listed {
		services = append(services, fmt.Sprintf("%v (%v)", svc[fName], svc[fState]))
	}
	if len(services) == 0 {
		return nil
	}

	return services
}
