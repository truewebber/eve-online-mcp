# eve-mcp — MCP Tool Catalog

**This document is normative.** The implementation must match it: tool
names, descriptions and parameter schemas below are the contract the
model sees. `go run ./evals lint` checks a running server against the
tool rules in SPEC §4; changing a tool means changing this file in
the same commit. ESI endpoints behind each tool are documented in
[ESI.md](ESI.md).

Conventions (SPEC §4): every response carries `data_age`; list tools
default to `response_format="concise"` and a small `limit`; mutations
follow the confirm cycle (preview + `confirm_token`); errors are
actionable sentences with a `kind` field.

51 tools.


## Account & authorization

### `eve_server_status`

*Source: `internal/usecase/eve/account.go`*

Tranquility server status: player count, build version, uptime, VIP mode.

Also the cheapest way to confirm this server can reach ESI at all. EVE has a daily downtime around 11:00 UTC; a low player count right after it is normal, not a bug.

Returns: server_version, players, vip, start_time, data_age.

_No parameters._

### `eve_auth_status`

*Source: `internal/usecase/eve/account.go`*

Who is authorized here, and which in-game changes the tools can make.

Call this before anything else when you do not know the setup, and always before promising the user an in-game change. It lists authorized characters, every mutating capability (all of them are registered), remaining mail sends this hour, and how confirmation works.

Returns: characters[], default_character, capabilities, capability_reference, outward_facing_capabilities, mails_last_hour, mails_remaining_this_hour, mail_cap_per_hour, pending_confirmations, confirm_ttl_seconds, confirm.

_No parameters._

### `eve_auth_login_url`

*Source: `internal/usecase/eve/account.go`*

Generate an EVE SSO link the user must open to authorize a character.

You cannot complete this yourself — hand the URL to the user. They log in with their EVE account, approve the scope list, and the server stores the resulting token. One-time per character; several characters can be authorized by repeating it. The link always requests the full read, corporation, and write scope set.

Returns: login_url, scope_count, write_capabilities_requested, instructions.

_No parameters._

### `eve_auth_logout`

*Source: `internal/usecase/eve/account.go`*

Revoke this server's access to one character and delete its stored token.

Irreversible in the sense that re-authorizing needs another browser login, but it destroys nothing in-game.

Returns: removed, character_id.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `character` | string | **yes** | Character name or numeric id to log out. |

### `eve_character_overview`

*Source: `internal/usecase/eve/account.go`*

Everything you would glance at on logging in: corp, ISK, location, ship, training.

The best first call for almost any question about how the character is doing — it fuses seven ESI endpoints into roughly 200 tokens and tells you what to drill into next. It already includes the wallet balance and what is training, so there is no need to ask for those separately.

Partial results are normal: if one underlying endpoint fails, the rest still come back rather than the whole call erroring.

Returns: name, corporation, alliance, security_status, wallet_isk, online, solar_system, docked_at, ship_type, training_now, queue_ends, remaps_available.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |


## Character

### `eve_character_skills`

*Source: `internal/usecase/eve/character.go`*

Trained skills with levels and skill points.

Prefer `search` over dumping everything: to answer "can I fly a Drake" you want the handful of relevant skills, not all 118.

One subtlety worth surfacing to the user: `active_level` can be lower than `level`. That means the account is on an Alpha (free) clone.

Returns: total_sp, unallocated_sp, skills_known, at_level_5, skills[].

| Parameter | Type | Required | Description |
|---|---|---|---|
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `search` | string | no | Case-insensitive substring of the skill name, e.g. 'Gunnery' or 'Caldari'. Strongly recommended — a full skill list is hundreds of rows. |
| `trained_only` | bool | no | Hide skills that are injected but sitting at level 0. Default true. |
| `limit` | int | no | Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist. |
| `response_format` | string | no | 'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids. |

### `eve_character_skill_queue`

*Source: `internal/usecase/eve/character.go`*

The training queue: what is training now, what follows, and when it runs dry.

An empty queue means the character is accruing nothing — always worth telling the user.

Returns: queued_skills, training_now, queue_empty_in, queue_ends, queue[].

| Parameter | Type | Required | Description |
|---|---|---|---|
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |

### `eve_character_clones`

*Source: `internal/usecase/eve/character.go`*

Jump clones with their locations and implants, plus the active clone's implants.

Useful for "where can I jump to" and "what implants would I lose if I died right now".

Returns: home_station, last_clone_jump, active_implants[], jump_clones[].

| Parameter | Type | Required | Description |
|---|---|---|---|
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |

### `eve_character_standings`

*Source: `internal/usecase/eve/character.go`*

NPC faction and corporation standings, plus loyalty point balances.

Standings run -10 to +10 and gate agent access, broker fees and whether a faction's navy shoots you.

Returns: loyalty_points[], standings[] sorted best-first.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `limit` | int | no | Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist. |


## Assets

### `eve_assets_list`

*Source: `internal/usecase/eve/assets.go`*

Assets grouped by the station or structure they sit in, with an ISK estimate.

Items nested inside containers and ship holds are rolled up into the station that ultimately holds them. Valuation uses CCP's global average price per type, not a hub quote. ESI caches assets for a full hour.

Returns: total_estimated_value, total_locations, locations[] sorted by value.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `location` | string | no | Case-insensitive substring of a station or structure name, e.g. 'Jita' or 'Amarr VIII'. Empty means every location. |
| `min_value` | float64 | no | Hide locations holding less than this many ISK.,minimum=0 |
| `limit` | int | no | Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist. |
| `items` | int | no | Maximum items to list inside each location in detailed mode.,minimum=1,maximum=200 |
| `response_format` | string | no | 'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids. |

### `eve_assets_find`

*Source: `internal/usecase/eve/assets.go`*

Locate a specific item across every hangar, container and ship hold.

Answers "where did I leave my Orca" or "do I still have any Tritanium". Searches personal assets only. Corporation hangars are eve_corp_assets_find.

Returns: total_units, total_stacks, matches[].

| Parameter | Type | Required | Description |
|---|---|---|---|
| `name` | string | **yes** | Case-insensitive substring of the item type name, e.g. 'Drake' or 'Tritanium'. |
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `limit` | int | no | Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist. |
| `response_format` | string | no | 'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids. |

### `eve_assets_blueprints`

*Source: `internal/usecase/eve/assets.go`*

Blueprints with material/time efficiency and remaining runs.

Originals (BPO) can be used forever and report runs_left absent; copies (BPC) are consumed. Material efficiency (0-10) cuts input materials; time efficiency (0-20) cuts job duration.

Returns: originals, copies, blueprints[].

| Parameter | Type | Required | Description |
|---|---|---|---|
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `limit` | int | no | Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist. |
| `response_format` | string | no | 'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids. |


## Wallet

### `eve_wallet_history`

*Source: `internal/usecase/eve/wallet.go`*

Where the ISK went: journal entries and market trades, with totals by category.

The current balance is not here — eve_character_overview already carries it. ESI keeps roughly the last 30 days. The by_category summary is computed over the whole window, not just the returned rows.

Returns: period, totals, by_category[], and journal[] / transactions[] depending on kind.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `kind` | string | no | 'journal' is every ISK movement. 'transactions' is market trades. 'both' returns each in its own section. Default journal. |
| `ref_type` | string | no | Journal only: keep just one reason code, e.g. 'bounty_prizes'. |
| `limit` | int | no | Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist. |
| `response_format` | string | no | 'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids. |


## Industry

### `eve_industry_jobs`

*Source: `internal/usecase/eve/industry.go`*

Manufacturing, research, invention and reaction jobs with time remaining.

Jobs whose end time has passed show ready: true — they are finished but still need collecting in game.

Returns: active_jobs, ready_to_deliver, jobs[] sorted by end time.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `include_completed` | bool | no | Also return jobs that already delivered. Default false. |
| `limit` | int | no | Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist. |
| `response_format` | string | no | 'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids. |

### `eve_industry_planets`

*Source: `internal/usecase/eve/industry.go`*

Planetary interaction colonies: where they are and whether they have stalled.

Pass detail=true to get extractor_expires_in per colony — anything reading "expired" is currently earning nothing.

Returns: colony_count, colonies[].

| Parameter | Type | Required | Description |
|---|---|---|---|
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `detail` | bool | no | Fetch each colony's layout to report extractor expiry and stored output. Default false. |

### `eve_industry_mining`

*Source: `internal/usecase/eve/industry.go`*

Mining ledger for the last ~30 days, aggregated by ore type and valued.

Values use CCP's global average price. Returns: total_estimated_value, top_systems[], ores[] sorted by volume.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `limit` | int | no | Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist. |


## Market

### `eve_market_price`

*Source: `internal/usecase/eve/market.go`*

Live best buy and sell price for an item, from real orders on the market.

Use this — not the average price in asset or mining results — whenever the answer involves actually buying or selling something. best_sell is what you would pay to buy right now; best_buy is what you would get selling instantly.

Returns: best_sell, best_buy, spread_pct, volumes, ccp_average_price, packaged_volume_m3.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `item` | string | **yes** | Exact item type name, e.g. 'Tritanium' or 'Rifter'. Must match the in-game name exactly. |
| `region` | string | no | Exact region name. Empty means The Forge / Jita 4-4. |
| `whole_region` | bool | no | Price across every station in the region instead of just the main hub. |
| `history_days` | int | no | Summarise this many days of daily price history. 0 skips it.,minimum=0,maximum=365 |

### `eve_market_orders`

*Source: `internal/usecase/eve/market.go`*

The character's own open buy and sell orders, with fill progress and expiry.

Returns: open_orders, sell_side_value, buy_escrow_locked, orders[].

| Parameter | Type | Required | Description |
|---|---|---|---|
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `limit` | int | no | Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist. |
| `response_format` | string | no | 'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids. |

### `eve_market_contracts`

*Source: `internal/usecase/eve/market.go`*

Contracts the character issued or was assigned, newest first.

Courier contracts are the ones with a collateral and a reward. Returns: total, outstanding, contracts[].

| Parameter | Type | Required | Description |
|---|---|---|---|
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `outstanding_only` | bool | no | Only contracts still awaiting action. Default true. |
| `limit` | int | no | Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist. |
| `response_format` | string | no | 'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids. |


## Social

### `eve_mail_list`

*Source: `internal/usecase/eve/social.go`*

Mail headers only — sender, subject, date, read state. Bodies are not included.

To read the actual text of one mail, follow up with eve_mail_read using the mail_id from here.

Returns: unread count, mails[] with mail_id for follow-up.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `unread_only` | bool | no | Only list mail that has not been read yet. |
| `limit` | int | no | Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist. |
| `response_format` | string | no | 'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids. |

### `eve_mail_read`

*Source: `internal/usecase/eve/social.go`*

Fetch the full body text of one mail.

Read-only: this does not mark the mail as read in game. Use eve_mail_mark for that.

Returns: from, to[], subject, timestamp, body (markup stripped).

| Parameter | Type | Required | Description |
|---|---|---|---|
| `mail_id` | int | **yes** | Mail id from eve_mail_list.,minimum=1 |
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |

### `eve_social_notifications`

*Source: `internal/usecase/eve/social.go`*

In-game notifications: structure attacks, war decs, corp and contract events.

This is where genuinely time-critical things surface. The detail field is raw YAML with unresolved numeric ids.

Returns: unread count, notifications[] newest first.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `limit` | int | no | Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist. |
| `response_format` | string | no | 'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids. |

### `eve_social_killmails`

*Source: `internal/usecase/eve/social.go`*

Recent kills and losses involving this character.

hull_value covers the ship hull only. Each row carries a zkillboard link.

Returns: kills, losses, killmails[] newest first.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `limit` | int | no | Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist. |
| `response_format` | string | no | 'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids. |

### `eve_fitting_list`

*Source: `internal/usecase/eve/social.go`*

Saved ship fittings with their module lists.

In concise mode module lists are omitted. Returns: fittings[] with fitting_id (needed by eve_fitting_delete).

| Parameter | Type | Required | Description |
|---|---|---|---|
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `limit` | int | no | Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist. |
| `response_format` | string | no | 'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids. |


## Universe

### `eve_universe_search`

*Source: `internal/usecase/eve/universe.go`*

Resolve a partial or misspelled name to the exact EVE name and its id.

Call this first whenever you are not certain of a name. ESI matches on prefix, not fuzzily — this tool shortens the prefix and retries.

Returns: one section per requested category, each with total and results[] of {id, name}.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `query` | string | **yes** | At least 3 characters. Prefix match by default, so 'Trit' finds 'Tritanium'. |
| `categories` | string | no | Comma-separated subset of: agent, alliance, character, constellation, corporation, faction, inventory_type, region, solar_system, station, structure. |
| `strict` | bool | no | Exact-match instead of prefix match. |
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `limit` | int | no | Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist. |

### `eve_universe_item`

*Source: `internal/usecase/eve/universe.go`*

Item type reference: group, volume, mass, capacity and description.

packaged_volume_m3 is what hauling maths should use unless the item is assembled. For live cost use eve_market_price.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `item` | string | **yes** | Exact item type name, e.g. 'Rifter'. |

### `eve_universe_system`

*Source: `internal/usecase/eve/universe.go`*

Security status, region, and the last hour of kills and jumps for one system.

Returns: system, region, security_status, security_class, kills and jumps in the last hour.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `system` | string | **yes** | Exact solar system name, e.g. 'Jita'. |

### `eve_universe_route`

*Source: `internal/usecase/eve/universe.go`*

Gate-to-gate route between two systems, with the danger profile of each hop.

safe means the whole route stays in high-security space. Suicide ganking still happens in high-sec — mention avoid for Uedama/Niarja when hauling valuables.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `origin` | string | **yes** | Exact origin system name. |
| `destination` | string | **yes** | Exact destination system name. |
| `preference` | string | no | shorter (default), safer, or less_secure. |
| `avoid` | string | no | Comma-separated exact system names to route around, e.g. 'Uedama,Niarja'. |
| `show_hops` | bool | no | Include the full system-by-system list. |

### `eve_universe_hotspots`

*Source: `internal/usecase/eve/universe.go`*

Systems with the most ship and pod kills in the last hour, by name.

High npc_kills with low ship kills just means busy ratting. Returns: window, systems[] sorted by player kills.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `limit` | int | no | Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist. |


## Corporation (gated by in-game roles)

### `eve_corp_overview`

*Source: `internal/usecase/eve/corp.go`*

The corporation this character is in: ticker, wallets, roles, what you can read.

The right first call before any other eve_corp_* tool. Location-specific roles do not unlock ESI.

Returns: corporation, ticker, alliance, member_count, ceo, tax_pct, roles, wallets[], available_tools[].

| Parameter | Type | Required | Description |
|---|---|---|---|
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |

### `eve_corp_assets_list`

*Source: `internal/usecase/eve/corp.go`*

Corporation assets grouped by station or structure, with an ISK estimate. Needs the Director role. Large corps are truncated after 80 ESI pages.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `location` | string | no | Case-insensitive substring of a station or structure name. |
| `min_value` | float64 | no | Hide locations holding less than this many ISK.,minimum=0 |
| `limit` | int | no | Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist. |
| `items` | int | no | Maximum items per location in detailed mode.,minimum=1,maximum=200 |
| `response_format` | string | no | 'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids. |

### `eve_corp_assets_find`

*Source: `internal/usecase/eve/corp.go`*

Locate a specific item across every corp hangar, container and ship hold. Needs the Director role. Same search as eve_assets_find, but against the shared hangar — personal assets stay on that tool.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `name` | string | **yes** | Case-insensitive substring of the item type name. |
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `limit` | int | no | Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist. |
| `response_format` | string | no | 'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids. |

### `eve_corp_blueprints`

*Source: `internal/usecase/eve/corp.go`*

Corporation blueprints with material/time efficiency and remaining runs. Needs the Director role. Personal BPOs stay on eve_assets_blueprints.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `limit` | int | no | Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist. |
| `response_format` | string | no | 'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids. |

### `eve_corp_wallet`

*Source: `internal/usecase/eve/corp.go`*

Corporation ISK: the seven wallet divisions, plus journal and market trades. Needs Accountant or Junior_Accountant. Personal wallet stays on eve_wallet_history.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `kind` | string | no | balances (default), journal, transactions, or both. |
| `division` | int | no | Corporation wallet division, 1 through 7. Division 1 is the master wallet. Named divisions (if this character is a Director) come back from eve_corp_overview. |
| `ref_type` | string | no | Journal only: keep just one reason code. |
| `limit` | int | no | Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist. |
| `response_format` | string | no | 'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids. |

### `eve_corp_industry_jobs`

*Source: `internal/usecase/eve/corp.go`*

Corporation manufacturing, research, invention and reaction jobs. Needs Factory_Manager. Each row names the installer. Personal jobs stay on eve_industry_jobs.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `include_completed` | bool | no | Also return jobs that already delivered. |
| `limit` | int | no | Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist. |
| `response_format` | string | no | 'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids. |

### `eve_corp_mining`

*Source: `internal/usecase/eve/corp.go`*

Corporation moon-mining ledger and extraction timers. Accountant unlocks the observer ledger; Station_Manager unlocks extraction timers.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `limit` | int | no | Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist. |
| `response_format` | string | no | 'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids. |

### `eve_corp_orders`

*Source: `internal/usecase/eve/corp.go`*

The corporation's open buy and sell orders. Needs Accountant or Trader. Personal market orders stay on eve_market_orders.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `limit` | int | no | Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist. |
| `response_format` | string | no | 'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids. |

### `eve_corp_contracts`

*Source: `internal/usecase/eve/corp.go`*

Contracts issued by or assigned to the corporation. Any member with the corporation-contracts scope can read them. Use outstanding_only to hide finished ones.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `outstanding_only` | bool | no | Only contracts still awaiting action. Default true. |
| `limit` | int | no | Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist. |
| `response_format` | string | no | 'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids. |

### `eve_corp_killmails`

*Source: `internal/usecase/eve/corp.go`*

Recent kills and losses involving this corporation. Needs the Director role. Personal killmails stay on eve_social_killmails.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `limit` | int | no | Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist. |
| `response_format` | string | no | 'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids. |

### `eve_corp_structures`

*Source: `internal/usecase/eve/corp.go`*

Upwell structures this corporation owns: fuel, state and services. Needs Station_Manager. fuel_expires_in is the one to watch.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `limit` | int | no | Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist. |
| `response_format` | string | no | 'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids. |

### `eve_corp_members`

*Source: `internal/usecase/eve/corp.go`*

Current corporation members, alphabetically. Any member can read the roster. detailed adds ESI roles when this character is a Director.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `limit` | int | no | Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist. |
| `response_format` | string | no | 'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids. |


## Writes (mutations — confirm flow, SPEC §4.1)

### `eve_ui_set_waypoint`

*Source: `internal/usecase/eve/writes.go`*

Set an autopilot waypoint in the running game client.

This only moves the route marker on the map. It never undocks, flies or activates autopilot. Default clear_other_waypoints=true wipes a route the player may have spent time building.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `event_id` | int | **yes** | Event id from the in-game calendar.,minimum=1 |
| `response` | string | **yes** | accepted, declined, or tentative. |
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `confirm_token` | string | no | Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here. |

### `eve_ui_open_window`

*Source: `internal/usecase/eve/writes.go`*

Open a window in the running game client.

Good for handing something off to the player. Changes nothing in game and costs nothing.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `event_id` | int | **yes** | Event id from the in-game calendar.,minimum=1 |
| `response` | string | **yes** | accepted, declined, or tentative. |
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `confirm_token` | string | no | Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here. |

### `eve_fitting_save`

*Source: `internal/usecase/eve/writes.go`*

Save a ship fitting to the character's in-game fitting list.

Does not buy, move or fit anything — it stores a template. Unknown module names are rejected before anything is saved.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `name` | string | **yes** | Fitting name as it will appear in game. |
| `ship` | string | **yes** | Exact hull name, e.g. 'Rifter'. |
| `modules` | []fittingModule | **yes** | Modules as objects with name, flag, quantity. |
| `description` | string | no | Optional note stored with the fitting. |
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `confirm_token` | string | no | Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here. |

### `eve_fitting_delete`

*Source: `internal/usecase/eve/writes.go`*

Delete a saved fitting. Permanent — there is no undo in game. The preview names the fitting so the user can confirm before the token is spent.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `names` | string list | **yes** | Exact contact names to remove. |
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `confirm_token` | string | no | Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here. |

### `eve_mail_mark`

*Source: `internal/usecase/eve/writes.go`*

Change the read flag on one mail. This does not return the mail's contents — use eve_mail_read for that. Needs a confirm_token in confirm mode.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `mail_id` | int | **yes** | Mail id from eve_mail_list.,minimum=1 |
| `read` | bool | no | True marks it read, False marks it unread. Default true. |
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `confirm_token` | string | no | Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here. |

### `eve_mail_delete`

*Source: `internal/usecase/eve/writes.go`*

Delete one mail. Permanent — deleted EVE mail cannot be recovered. The preview shows sender, subject and date so the user can confirm.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `names` | string list | **yes** | Exact contact names to remove. |
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `confirm_token` | string | no | Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here. |

### `eve_mail_send`

*Source: `internal/usecase/eve/writes.go`*

Send an in-game EVE mail from this character to other players.

The most consequential tool on this server. The mail cannot be recalled. Show the preview to the user word for word — including the full body — and get an explicit yes before confirming.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `event_id` | int | **yes** | Event id from the in-game calendar.,minimum=1 |
| `response` | string | **yes** | accepted, declined, or tentative. |
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `confirm_token` | string | no | Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here. |

### `eve_contacts_set`

*Source: `internal/usecase/eve/writes.go`*

Add or update contacts with a standing.

A negative standing colours that player red in the overview. Treat it as a visible social act.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `names` | string list | **yes** | Exact character, corporation or alliance names. |
| `standing` | float64 | **yes** | -10.0 to 10.0.,minimum=-10,maximum=10 |
| `watched` | bool | no | Add to the watch list. Characters only. |
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `confirm_token` | string | no | Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here. |

### `eve_contacts_delete`

*Source: `internal/usecase/eve/writes.go`*

Remove contacts from this character's contact list.

Deleting a contact also clears any standing set on them. That is a visible social change, so confirm the names before the second call. It does not block or report anyone.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `names` | string list | **yes** | Exact contact names to remove. |
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `confirm_token` | string | no | Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here. |

### `eve_calendar_respond`

*Source: `internal/usecase/eve/writes.go`*

Respond to a calendar event invitation on this character.

The organiser and other invitees see accepted, declined or tentative in-game. This only RSVPs; it does not create, edit or delete events. Confirm before sending an answer the player will have to live with.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `event_id` | int | **yes** | Event id from the in-game calendar.,minimum=1 |
| `response` | string | **yes** | accepted, declined, or tentative. |
| `character` | string | no | Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them. |
| `confirm_token` | string | no | Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here. |
