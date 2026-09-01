package tests

const (
	minDescriptionChars     = 120
	maxDescriptionChars     = 2000
	maxDefaultResponseChars = 6000
	errorPreview            = 60

	fieldName        = "name"
	fieldDescription = "description"
	fieldInputSchema = "inputSchema"
	fieldProperties  = "properties"
	fieldType        = "type"
	fieldLimit       = "limit"
	typeInteger      = "integer"
)

func needsResponseFormat(name string) bool {
	switch name {
	case "eve_character_standings", "eve_industry_mining", "eve_universe_search", "eve_universe_hotspots":
		return false
	default:
		return true
	}
}

func skipInSmoke(name string) bool {
	switch name {
	case "eve_auth_logout", "eve_mail_read",
		"eve_ui_set_waypoint", "eve_ui_open_window",
		"eve_fitting_save", "eve_fitting_delete",
		"eve_mail_mark", "eve_mail_delete", "eve_mail_compose", "eve_mail_send",
		"eve_contacts_set", "eve_contacts_delete", "eve_calendar_respond":
		return true
	case "eve_corp_assets_list", "eve_corp_assets_find", "eve_corp_blueprints",
		"eve_corp_wallet", "eve_corp_industry_jobs", "eve_corp_mining",
		"eve_corp_orders", "eve_corp_contracts", "eve_corp_killmails",
		"eve_corp_structures", "eve_corp_members":
		return true
	default:
		return false
	}
}

func smokeArgs(name string) map[string]any {
	switch name {
	case "eve_market_price":
		return map[string]any{"item": "Tritanium"}
	case "eve_universe_item":
		return map[string]any{"item": "Rifter"}
	case "eve_universe_system":
		return map[string]any{"system": "Jita"}
	case "eve_universe_route":
		return map[string]any{"origin": "Jita", "destination": "Amarr"}
	case "eve_universe_search":
		return map[string]any{"query": "Rifter"}
	case "eve_assets_find":
		return map[string]any{"name": "Drake"}
	default:
		return nil
	}
}
