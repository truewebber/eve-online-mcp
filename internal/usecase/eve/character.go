package eve

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/domain/j"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type characterSkillsIn struct {
	Search         string `json:"search,omitempty"          jsonschema:"Case-insensitive substring of the skill name, e.g. 'Gunnery' or 'Caldari'. Strongly recommended — a full skill list is hundreds of rows."`
	TrainedOnly    *bool  `json:"trained_only,omitempty"    jsonschema:"Hide skills that are injected but sitting at level 0. Default true."`
	Limit          int    `json:"limit,omitempty"           jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	ResponseFormat string `json:"response_format,omitempty" jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
}

type characterStandingsIn struct {
	Limit int `json:"limit,omitempty" jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
}

func registerCharacter(s *mcp.Server) {
	addTool(s, &mcp.Tool{
		Name:        "eve_character_skills",
		Description: "Trained skills with levels and skill points.\n\nPrefer `search` over dumping everything: to answer \"can I fly a Drake\" you want the handful of relevant skills, not all 118.\n\nOne subtlety worth surfacing to the user: `active_level` can be lower than `level`. That means the account is on an Alpha (free) clone.\n\nReturns: total_sp, unallocated_sp, skills_known, at_level_5, skills[].",
	}, sessionTool(eveCharacterSkills))
	addTool(s, &mcp.Tool{
		Name:        "eve_character_skill_queue",
		Description: "The training queue: what is training now, what follows, and when it runs dry.\n\nAn empty queue means the character is accruing nothing — always worth telling the user.\n\nReturns: queued_skills, training_now, queue_empty_in, queue_ends, queue[].",
	}, sessionTool(eveCharacterSkillQueue))
	addTool(s, &mcp.Tool{
		Name:        "eve_character_clones",
		Description: "Jump clones with their locations and implants, plus the active clone's implants.\n\nUseful for \"where can I jump to\" and \"what implants would I lose if I died right now\".\n\nReturns: home_station, last_clone_jump, active_implants[], jump_clones[].",
	}, sessionTool(eveCharacterClones))
	addTool(s, &mcp.Tool{
		Name:        "eve_character_standings",
		Description: "NPC faction and corporation standings, plus loyalty point balances.\n\nStandings run -10 to +10 and gate agent access, broker fees and whether a faction's navy shoots you.\n\nReturns: loyalty_points[], standings[] sorted best-first.",
	}, sessionTool(eveCharacterStandings))
}

func eveCharacterSkills(ctx context.Context, a *session.Session, in characterSkillsIn) (any, error) {
	token, err := a.Character(ctx)
	if err != nil {
		return nil, wrap("eveCharacterSkills", err)
	}
	if err := a.RequireScope(token, "esi-skills.read_skills.v1", "skills"); err != nil {
		return nil, wrap("eveCharacterSkills", err)
	}
	cid := token.CharacterID
	result, err := a.ESI.Get(ctx, esiPath("characters", esiID(cid), "skills"), &cid, nil, nil)
	if err != nil {
		return nil, wrap("eveCharacterSkills", err)
	}
	payload := j.Map(result.Data)
	skills := j.Maps(payload["skills"])
	var ids []int
	for _, s := range skills {
		ids = append(ids, j.Int(s["skill_id"]))
	}
	names, err := a.Resolver.Names(ctx, ids, nil)
	if err != nil {
		return nil, wrap("eveCharacterSkills", err)
	}
	view := filterCharacterSkills(skills, names, in.Search, boolDef(in.TrainedOnly, true))
	sort.Slice(view.rows, func(i, k int) bool { return j.Str(view.rows[i][fSkill]) < j.Str(view.rows[k][fSkill]) })
	visible, meta := page(view.rows, limitOr(in.Limit, limitMedium), "Narrow with `search`, or raise `limit`.")
	at5 := 0
	for _, s := range skills {
		if j.Int(s["trained_skill_level"]) == skillLevelV {
			at5++
		}
	}
	out := merge(map[string]any{
		fCharacter: token.CharacterName, "total_sp": payload["total_sp"],
		"unallocated_sp": payload["unallocated_sp"], "skills_known": len(skills),
		"at_level_5": at5, "matching": len(view.rows), fDataAge: result.StaleNote(),
		"skills": project(visible, []string{fSkill, "level"}, concise(in.ResponseFormat)),
	}, meta)
	if view.capped > 0 {
		out["alpha_clone_warning"] = fmt.Sprintf("%d skills have active_level below trained level — this account looks like it is on an Alpha clone, so trained levels are capped.", view.capped)
	}

	return out, nil
}

type characterSkillsView struct {
	rows   []map[string]any
	capped int
}

func filterCharacterSkills(skills []map[string]any, names map[int]string, search string, trainedOnly bool) characterSkillsView {
	needle := strings.ToLower(strings.TrimSpace(search))
	var rows []map[string]any
	capped := 0
	for _, skill := range skills {
		name := names[j.Int(skill["skill_id"])]
		if name == "" {
			name = fmt.Sprintf("#%d", j.Int(skill["skill_id"]))
		}
		level := j.Int(skill["trained_skill_level"])
		active := j.Int(skill["active_skill_level"])
		if active == 0 && skill["active_skill_level"] == nil {
			active = level
		}
		if active < level {
			capped++
		}
		if !keepCharacterSkill(name, level, needle, trainedOnly) {
			continue
		}
		rows = append(rows, map[string]any{
			fSkill: name, "level": roman(level),
			"skillpoints": skill["skillpoints_in_skill"], "active_level": roman(active),
		})
	}

	return characterSkillsView{rows: rows, capped: capped}
}

func keepCharacterSkill(name string, level int, needle string, trainedOnly bool) bool {
	if trainedOnly && level == 0 {
		return false
	}
	if needle != "" && !strings.Contains(strings.ToLower(name), needle) {
		return false
	}

	return true
}

func eveCharacterSkillQueue(ctx context.Context, a *session.Session, _ empty) (any, error) {
	token, err := a.Character(ctx)
	if err != nil {
		return nil, wrap("eveCharacterSkillQueue", err)
	}
	if err := a.RequireScope(token, "esi-skills.read_skillqueue.v1", "the skill queue"); err != nil {
		return nil, wrap("eveCharacterSkillQueue", err)
	}
	cid := token.CharacterID
	result, err := a.ESI.Get(ctx, esiPath("characters", esiID(cid), "skillqueue"), &cid, nil, nil)
	if err != nil {
		return nil, wrap("eveCharacterSkillQueue", err)
	}
	entries := j.Maps(result.Data)
	if len(entries) == 0 {
		return map[string]any{
			fCharacter: token.CharacterName, "queue": []any{},
			"warning": "The skill queue is empty. This character is accruing no skill points at all until something is queued.",
		}, nil
	}
	var ids []int
	for _, e := range entries {
		ids = append(ids, j.Int(e["skill_id"]))
	}
	names, err := a.Resolver.Names(ctx, ids, nil)
	if err != nil {
		return nil, wrap("eveCharacterSkillQueue", err)
	}
	now := time.Now().UTC()
	sort.Slice(entries, func(i, k int) bool { return j.Int(entries[i]["queue_position"]) < j.Int(entries[k]["queue_position"]) })
	rows := formatSkillQueue(entries, names, now)
	emptyIn := vUnknown
	if last := parseTime(j.Str(rows[len(rows)-1]["finish_date"])); last != nil {
		emptyIn = humanDelta(last.Sub(now))
	}

	return map[string]any{
		fCharacter: token.CharacterName, "queued_skills": len(rows),
		"training_now":   strings.TrimSpace(j.Str(rows[0][fSkill]) + " " + j.Str(rows[0]["to_level"])),
		"queue_empty_in": emptyIn, "queue_ends": rows[len(rows)-1]["finish_date"],
		fDataAge: result.StaleNote(), "queue": rows,
	}, nil
}

func formatSkillQueue(entries []map[string]any, names map[int]string, now time.Time) []map[string]any {
	rows := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		finish := parseTime(j.Str(e["finish_date"]))
		finishes := "paused"
		if finish != nil {
			finishes = humanDelta(finish.Sub(now))
		}
		name := names[j.Int(e["skill_id"])]
		if name == "" {
			name = fmt.Sprintf("#%d", j.Int(e["skill_id"]))
		}
		rows = append(rows, map[string]any{
			"position": e["queue_position"], fSkill: name,
			"to_level": roman(j.Int(e["finished_level"])), "finishes_in": finishes,
			"finish_date": e["finish_date"],
		})
	}

	return rows
}

func eveCharacterClones(ctx context.Context, a *session.Session, _ empty) (any, error) {
	token, err := a.Character(ctx)
	if err != nil {
		return nil, wrap("eveCharacterClones", err)
	}
	if err := a.RequireScope(token, "esi-clones.read_clones.v1", "clones"); err != nil {
		return nil, wrap("eveCharacterClones", err)
	}
	if err := a.RequireScope(token, "esi-clones.read_implants.v1", "the active clone's implants"); err != nil {
		return nil, wrap("eveCharacterClones", err)
	}
	cid := token.CharacterID
	clonesRes, err := a.ESI.Get(ctx, esiPath("characters", esiID(cid), "clones"), &cid, nil, nil)
	if err != nil {
		return nil, wrap("eveCharacterClones", err)
	}
	implantsRes, err := a.ESI.Get(ctx, esiPath("characters", esiID(cid), "implants"), &cid, nil, nil)
	if err != nil {
		return nil, wrap("eveCharacterClones", err)
	}
	clones := j.Map(clonesRes.Data)
	implants := j.Slice(implantsRes.Data)
	jump := j.Maps(clones["jump_clones"])
	home := j.Map(clones["home_location"])
	names, err := a.Resolver.Names(ctx, collectCloneIDs(implants, jump, home), &cid)
	if err != nil {
		return nil, wrap("eveCharacterClones", err)
	}

	return map[string]any{
		fCharacter:        token.CharacterName,
		"home_station":    names[j.Int(home["location_id"])],
		"last_clone_jump": clones["last_clone_jump_date"],
		"active_implants": formatActiveImplants(implants, names), "jump_clones": formatJumpClones(jump, names),
		fDataAge: clonesRes.StaleNote(),
	}, nil
}

func collectCloneIDs(implants []any, jump []map[string]any, home map[string]any) []int {
	idSet := map[int]struct{}{}
	for _, v := range implants {
		idSet[j.Int(v)] = struct{}{}
	}
	for _, clone := range jump {
		for _, v := range j.Slice(clone["implants"]) {
			idSet[j.Int(v)] = struct{}{}
		}
		idSet[j.Int(clone["location_id"])] = struct{}{}
	}
	if j.Int(home["location_id"]) != 0 {
		idSet[j.Int(home["location_id"])] = struct{}{}
	}

	return setToList(idSet)
}

func formatActiveImplants(implants []any, names map[int]string) []string {
	active := make([]string, 0, len(implants))
	for _, v := range implants {
		active = append(active, names[j.Int(v)])
	}

	return active
}

func formatJumpClones(jump []map[string]any, names map[int]string) []map[string]any {
	jumps := make([]map[string]any, 0, len(jump))
	for _, clone := range jump {
		listed := j.Slice(clone["implants"])
		imps := make([]string, 0, len(listed))
		for _, v := range listed {
			imps = append(imps, names[j.Int(v)])
		}
		name := j.Str(clone[fName])
		if name == "" {
			name = fmt.Sprintf("Clone %v", clone["jump_clone_id"])
		}
		loc := names[j.Int(clone["location_id"])]
		if loc == "" {
			loc = vUnknown
		}
		jumps = append(jumps, map[string]any{fName: name, fLocation: loc, "implants": imps})
	}

	return jumps
}

func eveCharacterStandings(ctx context.Context, a *session.Session, in characterStandingsIn) (any, error) {
	token, err := a.Character(ctx)
	if err != nil {
		return nil, wrap("eveCharacterStandings", err)
	}
	if err := a.RequireScope(token, "esi-characters.read_standings.v1", "standings"); err != nil {
		return nil, wrap("eveCharacterStandings", err)
	}
	cid := token.CharacterID
	standingsRes, standingsErr := a.ESI.Get(ctx, esiPath("characters", esiID(cid), "standings"), &cid, nil, nil)
	lpScope := "esi-characters.read_loyalty.v1"
	lpGranted := a.HasScope(token, lpScope)
	lpData, lpErr := fetchCharacterLP(ctx, a, cid, lpGranted)
	var standings []map[string]any
	if standingsErr == nil {
		standings = j.Maps(standingsRes.Data)
	}
	names, err := a.Resolver.Names(ctx, standingsNameIDs(standings, lpData), nil)
	if err != nil {
		return nil, wrap("eveCharacterStandings", err)
	}
	rows := formatCharacterStandings(standings, names)
	visible, meta := page(rows, limitOr(in.Limit, limitMedium), "")
	out := merge(map[string]any{
		fCharacter: token.CharacterName, "loyalty_points": formatLoyaltyPoints(lpData, names), "standings": visible,
	}, meta)
	applyStandingsNotes(out, standingsErr, lpErr, lpGranted, token.CharacterName, lpScope)

	return out, nil
}

func fetchCharacterLP(ctx context.Context, a *session.Session, cid int, granted bool) ([]map[string]any, error) {
	if !granted {
		return nil, nil
	}
	lpRes, err := a.ESI.Get(ctx, esiPath("characters", esiID(cid), "loyalty", "points"), &cid, nil, nil)
	if err != nil {
		return nil, wrap("fetchCharacterLP", err)
	}

	return j.Maps(lpRes.Data), nil
}

func standingsNameIDs(standings, lpData []map[string]any) []int {
	idSet := map[int]struct{}{}
	for _, s := range standings {
		idSet[j.Int(s["from_id"])] = struct{}{}
	}
	for _, l := range lpData {
		idSet[j.Int(l["corporation_id"])] = struct{}{}
	}

	return setToList(idSet)
}

func formatCharacterStandings(standings []map[string]any, names map[int]string) []map[string]any {
	rows := make([]map[string]any, 0, len(standings))
	for _, s := range standings {
		rows = append(rows, map[string]any{
			"entity": names[j.Int(s["from_id"])], fType: s["from_type"],
			fStanding: mathRound(j.Float(s[fStanding]), decimalPlaces),
		})
	}
	sort.Slice(rows, func(i, k int) bool { return j.Float(rows[i][fStanding]) > j.Float(rows[k][fStanding]) })

	return rows
}

func formatLoyaltyPoints(lpData []map[string]any, names map[int]string) []map[string]any {
	sort.Slice(lpData, func(i, k int) bool {
		return j.Float(lpData[i]["loyalty_points"]) > j.Float(lpData[k]["loyalty_points"])
	})
	lpRows := make([]map[string]any, 0, len(lpData))
	for _, l := range lpData {
		lpRows = append(lpRows, map[string]any{fCorporation: names[j.Int(l["corporation_id"])], "lp": l["loyalty_points"]})
	}

	return lpRows
}

func applyStandingsNotes(out map[string]any, standingsErr, lpErr error, lpGranted bool, character, lpScope string) {
	if standingsErr != nil {
		out["standings_note"] = fmt.Sprintf("Standings could not be read: %v. The list above is empty because the call failed, not because there are none.", standingsErr)
	}
	if lpErr != nil {
		out["loyalty_points_note"] = fmt.Sprintf("Loyalty points could not be read: %v.", lpErr)
	} else if !lpGranted {
		out["loyalty_points_note"] = fmt.Sprintf("%s was not authorized with '%s', so loyalty point balances are not available. Re-run the login for this character to include them.", character, lpScope)
	}
}
