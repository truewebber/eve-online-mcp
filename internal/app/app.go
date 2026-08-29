package app

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"eve-mcp/internal/auth"
	"eve-mcp/internal/cache"
	"eve-mcp/internal/config"
	"eve-mcp/internal/esi"
	"eve-mcp/internal/j"
	"eve-mcp/internal/names"
	"eve-mcp/internal/safety"
)

const PlayerCorpIDFloor = 98_000_000

type NotFound struct{ Msg string }

func (e NotFound) Error() string { return e.Msg }

type Corporation struct {
	Token           *auth.CharacterToken
	CorporationID   int
	CorporationName string
	Ticker          string
	Public          map[string]any
	Roles           map[string]struct{}
	RolesAtHQ       map[string]struct{}
	RolesAtBase     map[string]struct{}
	RolesAtOther    map[string]struct{}
}

func (c Corporation) CharacterID() int      { return c.Token.CharacterID }
func (c Corporation) CharacterName() string { return c.Token.CharacterName }
func (c Corporation) IsNPC() bool           { return c.CorporationID < PlayerCorpIDFloor }

func (c Corporation) HasRole(needed ...string) bool {
	if _, ok := c.Roles["Director"]; ok {
		return true
	}
	for _, role := range needed {
		if _, ok := c.Roles[role]; ok {
			return true
		}
	}
	return false
}

type App struct {
	Settings *config.Settings
	HTTP     *http.Client
	Store    *cache.Store
	SSO      *auth.Client
	ESI      *esi.Client
	Resolver *names.Resolver
	Guard    *safety.Guard
}

func Open(settings *config.Settings) (*App, error) {
	httpClient := &http.Client{
		Timeout: time.Duration(settings.RequestTimeoutSec * float64(time.Second)),
		Transport: &http.Transport{
			MaxIdleConns:        settings.MaxConcurrency * 2,
			MaxIdleConnsPerHost: settings.MaxConcurrency * 2,
		},
	}
	store, err := cache.Open(settings.CacheFile())
	if err != nil {
		return nil, err
	}
	if n, err := store.PurgeExpired(30); err == nil && n > 0 {
		log.Printf("purged %d stale cache rows", n)
	}
	sso := auth.New(settings, httpClient)
	esiClient := esi.New(settings, httpClient, store, sso)
	return &App{
		Settings: settings,
		HTTP:     httpClient,
		Store:    store,
		SSO:      sso,
		ESI:      esiClient,
		Resolver: names.New(esiClient, store),
		Guard:    safety.New(settings),
	}, nil
}

func (a *App) Close() {
	if a.Store != nil {
		_ = a.Store.Close()
	}
}

type ctxKey struct{}

func With(ctx context.Context, a *App) context.Context {
	return context.WithValue(ctx, ctxKey{}, a)
}

func From(ctx context.Context) (*App, error) {
	a, _ := ctx.Value(ctxKey{}).(*App)
	if a == nil {
		return nil, NotFound{Msg: "This request is not tied to an EVE login. Re-authenticate the MCP server (Authentication required) and try again."}
	}
	return a, nil
}

// ForTenant returns an App that talks ESI as this tenant's CCP application.
// HTTP cache is shared; tokens, audit and User-Agent are per tenant.
func (a *App) ForTenant(id, eveClientID, eveSecret, contact, dataDir string) *App {
	s := *a.Settings
	s.ClientID = eveClientID
	s.ClientSecret = eveSecret
	s.DataDir = dataDir
	s.ConfigPath = filepath.Join(dataDir, "tenant.toml")
	if contact != "" {
		s.UserAgent = "eve-mcp/" + config.Version + " " + contact
	}
	sso := auth.New(&s, a.HTTP)
	esiClient := esi.New(&s, a.HTTP, a.Store, sso)
	return &App{
		Settings: &s,
		HTTP:     a.HTTP,
		Store:    a.Store,
		SSO:      sso,
		ESI:      esiClient,
		Resolver: names.New(esiClient, a.Store),
		Guard:    safety.New(&s),
	}
}

func (a *App) ResolveCharacter(spec string) (*auth.CharacterToken, error) {
	tokens := a.SSO.Store.All()
	if len(tokens) == 0 {
		return nil, NotFound{Msg: "No characters are authorized yet. Call eve_auth_login_url and open the link in a browser to authorize one."}
	}
	spec = strings.TrimSpace(spec)
	if spec == "" {
		if len(tokens) == 1 {
			return tokens[0], nil
		}
		var names []string
		for _, t := range tokens {
			names = append(names, fmt.Sprintf("%s (%d)", t.CharacterName, t.CharacterID))
		}
		return nil, NotFound{Msg: "Several characters are authorized, so 'character' is required. Available: " + strings.Join(names, ", ")}
	}
	if id, err := strconv.Atoi(spec); err == nil {
		token := a.SSO.Store.Get(id)
		if token == nil {
			return nil, NotFound{Msg: fmt.Sprintf("Character id %s is not authorized.", spec)}
		}
		return token, nil
	}
	token := a.SSO.Store.FindByName(spec)
	if token == nil {
		var names []string
		for _, t := range tokens {
			names = append(names, t.CharacterName)
		}
		return nil, NotFound{Msg: fmt.Sprintf("No authorized character matches %q. Have: %s", spec, strings.Join(names, ", "))}
	}
	return token, nil
}

func (a *App) RequireScope(token *auth.CharacterToken, scope, what string) error {
	for _, s := range token.Scopes {
		if s == scope {
			return nil
		}
	}
	extra := ""
	for _, s := range config.CorpReadScopes {
		if s == scope {
			extra = " That is a corporation scope: set corp_scopes=true in the config, add the matching permissions on the EVE developer application, restart, and re-authorize this character with eve_auth_login_url."
			break
		}
	}
	return auth.Err(fmt.Sprintf("%s was not authorized with '%s', which is required to read %s. Re-run the login for this character.%s", token.CharacterName, scope, what, extra))
}

func (a *App) HasScope(token *auth.CharacterToken, scope string) bool {
	for _, s := range token.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

func (a *App) ResolveCorporation(spec string) (*Corporation, error) {
	token, err := a.ResolveCharacter(spec)
	if err != nil {
		return nil, err
	}
	sheet, err := a.ESI.Get(fmt.Sprintf("/characters/%d", token.CharacterID), nil, nil, nil)
	if err != nil {
		return nil, err
	}
	info := j.Map(sheet.Data)
	corpID := j.Int(info["corporation_id"])
	if corpID == 0 {
		return nil, auth.Err(fmt.Sprintf("%s has no corporation_id from ESI. Try again shortly.", token.CharacterName))
	}
	publicRes, err := a.ESI.Get(fmt.Sprintf("/corporations/%d", corpID), nil, nil, nil)
	if err != nil {
		return nil, err
	}
	public := j.Map(publicRes.Data)
	roles := map[string]struct{}{}
	hq, base, other := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	if a.HasScope(token, "esi-characters.read_corporation_roles.v1") {
		granted, err := a.ESI.Get(fmt.Sprintf("/characters/%d/roles", token.CharacterID), &token.CharacterID, nil, nil)
		if err != nil {
			log.Printf("could not read corporation roles for %s: %v", token.CharacterName, err)
		} else {
			payload := j.Map(granted.Data)
			roles = stringSet(payload["roles"])
			hq = stringSet(payload["roles_at_hq"])
			base = stringSet(payload["roles_at_base"])
			other = stringSet(payload["roles_at_other"])
		}
	}
	name := j.Str(public["name"])
	if name == "" {
		name = fmt.Sprintf("Corporation #%d", corpID)
	}
	return &Corporation{
		Token: token, CorporationID: corpID, CorporationName: name,
		Ticker: j.Str(public["ticker"]), Public: public,
		Roles: roles, RolesAtHQ: hq, RolesAtBase: base, RolesAtOther: other,
	}, nil
}

func (a *App) RequirePlayerCorp(corp *Corporation) error {
	if !corp.IsNPC() {
		return nil
	}
	return auth.Err(fmt.Sprintf("%s is in the NPC corporation %s [%s] (#%d). ESI corporation hangars, wallets and jobs only exist for player-created corporations. There is nothing for eve_corp_* to read.", corp.CharacterName(), corp.CorporationName, corp.Ticker, corp.CorporationID))
}

func (a *App) RequireCorpRole(corp *Corporation, needed []string, what string) error {
	if len(needed) == 0 || corp.HasRole(needed...) {
		return nil
	}
	var have []string
	for r := range corp.Roles {
		have = append(have, r)
	}
	if len(have) == 0 {
		have = []string{"none"}
	}
	return auth.Err(fmt.Sprintf("%s has no %s role (nor Director) in %s, which ESI requires to read %s. Roles granted everywhere: %s. Location-specific roles (HQ/base/other) do not unlock these endpoints. eve_corp_overview lists this character's roles.", corp.CharacterName(), strings.Join(needed, " or "), corp.CorporationName, what, strings.Join(have, ", ")))
}

func stringSet(v any) map[string]struct{} {
	out := map[string]struct{}{}
	for _, item := range j.Slice(v) {
		if s, ok := item.(string); ok {
			out[s] = struct{}{}
		}
	}
	return out
}
