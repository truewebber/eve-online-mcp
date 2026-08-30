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

func registerIndustry(s *mcp.Server, a *session.Session) {
	type jobsIn struct {
		Character        string `json:"character,omitempty"         jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
		IncludeCompleted *bool  `json:"include_completed,omitempty" jsonschema:"Also return jobs that already delivered. Default false."`
		Limit            int    `json:"limit,omitempty"             jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
		ResponseFormat   string `json:"response_format,omitempty"   jsonschema:"'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."`
	}
	addTool(s, &mcp.Tool{
		Name:        "eve_industry_jobs",
		Description: "Manufacturing, research, invention and reaction jobs with time remaining.\n\nJobs whose end time has passed show ready: true — they are finished but still need collecting in game.\n\nReturns: active_jobs, ready_to_deliver, jobs[] sorted by end time.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in jobsIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *session.Session) (any, error) {
			token, err := a.ResolveCharacter(in.Character)
			if err != nil {
				return nil, err
			}
			if err := a.RequireScope(token, "esi-industry.read_character_jobs.v1", "industry jobs"); err != nil {
				return nil, err
			}
			cid := token.CharacterID
			result, err := a.ESI.Get(fmt.Sprintf("/characters/%d/industry/jobs", cid), &cid, map[string]any{"include_completed": boolDef(in.IncludeCompleted, false)}, nil)
			if err != nil {
				return nil, err
			}

			return industryJobsResult(a, token.CharacterName, cid, result.Data, result.StaleNote(), limitOr(in.Limit, 20), concise(in.ResponseFormat), false)
		})
	})

	type planetsIn struct {
		Character string `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
		Detail    *bool  `json:"detail,omitempty"    jsonschema:"Fetch each colony's layout to report extractor expiry and stored output. Default false."`
	}
	addTool(s, &mcp.Tool{
		Name:        "eve_industry_planets",
		Description: "Planetary interaction colonies: where they are and whether they have stalled.\n\nPass detail=true to get extractor_expires_in per colony — anything reading \"expired\" is currently earning nothing.\n\nReturns: colony_count, colonies[].",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in planetsIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *session.Session) (any, error) {
			token, err := a.ResolveCharacter(in.Character)
			if err != nil {
				return nil, err
			}
			if err := a.RequireScope(token, "esi-planets.manage_planets.v1", "planetary colonies"); err != nil {
				return nil, err
			}
			cid := token.CharacterID
			result, err := a.ESI.Get(fmt.Sprintf("/characters/%d/planets", cid), &cid, nil, nil)
			if err != nil {
				return nil, err
			}
			colonies := j.Maps(result.Data)
			if len(colonies) == 0 {
				return map[string]any{"character": token.CharacterName, "colonies": []any{}, "note": "No PI colonies."}, nil
			}
			idSet := map[int]struct{}{}
			for _, c := range colonies {
				idSet[j.Int(c["planet_id"])] = struct{}{}
				idSet[j.Int(c["solar_system_id"])] = struct{}{}
			}
			names, _ := a.Resolver.Names(setToList(idSet), nil)
			var rows []map[string]any
			for _, c := range colonies {
				rows = append(rows, map[string]any{
					"planet": names[j.Int(c["planet_id"])], "system": names[j.Int(c["solar_system_id"])],
					"type": c["planet_type"], "upgrade_level": c["upgrade_level"],
					"pins": c["num_pins"], "planet_id": c["planet_id"],
				})
			}
			if boolDef(in.Detail, false) {
				now := time.Now().UTC()
				for i, c := range colonies {
					layout, err := a.ESI.Get(fmt.Sprintf("/characters/%d/planets/%d", cid, j.Int(c["planet_id"])), &cid, nil, nil)
					if err != nil {
						continue
					}
					pins := j.Maps(j.Map(layout.Data)["pins"])
					var expiries []time.Time
					for _, p := range pins {
						if t := parseTime(j.Str(p["expiry_time"])); t != nil {
							expiries = append(expiries, *t)
						}
					}
					if len(expiries) > 0 {
						soonest := expiries[0]
						for _, t := range expiries[1:] {
							if t.Before(soonest) {
								soonest = t
							}
						}
						if !soonest.After(now) {
							rows[i]["extractor_expires_in"] = "EXPIRED — producing nothing"
						} else {
							rows[i]["extractor_expires_in"] = humanDelta(soonest.Sub(now))
						}
					}
					stored := map[int]int{}
					for _, p := range pins {
						for _, content := range j.Maps(p["contents"]) {
							stored[j.Int(content["type_id"])] += j.Int(content["amount"])
						}
					}
					if len(stored) > 0 {
						pn, _ := a.Resolver.Names(keys(stored), nil)
						type kv struct {
							n string
							q int
						}
						var list []kv
						for t, q := range stored {
							list = append(list, kv{pn[t], q})
						}
						sort.Slice(list, func(a, b int) bool { return list[a].q > list[b].q })
						if len(list) > 8 {
							list = list[:8]
						}
						m := map[string]int{}
						for _, x := range list {
							m[x.n] = x.q
						}
						rows[i]["stored"] = m
					}
				}
			}

			return map[string]any{
				"character": token.CharacterName, "colony_count": len(rows),
				"data_age": result.StaleNote(), "colonies": rows,
			}, nil
		})
	})

	type miningIn struct {
		Character string `json:"character,omitempty" jsonschema:"Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."`
		Limit     int    `json:"limit,omitempty"     jsonschema:"Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."`
	}
	addTool(s, &mcp.Tool{
		Name:        "eve_industry_mining",
		Description: "Mining ledger for the last ~30 days, aggregated by ore type and valued.\n\nValues use CCP's global average price. Returns: total_estimated_value, top_systems[], ores[] sorted by volume.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in miningIn) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *session.Session) (any, error) {
			token, err := a.ResolveCharacter(in.Character)
			if err != nil {
				return nil, err
			}
			if err := a.RequireScope(token, "esi-industry.read_character_mining.v1", "the mining ledger"); err != nil {
				return nil, err
			}
			cid := token.CharacterID
			result, err := a.ESI.GetAllPages(fmt.Sprintf("/characters/%d/mining", cid), &cid, nil, 40)
			if err != nil {
				return nil, err
			}
			entries := j.Maps(result.Data)
			if len(entries) == 0 {
				return map[string]any{"character": token.CharacterName, "ores": []any{}, "note": "Nothing mined recently."}, nil
			}
			totals := map[int]int{}
			bySystem := map[int]int{}
			for _, e := range entries {
				totals[j.Int(e["type_id"])] += j.Int(e["quantity"])
				bySystem[j.Int(e["solar_system_id"])] += j.Int(e["quantity"])
			}
			names, _ := a.Resolver.Names(append(keys(totals), keys(bySystem)...), nil)
			prices, _ := a.Resolver.ReferencePrices()
			var rows []map[string]any
			grand := 0.0
			for tid, qty := range totals {
				value := unitPrice(prices, tid) * float64(qty)
				grand += value
				rows = append(rows, map[string]any{"ore": nameOr(names, tid), "units": qty, "estimated_value": isk(value)})
			}
			sort.Slice(rows, func(i, k int) bool { return j.Int(rows[i]["units"]) > j.Int(rows[k]["units"]) })
			visible, meta := page(rows, limitOr(in.Limit, 15), "")
			type kv struct{ id, q int }
			var sys []kv
			for id, q := range bySystem {
				sys = append(sys, kv{id, q})
			}
			sort.Slice(sys, func(i, k int) bool { return sys[i].q > sys[k].q })
			if len(sys) > 5 {
				sys = sys[:5]
			}
			var top []map[string]any
			for _, s := range sys {
				top = append(top, map[string]any{"system": nameOr(names, s.id), "units": s.q})
			}

			return merge(map[string]any{
				"character": token.CharacterName, "period": "last ~30 days",
				"total_estimated_value": isk(grand), "top_systems": top,
				"data_age": result.StaleNote(), "ores": visible,
			}, meta), nil
		})
	})
}

func industryJobsResult(a *session.Session, character string, cid int, data any, stale string, limit int, conciseMode, withInstaller bool) (map[string]any, error) {
	jobs := j.Maps(data)
	if len(jobs) == 0 {
		return map[string]any{
			"character": character, "jobs": []any{},
			"note": "No industry jobs. Pass include_completed=true to see finished ones.",
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
	names, _ := a.Resolver.Names(append(setToList(idSet), setToList(people)...), nil)
	places, _ := a.Resolver.Names(setToList(placeSet), &cid)
	now := time.Now().UTC()
	var rows []map[string]any
	for _, job := range jobs {
		end := parseTime(j.Str(job["end_date"]))
		ready := end != nil && !end.After(now)
		ends := "unknown"
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
			"runs": job["runs"], "ends_in": ends, "location": places[loc],
			"ready": ready, "status": job["status"],
			"blueprint":    names[j.Int(job["blueprint_type_id"])],
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
	keep := []string{"activity", "product", "runs", "ends_in", "location"}
	if withInstaller {
		keep = append(keep, "installer")
	}

	return merge(map[string]any{
		"character": character, "active_jobs": active, "ready_to_deliver": readyN,
		"data_age": stale, "jobs": project(visible, keep, conciseMode),
	}, meta), nil
}

func activityName(id int) string {
	if n, ok := activities[id]; ok {
		return n
	}

	return fmt.Sprintf("#%d", id)
}
