package write

import (
	"sort"
	"time"
)

const (
	// Rolling hour, per user — not a daily budget.
	MailCap    = 5
	ConfirmTTL = 300 * time.Second

	CapMailSend = "mail_send"
)

type Capability struct {
	Name          string
	Scopes        []string
	Summary       string
	OutwardFacing bool
}

func Capabilities() map[string]Capability {
	return map[string]Capability{
		"waypoint": {
			Name: "waypoint", Scopes: []string{"esi-ui.write_waypoint.v1"},
			Summary: "Set autopilot waypoints in the running game client.",
		},
		"openwindow": {
			Name: "openwindow", Scopes: []string{"esi-ui.open_window.v1"},
			Summary: "Open market / info / contract / new-mail windows in the client.",
		},
		"fittings": {
			Name: "fittings", Scopes: []string{"esi-fittings.write_fittings.v1"},
			Summary: "Save and delete saved ship fittings.",
		},
		"calendar": {
			Name: "calendar", Scopes: []string{"esi-calendar.respond_calendar_events.v1"},
			Summary: "Respond to calendar events (accept / decline / tentative).", OutwardFacing: true,
		},
		"mail_organize": {
			Name: "mail_organize", Scopes: []string{"esi-mail.organize_mail.v1"},
			Summary: "Mark mail read, manage labels, delete mail.",
		},
		CapMailSend: {
			Name: CapMailSend, Scopes: []string{"esi-mail.send_mail.v1"},
			Summary: "Send in-game EVE mail to other players.", OutwardFacing: true,
		},
		"contacts": {
			Name: "contacts", Scopes: []string{"esi-characters.write_contacts.v1"},
			Summary: "Add, edit and delete character contacts and standings.", OutwardFacing: true,
		},
	}
}

func ReadScopes() []string {
	return []string{
		"esi-assets.read_assets.v1",
		"esi-calendar.read_calendar_events.v1",
		"esi-characters.read_agents_research.v1",
		"esi-characters.read_blueprints.v1",
		"esi-characters.read_contacts.v1",
		"esi-characters.read_corporation_roles.v1",
		"esi-characters.read_fatigue.v1",
		"esi-characters.read_fw_stats.v1",
		"esi-characters.read_loyalty.v1",
		"esi-characters.read_medals.v1",
		"esi-characters.read_notifications.v1",
		"esi-characters.read_standings.v1",
		"esi-characters.read_titles.v1",
		"esi-clones.read_clones.v1",
		"esi-clones.read_implants.v1",
		"esi-contracts.read_character_contracts.v1",
		"esi-fittings.read_fittings.v1",
		"esi-fleets.read_fleet.v1",
		"esi-industry.read_character_jobs.v1",
		"esi-industry.read_character_mining.v1",
		"esi-killmails.read_killmails.v1",
		"esi-location.read_location.v1",
		"esi-location.read_online.v1",
		"esi-location.read_ship_type.v1",
		"esi-mail.read_mail.v1",
		"esi-markets.read_character_orders.v1",
		"esi-markets.structure_markets.v1",
		"esi-planets.manage_planets.v1",
		"esi-search.search_structures.v1",
		"esi-skills.read_skillqueue.v1",
		"esi-skills.read_skills.v1",
		"esi-universe.read_structures.v1",
		"esi-wallet.read_character_wallet.v1",
	}
}

func CorpReadScopes() []string {
	return []string{
		"esi-assets.read_corporation_assets.v1",
		"esi-contracts.read_corporation_contracts.v1",
		"esi-corporations.read_blueprints.v1",
		"esi-corporations.read_corporation_membership.v1",
		"esi-corporations.read_divisions.v1",
		"esi-corporations.read_structures.v1",
		"esi-industry.read_corporation_jobs.v1",
		"esi-industry.read_corporation_mining.v1",
		"esi-killmails.read_corporation_killmails.v1",
		"esi-markets.read_corporation_orders.v1",
		"esi-wallet.read_corporation_wallets.v1",
	}
}

func AllWriteScopeSet() map[string]struct{} {
	out := map[string]struct{}{}
	for _, cap := range Capabilities() {
		for _, s := range cap.Scopes {
			out[s] = struct{}{}
		}
	}

	return out
}

func CorpScopeSet() map[string]struct{} {
	out := map[string]struct{}{}
	for _, s := range CorpReadScopes() {
		out[s] = struct{}{}
	}

	return out
}

func CapabilityNames() []string {
	out := make([]string, 0, len(Capabilities()))
	for name := range Capabilities() {
		out = append(out, name)
	}
	sort.Strings(out)

	return out
}

func RequestedScopes() []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(scopes []string) {
		for _, sc := range scopes {
			if _, ok := seen[sc]; ok {
				continue
			}
			seen[sc] = struct{}{}
			out = append(out, sc)
		}
	}
	add(ReadScopes())
	add(CorpReadScopes())
	for _, name := range CapabilityNames() {
		add(Capabilities()[name].Scopes)
	}

	return out
}

func MissingScopes(granted []string) []string {
	have := make(map[string]struct{}, len(granted))
	for _, s := range granted {
		have[s] = struct{}{}
	}
	var missing []string
	for _, s := range RequestedScopes() {
		if _, ok := have[s]; !ok {
			missing = append(missing, s)
		}
	}
	sort.Strings(missing)

	return missing
}
