package eve

import (
	"context"
	"fmt"
	"sort"

	"github.com/truewebber/eve-online-mcp/internal/j"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"
)

type kmIn struct {
	Page           int    `json:"page,omitempty"            jsonschema:"Which page of results to fetch, starting at 1. The result says which page it is and how many exist. Only reach for page 2 if the user asked for more than page 1 showed."`
	Limit          int    `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

func eveSocialKillmails(ctx context.Context, a *session.Session, in kmIn) (any, error) {
	if err := rejectUnknownFormat(in.ResponseFormat); err != nil {
		return nil, err
	}
	token, err := a.Character(ctx)
	if err != nil {
		return nil, wrap("eveSocialKillmails", err)
	}
	if err := a.RequireScope(token, "esi-killmails.read_killmails.v1", fKillmails); err != nil {
		return nil, wrap("eveSocialKillmails", err)
	}

	return formatKillmails(ctx, a, killmailView{
		character: token.CharacterName, characterID: token.CharacterID,
		path: esiPath("characters", esiID(token.CharacterID), "killmails", "recent"),
		page: in.Page, limit: limitOr(in.Limit, limitKillmails), conciseMode: concise(in.ResponseFormat),
	})
}

type killmailFetch struct {
	kills  []map[string]any
	failed []any
}

type killmailSummary struct {
	rows   []map[string]any
	kills  int
	losses int
}

type killmailView struct {
	character           string
	characterID, corpID int
	path                string
	page, limit         int
	conciseMode         bool
}

func formatKillmails(ctx context.Context, a *session.Session, in killmailView) (any, error) {
	var cid *int
	if in.characterID != 0 {
		cid = &in.characterID
	}
	result, err := a.ESI.Get(ctx, in.path, cid, esiPageQuery(in.page, nil), nil)
	if err != nil {
		return nil, wrap("formatKillmails", err)
	}
	refs := j.Maps(result.Data)
	if len(refs) == 0 {
		return merge(map[string]any{fCharacter: in.character, fKillmails: []any{}, fNote: "Nothing recent.", fDataAge: result.StaleNote()}, pageByNumber(nil, in.page, result.PageCount(), in.limit).fields), nil
	}
	fetched := fetchKillmailBodies(ctx, a, refs)
	built, err := buildKillmailRows(ctx, a, fetched.kills, in.characterID, in.corpID)
	if err != nil {
		return nil, err
	}
	sort.Slice(built.rows, func(i, k int) bool { return j.Str(built.rows[i]["time"]) > j.Str(built.rows[k]["time"]) })
	paged := pageByNumber(built.rows, in.page, result.PageCount(), in.limit)
	out := merge(map[string]any{
		fCharacter: in.character, "kills": built.kills, "losses": built.losses,
		"hull_value_caveat": "Hull only — fitted modules and cargo are not included.",
		fKillmails:          project(paged.Rows, []string{"role", "time", fSystem, "victim", "ship_lost"}, in.conciseMode),
	}, paged.fields)
	if len(fetched.failed) > 0 {
		out["unavailable"] = len(fetched.failed)
		out["unavailable_note"] = fmt.Sprintf("%d of %d killmails could not be fetched from ESI, so kills/losses below undercount by that many. Try again shortly.", len(fetched.failed), len(refs))
	}

	return out, nil
}

func fetchKillmailBodies(ctx context.Context, a *session.Session, refs []map[string]any) killmailFetch {
	type box struct {
		id   any
		data map[string]any
		err  error
	}
	ch := make(chan box, len(refs))
	for _, ref := range refs {
		go func(ref map[string]any) {
			r, err := a.ESI.Get(ctx, esiPath("killmails", esiID(j.Int(ref["killmail_id"])), j.Str(ref["killmail_hash"])), nil, nil, nil)
			if err != nil {
				ch <- box{ref["killmail_id"], nil, err}

				return
			}
			ch <- box{ref["killmail_id"], j.Map(r.Data), nil}
		}(ref)
	}
	var kills []map[string]any
	var failed []any
	for range refs {
		b := <-ch
		if b.err != nil {
			failed = append(failed, b.id)
			a.Logger.Error("eve: killmail fetch", "id", b.id, "err", b.err)
		} else {
			kills = append(kills, b.data)
		}
	}

	return killmailFetch{kills: kills, failed: failed}
}

func buildKillmailRows(ctx context.Context, a *session.Session, kills []map[string]any, characterID, corpID int) (killmailSummary, error) {
	idSet := map[int]struct{}{}
	for _, kill := range kills {
		victim := j.Map(kill["victim"])
		for _, k := range []string{fCharacterID, "corporation_id", fShipTypeID} {
			if j.Int(victim[k]) != 0 {
				idSet[j.Int(victim[k])] = struct{}{}
			}
		}
		if j.Int(kill["solar_system_id"]) != 0 {
			idSet[j.Int(kill["solar_system_id"])] = struct{}{}
		}
	}
	names, err := a.Resolver.Names(ctx, setToList(idSet), nil)
	if err != nil {
		return killmailSummary{}, wrap("buildKillmailRows", err)
	}
	prices, err := a.Resolver.ReferencePrices(ctx)
	if err != nil {
		return killmailSummary{}, wrap("buildKillmailRows", err)
	}
	rows := make([]map[string]any, 0, len(kills))
	killsN, losses := 0, 0
	for _, kill := range kills {
		victim := j.Map(kill["victim"])
		var wasVictim bool
		if corpID != 0 {
			wasVictim = j.Int(victim["corporation_id"]) == corpID
		} else {
			wasVictim = j.Int(victim[fCharacterID]) == characterID
		}
		role := "kill"
		if wasVictim {
			role = "loss"
			losses++
		} else {
			killsN++
		}
		who := names[j.Int(victim[fCharacterID])]
		if who == "" {
			who = names[j.Int(victim["corporation_id"])]
		}
		rows = append(rows, map[string]any{
			"role": role, "time": kill["killmail_time"], fSystem: names[j.Int(kill["solar_system_id"])],
			"victim": who, "ship_lost": names[j.Int(victim[fShipTypeID])],
			"hull_value": isk(unitPrice(prices, j.Int(victim[fShipTypeID]))),
			"attackers":  len(j.Slice(kill["attackers"])),
			"zkill":      zkillURL(j.Int(kill["killmail_id"])),
		})
	}

	return killmailSummary{rows: rows, kills: killsN, losses: losses}, nil
}
