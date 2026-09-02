package eve

import (
	"context"
	"sort"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/domain/write"
	"github.com/truewebber/eve-online-mcp/internal/j"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerAccount(s *mcp.Server) {
	addTool(s, &mcp.Tool{
		Name:        "eve_server_status",
		Description: "Tranquility server status: player count, build version, uptime, VIP mode.\n\nAlso the cheapest way to confirm this server can reach ESI at all. EVE has a daily downtime around 11:00 UTC; a low player count right after it is normal, not a bug.\n\nReturns: server_version, players, vip, start_time, data_age.",
	}, sessionTool(eveServerStatus))
	addTool(s, &mcp.Tool{
		Name:        "eve_auth_status",
		Description: "Who this connection is, and which in-game changes it can make.\n\nCall this before anything else when you do not know the setup, and always before promising the user an in-game change. This connection is signed in as exactly one character and acts only as that character; there is no way to add or switch to another one from here — that is a second server entry in the client, signed in separately.\n\nReturns: character, corporation, capabilities, capability_reference, outward_facing_capabilities, mails_last_hour, mails_remaining_this_hour, mail_cap_per_hour, pending_confirmations, confirm_ttl_seconds, confirm, session_expires_at.",
	}, sessionTool(eveAuthStatus))
	addTool(s, &mcp.Tool{
		Name:        "eve_auth_logout",
		Description: "Sign this connection out and revoke the server's access to its character.\n\nEnds the connection: the stored EVE authorization is revoked at CCP and every following call fails until the user signs in again through the browser. Destroys nothing in game. There is no argument — a connection can only log out the character it is.\n\nReturns: removed, character_id, character.",
	}, sessionTool(eveAuthLogout))
	addTool(s, &mcp.Tool{
		Name:        "eve_character_overview",
		Description: "Everything you would glance at on logging in: corp, ISK, location, ship, training.\n\nThe best first call for almost any question about how the character is doing — it fuses seven ESI endpoints into roughly 200 tokens and tells you what to drill into next. It already includes the wallet balance and what is training, so there is no need to ask for those separately.\n\nPartial results are normal: if one underlying endpoint fails, the rest still come back rather than the whole call erroring.\n\nReturns: name, corporation, alliance, security_status, wallet_isk, online, solar_system, docked_at, ship_type, training_now, queue_ends, remaps_available.",
	}, sessionTool(eveCharacterOverview))
}

func eveServerStatus(ctx context.Context, a *session.Session, _ empty) (any, error) {
	result, err := a.ESI.Get(ctx, esi.Path("/status"), nil, nil, nil)
	if err != nil {
		return nil, wrap("eveServerStatus", err)
	}
	out := j.Map(result.Data)
	out[fDataAge] = result.StaleNote()

	return out, nil
}

func eveAuthStatus(ctx context.Context, a *session.Session, _ empty) (any, error) {
	token, err := a.Character(ctx)
	if err != nil {
		return nil, wrap("eveAuthStatus", err)
	}
	policy := a.Guard.Status(ctx)
	var outward []string
	for name, cap := range write.Capabilities() {
		if cap.OutwardFacing {
			outward = append(outward, name)
		}
	}
	sort.Strings(outward)
	policy["outward_facing_capabilities"] = outward
	if live, err := a.Live(ctx); err == nil {
		policy["session_expires_at"] = live.ValidTil.UTC().Format(time.RFC3339)
	}
	writes := write.AllWriteScopeSet()
	corps := write.CorpScopeSet()

	return merge(map[string]any{
		"character": map[string]any{
			fName: token.CharacterName, fCharacterID: token.CharacterID,
			"scope_count":        len(token.Scopes),
			"write_scopes":       sortStrings(intersect(token.Scopes, writes)),
			"corporation_scopes": sortStrings(intersect(token.Scopes, corps)),
		},
	}, policy), nil
}

func eveAuthLogout(ctx context.Context, a *session.Session, _ empty) (any, error) {
	out, err := a.Logout(ctx)
	if err != nil {
		return nil, wrap("eveAuthLogout", err)
	}

	return map[string]any{"removed": out.CharacterName, fCharacterID: out.CharacterID}, nil
}

type overviewBox struct {
	r   esi.Result
	err error
}

func eveCharacterOverview(ctx context.Context, a *session.Session, _ empty) (any, error) {
	token, err := a.Character(ctx)
	if err != nil {
		return nil, wrap("eveCharacterOverview", err)
	}
	cid := token.CharacterID
	got := fetchOverview(ctx, a, cid)
	out := map[string]any{fCharacterID: cid, fName: token.CharacterName}
	applyOverviewPublic(ctx, a, got.public, out)
	applyOverviewWallet(got.wallet, out)
	applyOverviewOnline(got.online, out)
	applyOverviewLocation(ctx, a, cid, got.location, out)
	applyOverviewShip(ctx, a, got.ship, out)
	applyOverviewQueue(ctx, a, got.queue, out)
	if got.attributes.err == nil {
		out["remaps_available"] = j.Map(got.attributes.r.Data)["bonus_remaps"]
	}

	if age := staleOf(got.public, got.wallet, got.location, got.ship, got.online, got.queue, got.attributes); age != "" {
		out[fDataAge] = age
	}

	return compact(out), nil
}

func staleOf(boxes ...overviewBox) string {
	var ages []float64
	for _, b := range boxes {
		if b.err == nil {
			ages = append(ages, b.r.AgeSeconds)
		}
	}
	if len(ages) == 0 {
		return ""
	}

	return staleNote(ages...)
}

type overviewFetch struct {
	public, wallet, location, ship, online, queue, attributes overviewBox
}

func fetchOverview(ctx context.Context, a *session.Session, cid int) overviewFetch {
	get := func(path esi.Route, auth bool) overviewBox {
		var id *int
		if auth {
			id = &cid
		}
		r, err := a.ESI.Get(ctx, path, id, nil, nil)

		return overviewBox{r, err}
	}
	ch := make(chan func(*overviewFetch), overviewFetches)
	launch := func(path esi.Route, auth bool, set func(*overviewFetch, overviewBox)) {
		go func() {
			box := get(path, auth)
			ch <- func(out *overviewFetch) { set(out, box) }
		}()
	}
	launch(esiPath("characters", esiID(cid)), false, func(o *overviewFetch, b overviewBox) { o.public = b })
	launch(esiPath("characters", esiID(cid), fWallet), true, func(o *overviewFetch, b overviewBox) { o.wallet = b })
	launch(esiPath("characters", esiID(cid), fLocation), true, func(o *overviewFetch, b overviewBox) { o.location = b })
	launch(esiPath("characters", esiID(cid), fShip), true, func(o *overviewFetch, b overviewBox) { o.ship = b })
	launch(esiPath("characters", esiID(cid), "online"), true, func(o *overviewFetch, b overviewBox) { o.online = b })
	launch(esiPath("characters", esiID(cid), "skillqueue"), true, func(o *overviewFetch, b overviewBox) { o.queue = b })
	launch(esiPath("characters", esiID(cid), "attributes"), true, func(o *overviewFetch, b overviewBox) { o.attributes = b })

	var out overviewFetch
	for range overviewFetches {
		(<-ch)(&out)
	}

	return out
}

func applyOverviewPublic(ctx context.Context, a *session.Session, public overviewBox, out map[string]any) {
	if public.err != nil {
		return
	}
	info := j.Map(public.r.Data)
	ids := idsFrom(info["corporation_id"], info["alliance_id"])
	n, err := a.Resolver.Names(ctx, ids, nil)
	if err != nil {
		return
	}
	out[fCorporation] = n[j.Int(info["corporation_id"])]
	if j.Int(info["alliance_id"]) != 0 {
		out[fAlliance] = n[j.Int(info["alliance_id"])]
	}
	out["security_status"] = mathRound(j.Float(info["security_status"]), decimalPlaces)
	out["birthday"] = info["birthday"]
}

func applyOverviewWallet(wallet overviewBox, out map[string]any) {
	if wallet.err != nil || !isWalletAmount(wallet.r.Data) {
		return
	}
	out["wallet_isk"] = wallet.r.Data
	out[fWallet] = isk(wallet.r.Data)
}

func isWalletAmount(v any) bool {
	switch v.(type) {
	case float64, float32, int, int64:
		return true
	default:
		return false
	}
}

func applyOverviewOnline(online overviewBox, out map[string]any) {
	if online.err != nil {
		return
	}
	o := j.Map(online.r.Data)
	out["online"] = o["online"]
	out["last_login"] = o["last_login"]
}

func applyOverviewLocation(ctx context.Context, a *session.Session, cid int, location overviewBox, out map[string]any) {
	if location.err != nil {
		return
	}
	loc := j.Map(location.r.Data)
	placeIDs := idsFrom(loc["solar_system_id"], loc["station_id"], loc["structure_id"])
	n, err := a.Resolver.Names(ctx, placeIDs, &cid)
	if err != nil {
		return
	}
	out["solar_system"] = n[j.Int(loc["solar_system_id"])]
	docked := j.Int(loc["station_id"])
	if docked == 0 {
		docked = j.Int(loc["structure_id"])
	}
	if docked != 0 {
		out["docked_at"] = n[docked]
	} else {
		out["docked_at"] = "in space"
	}
	out["location_age"] = location.r.StaleNote()
}

func applyOverviewShip(ctx context.Context, a *session.Session, ship overviewBox, out map[string]any) {
	if ship.err != nil {
		return
	}
	sh := j.Map(ship.r.Data)
	name, err := a.Resolver.Name(ctx, j.Int(sh[fShipTypeID]), nil)
	if err != nil {
		return
	}
	out["ship_type"] = name
	if sn := j.Str(sh["ship_name"]); sn != "" && sn != name {
		out["ship_name"] = sn
	}
}

func applyOverviewQueue(ctx context.Context, a *session.Session, queue overviewBox, out map[string]any) {
	if queue.err != nil {
		return
	}
	if _, ok := queue.r.Data.([]any); !ok {
		return
	}
	var entries []map[string]any
	for _, e := range j.Maps(queue.r.Data) {
		if j.Str(e["finish_date"]) != "" {
			entries = append(entries, e)
		}
	}
	if len(entries) == 0 {
		out["warning"] = "Skill queue is empty — training time is being wasted."

		return
	}
	first := entries[0]
	skill, err := a.Resolver.Name(ctx, j.Int(first["skill_id"]), nil)
	if err != nil {
		return
	}
	out["training_now"] = skill + " " + roman(j.Int(first["finished_level"]))
	out["training_finishes"] = first["finish_date"]
	out["queue_length"] = len(entries)
	out["queue_ends"] = entries[len(entries)-1]["finish_date"]
}

func sortStrings(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)

	return out
}

func mathRound(v float64, places int) float64 {
	p := 1.0
	for range places {
		p *= 10
	}

	return float64(int(v*p+roundHalf)) / p
}
