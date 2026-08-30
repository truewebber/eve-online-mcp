# eve-mcp — EVE Online API Usage Reference

**This document is normative.** Every ESI and SSO endpoint the server is
allowed to call is listed here; the implementation must not call
anything else. Each row cites where the endpoint is used in this repo
(the provenance of the entry) and links to CCP's official documentation.

Provenance note: this list was extracted from the Go sources of this
repository (every `ESI.Get/GetAllPages/GetCursorPages/Post/Put/Delete`
call site) on 2026-08-30. Official documentation links point to CCP's
[API Explorer](https://developers.eveonline.com/api-explorer) — the
canonical ESI reference (per [EVE Developer Docs](https://developers.eveonline.com/docs/services/esi/overview/),
"You can find all the endpoints and try them out in the API Explorer").
SSO endpoints are documented in the official
[SSO guide](https://developers.eveonline.com/docs/services/sso/) and
[docs.esi.evetech.net](https://docs.esi.evetech.net/docs/sso/).

Base URL: `https://esi.evetech.net`. Every request carries an
identifying `User-Agent` and the pinned `X-Compatibility-Date`
(SPEC §9). GETs go through the in-memory ETag cache (SPEC §5.1 — there
are no cache tables in Postgres); all traffic goes through
`internal/adapter/esi.Client` — never a bare `http.Client`.

Explorer links use the operation anchor:
`https://developers.eveonline.com/api-explorer#/operations/{OperationId}`.

## 1. Public endpoints (no authentication)

| Method & path | Purpose | Used by (tool / repo source) | Official docs |
|---|---|---|---|
| `GET /status` | server status | `eve_server_status` — `usecase/eve/account.go` | [GetStatus](https://developers.eveonline.com/api-explorer#/operations/GetStatus) |
| `GET /characters/{character_id}` | public character sheet | `eve_character_overview`, corp resolution — `usecase/eve/account.go`, `usecase/session/session.go` | [GetCharactersCharacterId](https://developers.eveonline.com/api-explorer#/operations/GetCharactersCharacterId) |
| `GET /corporations/{corporation_id}` | public corp sheet | corp resolution — `usecase/session/session.go` | [GetCorporationsCorporationId](https://developers.eveonline.com/api-explorer#/operations/GetCorporationsCorporationId) |
| `POST /universe/names` | ids → names (shared id space) | name resolver — `adapter/names/names.go` | [PostUniverseNames](https://developers.eveonline.com/api-explorer#/operations/PostUniverseNames) |
| `POST /universe/ids` | names → ids | name resolver — `adapter/names/names.go` | [PostUniverseIds](https://developers.eveonline.com/api-explorer#/operations/PostUniverseIds) |
| `GET /universe/types/{type_id}` | item type detail | `eve_universe_item` via resolver — `adapter/names/names.go` | [GetUniverseTypesTypeId](https://developers.eveonline.com/api-explorer#/operations/GetUniverseTypesTypeId) |
| `GET /universe/groups/{group_id}` | group names (not in /universe/names!) | resolver `GroupName` — `adapter/names/names.go` | [GetUniverseGroupsGroupId](https://developers.eveonline.com/api-explorer#/operations/GetUniverseGroupsGroupId) |
| `GET /universe/systems/{system_id}` | system detail | `eve_universe_system`, `eve_universe_route` — `usecase/eve/universe.go` | [GetUniverseSystemsSystemId](https://developers.eveonline.com/api-explorer#/operations/GetUniverseSystemsSystemId) |
| `GET /universe/constellations/{constellation_id}` | region lookup | `eve_universe_system` — `usecase/eve/universe.go` | [GetUniverseConstellationsConstellationId](https://developers.eveonline.com/api-explorer#/operations/GetUniverseConstellationsConstellationId) |
| `GET /universe/system_kills` | recent kills per system | `eve_universe_system`, `eve_universe_hotspots` — `usecase/eve/universe.go` | [GetUniverseSystemKills](https://developers.eveonline.com/api-explorer#/operations/GetUniverseSystemKills) |
| `GET /universe/system_jumps` | traffic per system | `eve_universe_system` — `usecase/eve/universe.go` | [GetUniverseSystemJumps](https://developers.eveonline.com/api-explorer#/operations/GetUniverseSystemJumps) |
| `POST /route/{origin}/{destination}` | route planning; `preference` in the JSON body (compat-date ≥ 2025 replaced the old GET `flag` param) | `eve_universe_route` — `usecase/eve/universe.go` | [API Explorer → Routes](https://developers.eveonline.com/api-explorer) |
| `GET /markets/prices` | global average/adjusted prices | asset valuations — `adapter/names/names.go` | [GetMarketsPrices](https://developers.eveonline.com/api-explorer#/operations/GetMarketsPrices) |
| `GET /markets/{region_id}/orders` | live order book (paged ≤ 10) | `eve_market_price` hub quotes — `adapter/names/names.go` | [GetMarketsRegionIdOrders](https://developers.eveonline.com/api-explorer#/operations/GetMarketsRegionIdOrders) |
| `GET /markets/{region_id}/history` | daily price history | `eve_market_price` — `usecase/eve/market.go` | [GetMarketsRegionIdHistory](https://developers.eveonline.com/api-explorer#/operations/GetMarketsRegionIdHistory) |
| `GET /killmails/{killmail_id}/{killmail_hash}` | killmail detail | `eve_social_killmails`, `eve_corp_killmails` — `usecase/eve/social.go` | [GetKillmailsKillmailIdKillmailHash](https://developers.eveonline.com/api-explorer#/operations/GetKillmailsKillmailIdKillmailHash) |

## 2. Character endpoints (authenticated)

Scope names are the exact grant requested at EVE login
(`internal/domain/write/policy.go`).

Seven requested read scopes have no row in this document, because no
tool calls their endpoints yet: `read_agents_research`, `read_fatigue`,
`read_fw_stats`, `read_medals`, `read_titles`, `fleets.read_fleet`,
`markets.structure_markets`. Asking for them up front is deliberate
(SPEC §4.2): a scope added later signs every player out (SPEC §3.5).
The rule below is about endpoints, not scopes — a scope may sit unused,
an endpoint may not be called without a row here.

| Method & path | Scope | Used by (tool / repo source) | Official docs |
|---|---|---|---|
| `GET /characters/{id}/wallet` | `esi-wallet.read_character_wallet.v1` | `eve_character_overview` — `usecase/eve/account.go` | [GetCharactersCharacterIdWallet](https://developers.eveonline.com/api-explorer#/operations/GetCharactersCharacterIdWallet) |
| `GET /characters/{id}/wallet/journal` | `esi-wallet.read_character_wallet.v1` | `eve_wallet_history` (paged ≤ 10) — `usecase/eve/wallet.go` | [GetCharactersCharacterIdWalletJournal](https://developers.eveonline.com/api-explorer#/operations/GetCharactersCharacterIdWalletJournal) |
| `GET /characters/{id}/wallet/transactions` | `esi-wallet.read_character_wallet.v1` | `eve_wallet_history` (cursor `from_id`, ≤ 4 pages) — `usecase/eve/wallet.go` | [GetCharactersCharacterIdWalletTransactions](https://developers.eveonline.com/api-explorer#/operations/GetCharactersCharacterIdWalletTransactions) |
| `GET /characters/{id}/location` | `esi-location.read_location.v1` | `eve_character_overview` — `usecase/eve/account.go` | [GetCharactersCharacterIdLocation](https://developers.eveonline.com/api-explorer#/operations/GetCharactersCharacterIdLocation) |
| `GET /characters/{id}/ship` | `esi-location.read_ship_type.v1` | `eve_character_overview` — `usecase/eve/account.go` | [GetCharactersCharacterIdShip](https://developers.eveonline.com/api-explorer#/operations/GetCharactersCharacterIdShip) |
| `GET /characters/{id}/online` | `esi-location.read_online.v1` | `eve_character_overview` — `usecase/eve/account.go` | [GetCharactersCharacterIdOnline](https://developers.eveonline.com/api-explorer#/operations/GetCharactersCharacterIdOnline) |
| `GET /characters/{id}/skills` | `esi-skills.read_skills.v1` | `eve_character_skills` — `usecase/eve/character.go` | [GetCharactersCharacterIdSkills](https://developers.eveonline.com/api-explorer#/operations/GetCharactersCharacterIdSkills) |
| `GET /characters/{id}/skillqueue` | `esi-skills.read_skillqueue.v1` | `eve_character_skill_queue`, overview — `usecase/eve/character.go` | [GetCharactersCharacterIdSkillqueue](https://developers.eveonline.com/api-explorer#/operations/GetCharactersCharacterIdSkillqueue) |
| `GET /characters/{id}/attributes` | `esi-skills.read_skills.v1` | `eve_character_overview` — `usecase/eve/account.go` | [GetCharactersCharacterIdAttributes](https://developers.eveonline.com/api-explorer#/operations/GetCharactersCharacterIdAttributes) |
| `GET /characters/{id}/clones` | `esi-clones.read_clones.v1` | `eve_character_clones` — `usecase/eve/character.go` | [GetCharactersCharacterIdClones](https://developers.eveonline.com/api-explorer#/operations/GetCharactersCharacterIdClones) |
| `GET /characters/{id}/implants` | `esi-clones.read_implants.v1` | `eve_character_clones` — `usecase/eve/character.go` | [GetCharactersCharacterIdImplants](https://developers.eveonline.com/api-explorer#/operations/GetCharactersCharacterIdImplants) |
| `GET /characters/{id}/standings` | `esi-characters.read_standings.v1` | `eve_character_standings` — `usecase/eve/character.go` | [GetCharactersCharacterIdStandings](https://developers.eveonline.com/api-explorer#/operations/GetCharactersCharacterIdStandings) |
| `GET /characters/{id}/loyalty/points` | `esi-characters.read_loyalty.v1` | `eve_character_standings` — `usecase/eve/character.go` | [GetCharactersCharacterIdLoyaltyPoints](https://developers.eveonline.com/api-explorer#/operations/GetCharactersCharacterIdLoyaltyPoints) |
| `GET /characters/{id}/roles` | `esi-characters.read_corporation_roles.v1` | corp resolution — `usecase/session/session.go` | [GetCharactersCharacterIdRoles](https://developers.eveonline.com/api-explorer#/operations/GetCharactersCharacterIdRoles) |
| `GET /characters/{id}/assets` | `esi-assets.read_assets.v1` | `eve_assets_list`, `eve_assets_find` (paged ≤ 40) — `usecase/eve/assets.go` | [GetCharactersCharacterIdAssets](https://developers.eveonline.com/api-explorer#/operations/GetCharactersCharacterIdAssets) |
| `GET /characters/{id}/blueprints` | `esi-characters.read_blueprints.v1` | `eve_assets_blueprints` (paged ≤ 40) — `usecase/eve/assets.go` | [GetCharactersCharacterIdBlueprints](https://developers.eveonline.com/api-explorer#/operations/GetCharactersCharacterIdBlueprints) |
| `GET /characters/{id}/industry/jobs` | `esi-industry.read_character_jobs.v1` | `eve_industry_jobs` — `usecase/eve/industry.go` | [GetCharactersCharacterIdIndustryJobs](https://developers.eveonline.com/api-explorer#/operations/GetCharactersCharacterIdIndustryJobs) |
| `GET /characters/{id}/planets` | `esi-planets.manage_planets.v1` | `eve_industry_planets` — `usecase/eve/industry.go` | [GetCharactersCharacterIdPlanets](https://developers.eveonline.com/api-explorer#/operations/GetCharactersCharacterIdPlanets) |
| `GET /characters/{id}/planets/{planet_id}` | `esi-planets.manage_planets.v1` | `eve_industry_planets` (detailed) — `usecase/eve/industry.go` | [GetCharactersCharacterIdPlanetsPlanetId](https://developers.eveonline.com/api-explorer#/operations/GetCharactersCharacterIdPlanetsPlanetId) |
| `GET /characters/{id}/mining` | `esi-industry.read_character_mining.v1` | `eve_industry_mining` (paged ≤ 40) — `usecase/eve/industry.go` | [GetCharactersCharacterIdMining](https://developers.eveonline.com/api-explorer#/operations/GetCharactersCharacterIdMining) |
| `GET /characters/{id}/orders` | `esi-markets.read_character_orders.v1` | `eve_market_orders` — `usecase/eve/market.go` | [GetCharactersCharacterIdOrders](https://developers.eveonline.com/api-explorer#/operations/GetCharactersCharacterIdOrders) |
| `GET /characters/{id}/contracts` | `esi-contracts.read_character_contracts.v1` | `eve_market_contracts` (paged ≤ 40) — `usecase/eve/market.go` | [GetCharactersCharacterIdContracts](https://developers.eveonline.com/api-explorer#/operations/GetCharactersCharacterIdContracts) |
| `GET /characters/{id}/mail` | `esi-mail.read_mail.v1` | `eve_mail_list` — `usecase/eve/social.go` | [GetCharactersCharacterIdMail](https://developers.eveonline.com/api-explorer#/operations/GetCharactersCharacterIdMail) |
| `GET /characters/{id}/mail/{mail_id}` | `esi-mail.read_mail.v1` | `eve_mail_read`; delete preview — `usecase/eve/social.go`, `usecase/eve/writes.go` | [GetCharactersCharacterIdMailMailId](https://developers.eveonline.com/api-explorer#/operations/GetCharactersCharacterIdMailMailId) |
| `GET /characters/{id}/notifications` | `esi-characters.read_notifications.v1` | `eve_social_notifications` — `usecase/eve/social.go` | [GetCharactersCharacterIdNotifications](https://developers.eveonline.com/api-explorer#/operations/GetCharactersCharacterIdNotifications) |
| `GET /characters/{id}/killmails/recent` | `esi-killmails.read_killmails.v1` | `eve_social_killmails` — `usecase/eve/social.go` | [GetCharactersCharacterIdKillmailsRecent](https://developers.eveonline.com/api-explorer#/operations/GetCharactersCharacterIdKillmailsRecent) |
| `GET /characters/{id}/fittings` | `esi-fittings.read_fittings.v1` | `eve_fitting_list`; save/delete previews — `usecase/eve/social.go`, `usecase/eve/writes.go` | [GetCharactersCharacterIdFittings](https://developers.eveonline.com/api-explorer#/operations/GetCharactersCharacterIdFittings) |
| `GET /characters/{id}/search` | `esi-search.search_structures.v1` | `eve_universe_search`; waypoint/window name resolution — `usecase/eve/universe.go`, `usecase/eve/writes.go` | [GetCharactersCharacterIdSearch](https://developers.eveonline.com/api-explorer#/operations/GetCharactersCharacterIdSearch) |
| `GET /characters/{id}/calendar/{event_id}` | `esi-calendar.read_calendar_events.v1` | `eve_calendar_respond` preview — `usecase/eve/writes.go` | [GetCharactersCharacterIdCalendarEventId](https://developers.eveonline.com/api-explorer#/operations/GetCharactersCharacterIdCalendarEventId) |
| `GET /characters/{id}/contacts` | `esi-characters.read_contacts.v1` | `eve_contacts_set`/`_delete` previews (paged ≤ 40) — `usecase/eve/writes.go` | [GetCharactersCharacterIdContacts](https://developers.eveonline.com/api-explorer#/operations/GetCharactersCharacterIdContacts) |
| `GET /universe/structures/{structure_id}` | `esi-universe.read_structures.v1` | name resolver, ids ≥ 10¹² — `adapter/names/names.go` | [GetUniverseStructuresStructureId](https://developers.eveonline.com/api-explorer#/operations/GetUniverseStructuresStructureId) |

## 3. Corporation endpoints (authenticated + in-game role)

Role gates per SPEC §4.2. Note CCP's inconsistency: mining lives under
`/corporation/` (singular).

| Method & path | Scope | Used by (tool / repo source) | Official docs |
|---|---|---|---|
| `GET /corporations/{id}/wallets` | `esi-wallet.read_corporation_wallets.v1` | `eve_corp_overview`, `eve_corp_wallet` — `usecase/eve/corp.go` | [GetCorporationsCorporationIdWallets](https://developers.eveonline.com/api-explorer#/operations/GetCorporationsCorporationIdWallets) |
| `GET /corporations/{id}/wallets/{division}/journal` | `esi-wallet.read_corporation_wallets.v1` | `eve_corp_wallet` (paged ≤ 10) — `usecase/eve/corp.go` | [GetCorporationsCorporationIdWalletsDivisionJournal](https://developers.eveonline.com/api-explorer#/operations/GetCorporationsCorporationIdWalletsDivisionJournal) |
| `GET /corporations/{id}/wallets/{division}/transactions` | `esi-wallet.read_corporation_wallets.v1` | `eve_corp_wallet` — `usecase/eve/corp.go` | [GetCorporationsCorporationIdWalletsDivisionTransactions](https://developers.eveonline.com/api-explorer#/operations/GetCorporationsCorporationIdWalletsDivisionTransactions) |
| `GET /corporations/{id}/assets` | `esi-assets.read_corporation_assets.v1` | `eve_corp_assets_list`, `eve_corp_assets_find` (paged ≤ 80) — `usecase/eve/corp.go` | [GetCorporationsCorporationIdAssets](https://developers.eveonline.com/api-explorer#/operations/GetCorporationsCorporationIdAssets) |
| `GET /corporations/{id}/blueprints` | `esi-corporations.read_blueprints.v1` | `eve_corp_blueprints` (paged ≤ 40) — `usecase/eve/corp.go` | [GetCorporationsCorporationIdBlueprints](https://developers.eveonline.com/api-explorer#/operations/GetCorporationsCorporationIdBlueprints) |
| `GET /corporations/{id}/industry/jobs` | `esi-industry.read_corporation_jobs.v1` | `eve_corp_industry_jobs` (paged ≤ 40) — `usecase/eve/corp.go` | [GetCorporationsCorporationIdIndustryJobs](https://developers.eveonline.com/api-explorer#/operations/GetCorporationsCorporationIdIndustryJobs) |
| `GET /corporations/{id}/orders` | `esi-markets.read_corporation_orders.v1` | `eve_corp_orders` (paged ≤ 40) — `usecase/eve/corp.go` | [GetCorporationsCorporationIdOrders](https://developers.eveonline.com/api-explorer#/operations/GetCorporationsCorporationIdOrders) |
| `GET /corporations/{id}/contracts` | `esi-contracts.read_corporation_contracts.v1` | `eve_corp_contracts` (paged ≤ 40) — `usecase/eve/corp.go` | [GetCorporationsCorporationIdContracts](https://developers.eveonline.com/api-explorer#/operations/GetCorporationsCorporationIdContracts) |
| `GET /corporations/{id}/structures` | `esi-corporations.read_structures.v1` | `eve_corp_structures` (paged ≤ 40) — `usecase/eve/corp.go` | [GetCorporationsCorporationIdStructures](https://developers.eveonline.com/api-explorer#/operations/GetCorporationsCorporationIdStructures) |
| `GET /corporations/{id}/members` | `esi-corporations.read_corporation_membership.v1` | `eve_corp_members` (paged ≤ 40) — `usecase/eve/corp.go` | [GetCorporationsCorporationIdMembers](https://developers.eveonline.com/api-explorer#/operations/GetCorporationsCorporationIdMembers) |
| `GET /corporations/{id}/roles` | `esi-characters.read_corporation_roles.v1` | `eve_corp_members` (detailed) — `usecase/eve/corp.go` | [GetCorporationsCorporationIdRoles](https://developers.eveonline.com/api-explorer#/operations/GetCorporationsCorporationIdRoles) |
| `GET /corporations/{id}/divisions` | `esi-corporations.read_divisions.v1` | `eve_corp_wallet`, `eve_corp_overview` — `usecase/eve/corp.go` | [GetCorporationsCorporationIdDivisions](https://developers.eveonline.com/api-explorer#/operations/GetCorporationsCorporationIdDivisions) |
| `GET /corporations/{id}/killmails/recent` | `esi-killmails.read_corporation_killmails.v1` | `eve_corp_killmails` — `usecase/eve/corp.go` | [GetCorporationsCorporationIdKillmailsRecent](https://developers.eveonline.com/api-explorer#/operations/GetCorporationsCorporationIdKillmailsRecent) |
| `GET /corporation/{id}/mining/extractions` | `esi-industry.read_corporation_mining.v1` | `eve_corp_mining` — `usecase/eve/corp.go` | [GetCorporationCorporationIdMiningExtractions](https://developers.eveonline.com/api-explorer#/operations/GetCorporationCorporationIdMiningExtractions) |
| `GET /corporation/{id}/mining/observers` | `esi-industry.read_corporation_mining.v1` | `eve_corp_mining` (paged ≤ 40) — `usecase/eve/corp.go` | [GetCorporationCorporationIdMiningObservers](https://developers.eveonline.com/api-explorer#/operations/GetCorporationCorporationIdMiningObservers) |
| `GET /corporation/{id}/mining/observers/{observer_id}` | `esi-industry.read_corporation_mining.v1` | `eve_corp_mining` (paged ≤ 10) — `usecase/eve/corp.go` | [GetCorporationCorporationIdMiningObserversObserverId](https://developers.eveonline.com/api-explorer#/operations/GetCorporationCorporationIdMiningObserversObserverId) |

## 4. Write endpoints (authenticated, confirm flow)

| Method & path | Scope | Used by (tool / repo source) | Official docs |
|---|---|---|---|
| `POST /ui/autopilot/waypoint` | `esi-ui.write_waypoint.v1` | `eve_ui_set_waypoint` — `usecase/eve/writes.go` | [PostUiAutopilotWaypoint](https://developers.eveonline.com/api-explorer#/operations/PostUiAutopilotWaypoint) |
| `POST /ui/openwindow/marketdetails` | `esi-ui.open_window.v1` | `eve_ui_open_window` — `usecase/eve/writes.go` | [PostUiOpenwindowMarketdetails](https://developers.eveonline.com/api-explorer#/operations/PostUiOpenwindowMarketdetails) |
| `POST /ui/openwindow/information` | `esi-ui.open_window.v1` | `eve_ui_open_window` — `usecase/eve/writes.go` | [PostUiOpenwindowInformation](https://developers.eveonline.com/api-explorer#/operations/PostUiOpenwindowInformation) |
| `POST /ui/openwindow/contract` | `esi-ui.open_window.v1` | `eve_ui_open_window` — `usecase/eve/writes.go` | [PostUiOpenwindowContract](https://developers.eveonline.com/api-explorer#/operations/PostUiOpenwindowContract) |
| `POST /ui/openwindow/newmail` | `esi-ui.open_window.v1` | `eve_ui_open_window` — `usecase/eve/writes.go` | [PostUiOpenwindowNewmail](https://developers.eveonline.com/api-explorer#/operations/PostUiOpenwindowNewmail) |
| `POST /characters/{id}/fittings` | `esi-fittings.write_fittings.v1` | `eve_fitting_save` — `usecase/eve/writes.go` | [PostCharactersCharacterIdFittings](https://developers.eveonline.com/api-explorer#/operations/PostCharactersCharacterIdFittings) |
| `DELETE /characters/{id}/fittings/{fitting_id}` | `esi-fittings.write_fittings.v1` | `eve_fitting_delete` — `usecase/eve/writes.go` | [DeleteCharactersCharacterIdFittingsFittingId](https://developers.eveonline.com/api-explorer#/operations/DeleteCharactersCharacterIdFittingsFittingId) |
| `PUT /characters/{id}/mail/{mail_id}` | `esi-mail.organize_mail.v1` | `eve_mail_mark` — `usecase/eve/writes.go` | [PutCharactersCharacterIdMailMailId](https://developers.eveonline.com/api-explorer#/operations/PutCharactersCharacterIdMailMailId) |
| `DELETE /characters/{id}/mail/{mail_id}` | `esi-mail.organize_mail.v1` | `eve_mail_delete` — `usecase/eve/writes.go` | [DeleteCharactersCharacterIdMailMailId](https://developers.eveonline.com/api-explorer#/operations/DeleteCharactersCharacterIdMailMailId) |
| `POST /characters/{id}/mail` | `esi-mail.send_mail.v1` | `eve_mail_send` (per-character hourly cap) — `usecase/eve/writes.go` | [PostCharactersCharacterIdMail](https://developers.eveonline.com/api-explorer#/operations/PostCharactersCharacterIdMail) |
| `POST /characters/{id}/contacts` | `esi-characters.write_contacts.v1` | `eve_contacts_set` (add) — `usecase/eve/writes.go` | [PostCharactersCharacterIdContacts](https://developers.eveonline.com/api-explorer#/operations/PostCharactersCharacterIdContacts) |
| `PUT /characters/{id}/contacts` | `esi-characters.write_contacts.v1` | `eve_contacts_set` (update) — `usecase/eve/writes.go` | [PutCharactersCharacterIdContacts](https://developers.eveonline.com/api-explorer#/operations/PutCharactersCharacterIdContacts) |
| `DELETE /characters/{id}/contacts` | `esi-characters.write_contacts.v1` | `eve_contacts_delete` — `usecase/eve/writes.go` | [DeleteCharactersCharacterIdContacts](https://developers.eveonline.com/api-explorer#/operations/DeleteCharactersCharacterIdContacts) |
| `PUT /characters/{id}/calendar/{event_id}` | `esi-calendar.respond_calendar_events.v1` | `eve_calendar_respond` — `usecase/eve/writes.go` | [PutCharactersCharacterIdCalendarEventId](https://developers.eveonline.com/api-explorer#/operations/PutCharactersCharacterIdCalendarEventId) |

## 5. EVE SSO endpoints (login.eveonline.com)

Used by `internal/adapter/sso/sso.go` (the only file that may talk to
the SSO). Documented in the official SSO guide.

| Method & URL | Purpose | Official docs |
|---|---|---|
| `GET https://login.eveonline.com/v2/oauth/authorize` | browser login, PKCE S256 | [Authorization code flow](https://developers.eveonline.com/docs/services/sso/) · [Web based SSO flow](https://docs.esi.evetech.net/docs/sso/web_based_sso_flow.html) |
| `POST https://login.eveonline.com/v2/oauth/token` | code → tokens; refresh | [Token exchange](https://docs.esi.evetech.net/docs/sso/web_based_sso_flow.html) · [Refreshing tokens](https://docs.esi.evetech.net/docs/sso/refreshing_access_tokens.html) |
| `POST https://login.eveonline.com/v2/oauth/revoke` | revoke refresh token (`eve_auth_logout`) | [SSO guide](https://developers.eveonline.com/docs/services/sso/) |
| `GET https://login.eveonline.com/oauth/jwks` | keys to validate EVE JWTs | [Validating JWT tokens](https://docs.esi.evetech.net/docs/sso/validating_eve_jwt.html) |

## 6. Rules that apply to every row

- Anything not listed here is off-limits; adding an endpoint means
  adding a row **in the same commit** (with the repo source and the
  API Explorer link).
- ESI search (`GET /characters/{id}/search`) matches on **prefix**, not
  fuzzily — the search tool compensates by shortening and retrying.
- `/universe/names` is one shared id space; group ids must go through
  `GET /universe/groups/{group_id}`.
- Error-limit headers (`X-Esi-Error-Limit-Remain/Reset`) are honoured on
  every response; `420`/`429` handling per SPEC §5.1.
- Every response with status ≥ 400 is charged to the character whose
  tool call produced it (SPEC §5.3). An endpoint that a character's
  in-game roles do not allow costs them their own error budget, not the
  instance's.
- Page caps per call site are part of this contract (they bound the
  worst-case cost of one tool call against the per-character allowance,
  SPEC §5.2).
