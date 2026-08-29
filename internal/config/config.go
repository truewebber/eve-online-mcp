package config

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const (
	ESIBase           = "https://esi.evetech.net"
	SSOBase           = "https://login.eveonline.com"
	AuthorizeURL      = SSOBase + "/v2/oauth/authorize"
	TokenURL          = SSOBase + "/v2/oauth/token"
	RevokeURL         = SSOBase + "/v2/oauth/revoke"
	JWKSURL           = SSOBase + "/oauth/jwks"
	TokenAudience     = "EVE Online"
	DefaultCompatDate = "2026-08-18"
	TheForgeRegionID  = 10000002
	Jita44StationID   = 60003760
	Version           = "0.2.0"
)

var TokenIssuers = []string{"login.eveonline.com", "https://login.eveonline.com"}

var ReadScopes = []string{
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

var CorpReadScopes = []string{
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

type WriteCapability struct {
	Name          string
	Scopes        []string
	Summary       string
	OutwardFacing bool
}

var WriteCapabilities = map[string]WriteCapability{
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
	"mail_send": {
		Name: "mail_send", Scopes: []string{"esi-mail.send_mail.v1"},
		Summary: "Send in-game EVE mail to other players. Off by default.", OutwardFacing: true,
	},
	"contacts": {
		Name: "contacts", Scopes: []string{"esi-characters.write_contacts.v1"},
		Summary: "Add, edit and delete character contacts and standings. Off by default.", OutwardFacing: true,
	},
}

var DefaultWriteAllow = []string{"waypoint", "openwindow", "fittings", "mail_organize"}

type File struct {
	ClientID           string   `toml:"client_id"`
	ClientSecret       string   `toml:"client_secret,omitempty"`
	Contact            string   `toml:"contact"`
	CallbackURL        string   `toml:"callback_url,omitempty"`
	PublicURL          string   `toml:"public_url,omitempty"`
	Listen             string   `toml:"listen,omitempty"`
	MCPToken           string   `toml:"mcp_token,omitempty"`
	WriteMode          string   `toml:"write_mode,omitempty"`
	WriteAllow         []string `toml:"write_allow,omitempty"`
	CorpScopes         bool     `toml:"corp_scopes,omitempty"`
	WriteBudgetPerHour int      `toml:"write_budget_per_hour,omitempty"`
	MailBudgetPerHour  int      `toml:"mail_budget_per_hour,omitempty"`
	ConfirmTTLSeconds  int      `toml:"confirm_ttl_seconds,omitempty"`
	CompatDate         string   `toml:"compat_date,omitempty"`
	RequestTimeoutSec  float64  `toml:"request_timeout_seconds,omitempty"`
	MaxConcurrency     int      `toml:"max_concurrency,omitempty"`
}

type Settings struct {
	ClientID           string
	ClientSecret       string
	CallbackURL        string
	PublicURL          string
	UserAgent          string
	CompatDate         string
	DataDir            string
	ConfigPath         string
	Listen             string
	MCPPath            string
	BearerToken        string
	WriteMode          string
	WriteAllow         map[string]struct{}
	CorpScopes         bool
	WriteBudgetPerHour int
	MailBudgetPerHour  int
	ConfirmTTLSeconds  int
	RequestTimeoutSec  float64
	MaxConcurrency     int
}

func (s Settings) TokenFile() string { return filepath.Join(s.DataDir, "tokens.json") }
func (s Settings) CacheFile() string { return filepath.Join(s.DataDir, "cache.sqlite3") }
func (s Settings) AuditFile() string { return filepath.Join(s.DataDir, "audit.jsonl") }

func (s Settings) WritesEnabled() bool {
	return s.WriteMode != "off" && len(s.WriteAllow) > 0
}

func (s Settings) CapabilityEnabled(name string) bool {
	if s.WriteMode == "off" {
		return false
	}
	_, ok := s.WriteAllow[name]
	return ok
}

func (s Settings) RequestedScopes() []string {
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
	add(ReadScopes)
	if s.CorpScopes {
		add(CorpReadScopes)
	}
	if s.WriteMode != "off" {
		for name := range s.WriteAllow {
			if cap, ok := WriteCapabilities[name]; ok {
				add(cap.Scopes)
			}
		}
	}
	return out
}

func (s Settings) AllowedNames() []string {
	out := make([]string, 0, len(s.WriteAllow))
	for name := range s.WriteAllow {
		out = append(out, name)
	}
	return out
}

func (s Settings) HostPort() (host string, port int) {
	host, portStr, err := net.SplitHostPort(s.Listen)
	if err != nil {
		return "127.0.0.1", 8765
	}
	port, _ = strconv.Atoi(portStr)
	if port == 0 {
		port = 8765
	}
	return host, port
}

func (s Settings) Loopback() bool {
	host, _ := s.HostPort()
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

func (s Settings) BaseURL() string {
	if s.PublicURL != "" {
		return strings.TrimRight(s.PublicURL, "/")
	}
	host, port := s.HostPort()
	if host == "0.0.0.0" || host == "::" || host == "" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s:%d", host, port)
}

func DefaultDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "eve-mcp"), nil
}

func DefaultConfigPath() (string, error) {
	dir, err := DefaultDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.toml"), nil
}

func Load(configPath, listenOverride, publicURLOverride string) (*Settings, bool, error) {
	if configPath == "" {
		if env := strings.TrimSpace(os.Getenv("EVE_CONFIG")); env != "" {
			configPath = env
		} else {
			p, err := DefaultConfigPath()
			if err != nil {
				return nil, false, err
			}
			configPath = p
		}
	}
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, false, err
	}

	file := File{
		Listen:             "127.0.0.1:8765",
		WriteMode:          "confirm",
		WriteAllow:         append([]string{}, DefaultWriteAllow...),
		CorpScopes:         true,
		WriteBudgetPerHour: 40,
		MailBudgetPerHour:  5,
		ConfirmTTLSeconds:  300,
		CompatDate:         DefaultCompatDate,
		RequestTimeoutSec:  30,
		MaxConcurrency:     8,
	}

	created := false
	raw, err := os.ReadFile(configPath)
	switch {
	case err == nil:
		if err := toml.Unmarshal(raw, &file); err != nil {
			return nil, false, fmt.Errorf("parse %s: %w", configPath, err)
		}
	case os.IsNotExist(err):
		created = true
	default:
		return nil, false, err
	}

	imported := false
	if strings.TrimSpace(file.ClientID) == "" {
		if envMap := tryImportDotEnv(); len(envMap) > 0 && envMap["EVE_CLIENT_ID"] != "" {
			file.ClientID = envMap["EVE_CLIENT_ID"]
			file.ClientSecret = envMap["EVE_CLIENT_SECRET"]
			if c := envMap["EVE_CONTACT"]; c != "" {
				file.Contact = c
			}
			if v := envMap["EVE_WRITE_MODE"]; v != "" {
				file.WriteMode = v
			}
			if v := envMap["EVE_WRITE_ALLOW"]; v != "" {
				file.WriteAllow = splitCSV(v)
			}
			if v := envMap["EVE_CORP_SCOPES"]; v != "" {
				file.CorpScopes = parseBool(v, file.CorpScopes)
			}
			imported = true
		}
	}

	overlayEnv(&file)
	if listenOverride != "" {
		file.Listen = listenOverride
	}
	if publicURLOverride != "" {
		file.PublicURL = strings.TrimRight(publicURLOverride, "/")
	}

	if file.Listen == "" {
		file.Listen = "127.0.0.1:8765"
	}
	if file.WriteMode == "" {
		file.WriteMode = "confirm"
	}
	if file.WriteMode != "off" && file.WriteMode != "confirm" && file.WriteMode != "on" {
		return nil, false, fmt.Errorf("write_mode must be off, confirm or on")
	}
	if file.WriteBudgetPerHour == 0 {
		file.WriteBudgetPerHour = 40
	}
	if file.MailBudgetPerHour == 0 {
		file.MailBudgetPerHour = 5
	}
	if file.ConfirmTTLSeconds == 0 {
		file.ConfirmTTLSeconds = 300
	}
	if file.CompatDate == "" {
		file.CompatDate = DefaultCompatDate
	}
	if file.RequestTimeoutSec == 0 {
		file.RequestTimeoutSec = 30
	}
	if file.MaxConcurrency == 0 {
		file.MaxConcurrency = 8
	}
	if len(file.WriteAllow) == 0 && file.WriteMode != "off" {
		file.WriteAllow = append([]string{}, DefaultWriteAllow...)
	}

	host, port, err := net.SplitHostPort(file.Listen)
	if err != nil {
		return nil, false, fmt.Errorf("listen %q: %w", file.Listen, err)
	}
	_ = host
	if file.CallbackURL == "" {
		if file.PublicURL != "" {
			file.CallbackURL = file.PublicURL + "/auth/callback"
		} else {
			file.CallbackURL = fmt.Sprintf("http://127.0.0.1:%s/auth/callback", port)
		}
	}

	loopback := host == "127.0.0.1" || host == "localhost" || host == "::1"
	tokenGenerated := false
	if !loopback && strings.TrimSpace(file.MCPToken) == "" {
		file.MCPToken = randomToken()
		tokenGenerated = true
	}

	if created || imported || tokenGenerated {
		if err := SaveFile(configPath, file); err != nil {
			return nil, false, err
		}
	}

	contact := strings.TrimSpace(file.Contact)
	ua := strings.TrimSpace(os.Getenv("EVE_USER_AGENT"))
	if ua == "" {
		if contact != "" {
			ua = "eve-mcp/" + Version + " " + contact
		} else {
			ua = "eve-mcp/" + Version
		}
	}

	allow := map[string]struct{}{}
	switch {
	case len(file.WriteAllow) == 1 && file.WriteAllow[0] == "all":
		for name := range WriteCapabilities {
			allow[name] = struct{}{}
		}
	case len(file.WriteAllow) == 1 && (file.WriteAllow[0] == "none" || file.WriteAllow[0] == ""):
		// empty
	default:
		for _, name := range file.WriteAllow {
			if _, ok := WriteCapabilities[name]; !ok {
				return nil, false, fmt.Errorf("unknown write_allow entry %q", name)
			}
			allow[name] = struct{}{}
		}
	}

	s := &Settings{
		ClientID:           strings.TrimSpace(file.ClientID),
		ClientSecret:       strings.TrimSpace(file.ClientSecret),
		CallbackURL:        file.CallbackURL,
		PublicURL:          file.PublicURL,
		UserAgent:          ua,
		CompatDate:         file.CompatDate,
		DataDir:            dir,
		ConfigPath:         configPath,
		Listen:             file.Listen,
		MCPPath:            "/mcp",
		BearerToken:        strings.TrimSpace(file.MCPToken),
		WriteMode:          file.WriteMode,
		WriteAllow:         allow,
		CorpScopes:         file.CorpScopes,
		WriteBudgetPerHour: file.WriteBudgetPerHour,
		MailBudgetPerHour:  file.MailBudgetPerHour,
		ConfirmTTLSeconds:  file.ConfirmTTLSeconds,
		RequestTimeoutSec:  file.RequestTimeoutSec,
		MaxConcurrency:     file.MaxConcurrency,
	}
	return s, tokenGenerated, nil
}

func SaveFile(path string, file File) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := toml.Marshal(file)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func SaveSettings(s *Settings) error {
	file := File{
		ClientID:           s.ClientID,
		ClientSecret:       s.ClientSecret,
		Contact:            extractContact(s.UserAgent),
		CallbackURL:        s.CallbackURL,
		PublicURL:          s.PublicURL,
		Listen:             s.Listen,
		MCPToken:           s.BearerToken,
		WriteMode:          s.WriteMode,
		WriteAllow:         s.AllowedNames(),
		CorpScopes:         s.CorpScopes,
		WriteBudgetPerHour: s.WriteBudgetPerHour,
		MailBudgetPerHour:  s.MailBudgetPerHour,
		ConfirmTTLSeconds:  s.ConfirmTTLSeconds,
		CompatDate:         s.CompatDate,
		RequestTimeoutSec:  s.RequestTimeoutSec,
		MaxConcurrency:     s.MaxConcurrency,
	}
	return SaveFile(s.ConfigPath, file)
}

func extractContact(ua string) string {
	parts := strings.Fields(ua)
	if len(parts) >= 2 && strings.Contains(parts[len(parts)-1], "@") {
		return parts[len(parts)-1]
	}
	return ""
}

func overlayEnv(file *File) {
	if v := os.Getenv("EVE_CLIENT_ID"); v != "" {
		file.ClientID = v
	}
	if v := os.Getenv("EVE_CLIENT_SECRET"); v != "" {
		file.ClientSecret = v
	}
	if v := os.Getenv("EVE_CONTACT"); v != "" {
		file.Contact = v
	}
	if v := os.Getenv("EVE_CALLBACK_URL"); v != "" {
		file.CallbackURL = v
	}
	if v := os.Getenv("EVE_PUBLIC_URL"); v != "" {
		file.PublicURL = strings.TrimRight(v, "/")
	}
	if v := os.Getenv("EVE_LISTEN"); v != "" {
		file.Listen = v
	}
	if v := os.Getenv("EVE_MCP_TOKEN"); v != "" {
		file.MCPToken = v
	}
	if v := os.Getenv("EVE_WRITE_MODE"); v != "" {
		file.WriteMode = v
	}
	if v := os.Getenv("EVE_WRITE_ALLOW"); v != "" {
		file.WriteAllow = splitCSV(v)
	}
	if v := os.Getenv("EVE_CORP_SCOPES"); v != "" {
		file.CorpScopes = parseBool(v, file.CorpScopes)
	}
}

func tryImportDotEnv() map[string]string {
	candidates := []string{".env"}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, "eve_mcp", ".env"))
	}
	for _, path := range candidates {
		m, err := parseDotEnv(path)
		if err == nil && m["EVE_CLIENT_ID"] != "" {
			return m
		}
	}
	return nil
}

func parseDotEnv(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(key)] = strings.TrimSpace(val)
	}
	return out, sc.Err()
}

func splitCSV(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parseBool(raw string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func randomToken() string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func AllWriteScopeSet() map[string]struct{} {
	out := map[string]struct{}{}
	for _, cap := range WriteCapabilities {
		for _, s := range cap.Scopes {
			out[s] = struct{}{}
		}
	}
	return out
}

func CorpScopeSet() map[string]struct{} {
	out := map[string]struct{}{}
	for _, s := range CorpReadScopes {
		out[s] = struct{}{}
	}
	return out
}
