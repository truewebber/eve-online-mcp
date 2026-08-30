package eve

import (
	"context"
	"fmt"
	"sort"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/domain/j"
	"github.com/truewebber/eve-online-mcp/internal/domain/write"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerAccount(s *mcp.Server, a *session.Session) {
	addTool(s, &mcp.Tool{
		Name:        "eve_server_status",
		Description: "Tranquility server status: player count, build version, uptime, VIP mode.\n\nAlso the cheapest way to confirm this server can reach ESI at all. EVE has a daily downtime around 11:00 UTC; a low player count right after it is normal, not a bug.\n\nReturns: server_version, players, vip, start_time, data_age.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *session.Session) (any, error) {
			result, err := a.ESI.Get("/status", nil, nil, nil)
			if err != nil {
				return nil, err
			}
			out := j.Map(result.Data)
			out["data_age"] = result.StaleNote()
			return out, nil
		})
	})

	addTool(s, &mcp.Tool{
		Name:        "eve_auth_status",
		Description: "Who is authorized here, and which in-game changes the tools can make.\n\nCall this before anything else when you do not know the setup, and always before promising the user an in-game change. It lists authorized characters, every mutating capability (all of them are registered), remaining mail sends this hour, and how confirmation works.\n\nReturns: characters[], default_character, capabilities, capability_reference, outward_facing_capabilities, mails_last_hour, mails_remaining_this_hour, mail_cap_per_hour, pending_confirmations, confirm_ttl_seconds, confirm.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *session.Session) (any, error) {
			tokens := a.SSO.Store.All()
			policy := a.Guard.Status(ctx)
			var outward []string
			for name, cap := range write.Capabilities {
				if cap.OutwardFacing {
					outward = append(outward, name)
				}
			}
			sort.Strings(outward)
			policy["outward_facing_capabilities"] = outward
			writes := write.AllWriteScopeSet()
			corps := write.CorpScopeSet()
			if len(tokens) == 0 {
				return merge(map[string]any{
					"characters": []any{},
					"next_step":  "Nobody is authorized. Call eve_auth_login_url and give the user the link to open in a browser.",
				}, policy), nil
			}
			var chars []map[string]any
			for _, t := range tokens {
				chars = append(chars, map[string]any{
					"name": t.CharacterName, "character_id": t.CharacterID,
					"scope_count":        len(t.Scopes),
					"write_scopes":       sortStrings(intersect(t.Scopes, writes)),
					"corporation_scopes": sortStrings(intersect(t.Scopes, corps)),
				})
			}
			var def any
			if len(tokens) == 1 {
				def = tokens[0].CharacterName
			}
			return merge(map[string]any{
				"characters": chars, "default_character": def,
			}, policy), nil
		})
	})

	addTool(s, &mcp.Tool{
		Name:        "eve_auth_login_url",
		Description: "Generate an EVE SSO link the user must open to authorize a character.\n\nYou cannot complete this yourself — hand the URL to the user. They log in with their EVE account, approve the scope list, and the server stores the resulting token. One-time per character; several characters can be authorized by repeating it. The link always requests the full read, corporation, and write scope set.\n\nReturns: login_url, scope_count, write_capabilities_requested, instructions.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *session.Session) (any, error) {
			url, state, err := a.StartAltLogin(ctx)
			if err != nil {
				return nil, err
			}
			scopes := write.RequestedScopes()
			writes := write.CapabilityNames()
			return map[string]any{
				"login_url": url, "state": state, "scope_count": len(scopes),
				"write_capabilities_requested": writes,
				"instructions":                 "Open login_url in a browser, pick the character, approve. The link is valid for 15 minutes and works once.",
			}, nil
		})
	})

	type logoutIn struct {
		Character string `json:"character" jsonschema:"Character name or numeric id to log out."`
	}
	addTool(s, &mcp.Tool{
		Name:        "eve_auth_logout",
		Description: "Revoke this server's access to one character and delete its stored token.\n\nIrreversible in the sense that re-authorizing needs another browser login, but it destroys nothing in-game.\n\nReturns: removed, character_id.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in logoutIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *session.Session) (any, error) {
			token, err := a.ResolveCharacter(in.Character)
			if err != nil {
				return nil, err
			}
			a.SSO.Revoke(token.CharacterID)
			return map[string]any{"removed": token.CharacterName, "character_id": token.CharacterID}, nil
		})
	})

	type overviewIn struct {
		Character string `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
	}
	addTool(s, &mcp.Tool{
		Name:        "eve_character_overview",
		Description: "Everything you would glance at on logging in: corp, ISK, location, ship, training.\n\nThe best first call for almost any question about how the character is doing — it fuses seven ESI endpoints into roughly 200 tokens and tells you what to drill into next. It already includes the wallet balance and what is training, so there is no need to ask for those separately.\n\nPartial results are normal: if one underlying endpoint fails, the rest still come back rather than the whole call erroring.\n\nReturns: name, corporation, alliance, security_status, wallet_isk, online, solar_system, docked_at, ship_type, training_now, queue_ends, remaps_available.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in overviewIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *session.Session) (any, error) {
			token, err := a.ResolveCharacter(in.Character)
			if err != nil {
				return nil, err
			}
			cid := token.CharacterID
			type box struct {
				r   esi.Result
				err error
			}
			get := func(path string, auth bool) box {
				var id *int
				if auth {
					id = &cid
				}
				r, err := a.ESI.Get(path, id, nil, nil)
				return box{r, err}
			}
			ch := make(chan box, 7)
			go func() { ch <- get(fmt.Sprintf("/characters/%d", cid), false) }()
			go func() { ch <- get(fmt.Sprintf("/characters/%d/wallet", cid), true) }()
			go func() { ch <- get(fmt.Sprintf("/characters/%d/location", cid), true) }()
			go func() { ch <- get(fmt.Sprintf("/characters/%d/ship", cid), true) }()
			go func() { ch <- get(fmt.Sprintf("/characters/%d/online", cid), true) }()
			go func() { ch <- get(fmt.Sprintf("/characters/%d/skillqueue", cid), true) }()
			go func() { ch <- get(fmt.Sprintf("/characters/%d/attributes", cid), true) }()
			public, wallet, location, ship, online, queue, attributes := <-ch, <-ch, <-ch, <-ch, <-ch, <-ch, <-ch

			out := map[string]any{"character_id": cid, "name": token.CharacterName}
			if public.err == nil {
				info := j.Map(public.r.Data)
				ids := idsFrom(info["corporation_id"], info["alliance_id"])
				n, _ := a.Resolver.Names(ids, nil)
				out["corporation"] = n[j.Int(info["corporation_id"])]
				if j.Int(info["alliance_id"]) != 0 {
					out["alliance"] = n[j.Int(info["alliance_id"])]
				}
				out["security_status"] = mathRound(j.Float(info["security_status"]), 2)
				out["birthday"] = info["birthday"]
			}
			if wallet.err == nil {
				out["wallet_isk"] = wallet.r.Data
				out["wallet"] = isk(wallet.r.Data)
			}
			if online.err == nil {
				o := j.Map(online.r.Data)
				out["online"] = o["online"]
				out["last_login"] = o["last_login"]
			}
			if location.err == nil {
				loc := j.Map(location.r.Data)
				placeIDs := idsFrom(loc["solar_system_id"], loc["station_id"], loc["structure_id"])
				n, _ := a.Resolver.Names(placeIDs, &cid)
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
			if ship.err == nil {
				sh := j.Map(ship.r.Data)
				name, _ := a.Resolver.Name(j.Int(sh["ship_type_id"]), nil)
				out["ship_type"] = name
				if sn := j.Str(sh["ship_name"]); sn != "" && sn != name {
					out["ship_name"] = sn
				}
			}
			if queue.err == nil {
				var entries []map[string]any
				for _, e := range j.Maps(queue.r.Data) {
					if j.Str(e["finish_date"]) != "" {
						entries = append(entries, e)
					}
				}
				if len(entries) > 0 {
					first := entries[0]
					skill, _ := a.Resolver.Name(j.Int(first["skill_id"]), nil)
					out["training_now"] = skill + " " + roman(j.Int(first["finished_level"]))
					out["training_finishes"] = first["finish_date"]
					out["queue_length"] = len(entries)
					out["queue_ends"] = entries[len(entries)-1]["finish_date"]
				} else {
					out["warning"] = "Skill queue is empty — training time is being wasted."
				}
			}
			if attributes.err == nil {
				out["remaps_available"] = j.Map(attributes.r.Data)["bonus_remaps"]
			}
			return compact(out), nil
		})
	})
}

func sortStrings(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}

func mathRound(v float64, places int) float64 {
	p := 1.0
	for i := 0; i < places; i++ {
		p *= 10
	}
	return float64(int(v*p+0.5)) / p
}
