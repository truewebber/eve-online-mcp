package session

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/truewebber/gopkg/log"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	"github.com/truewebber/eve-online-mcp/internal/adapter/store"
	"github.com/truewebber/eve-online-mcp/internal/domain/authcode"
	"github.com/truewebber/eve-online-mcp/internal/domain/character"
	"github.com/truewebber/eve-online-mcp/internal/domain/confirm"
	"github.com/truewebber/eve-online-mcp/internal/domain/j"
	"github.com/truewebber/eve-online-mcp/internal/domain/loginstate"
	"github.com/truewebber/eve-online-mcp/internal/domain/oauthclient"
	"github.com/truewebber/eve-online-mcp/internal/domain/write"
)

const idleConnFactor = 2

var (
	ErrStoreRequired = errors.New("session: postgres store is required")
	ErrESIRequired   = errors.New("session: ESI client is required")
	ErrSSORequired   = errors.New("session: SSO client is required")
)

type Options struct {
	UserAgent         string
	RequestTimeoutSec float64
	MaxConcurrency    int
	HTTP              *http.Client
	ESI               esi.Client
	SSO               sso.Client
	Store             *store.Store
	Characters        character.Repository
	Clients           oauthclient.Repository
	Logins            loginstate.Repository
	Codes             authcode.Repository
	Confirms          confirm.Repository
	Logger            log.Logger
}

type Session struct {
	Opts       Options
	HTTP       *http.Client
	Store      *store.Store
	Characters character.Repository
	Clients    oauthclient.Repository
	Logins     loginstate.Repository
	Codes      authcode.Repository
	Confirms   confirm.Repository
	UserID     string
	SSO        sso.Client
	ESI        esi.Client
	Resolver   *esi.Resolver
	Guard      *write.Guard
	Logger     log.Logger
}

func Open(opts Options) (*Session, error) {
	if opts.Store == nil {
		return nil, ErrStoreRequired
	}
	if opts.ESI == nil {
		return nil, ErrESIRequired
	}
	if opts.SSO == nil {
		return nil, ErrSSORequired
	}
	opts = normalize(opts)
	if opts.HTTP == nil {
		opts.HTTP = NewHTTPClient(opts)
	}
	purgeExpired(context.Background(), opts)
	ssoClient := opts.SSO.ForUser("", opts.Characters)
	esiClient := opts.ESI.ForUser(ssoTokens{sso: ssoClient})
	persist := guardPersist{db: opts.Store, confirms: opts.Confirms}

	return &Session{
		Opts:       opts,
		HTTP:       opts.HTTP,
		Store:      opts.Store,
		Characters: opts.Characters,
		Clients:    opts.Clients,
		Logins:     opts.Logins,
		Codes:      opts.Codes,
		Confirms:   opts.Confirms,
		SSO:        ssoClient,
		ESI:        esiClient,
		Resolver:   esi.NewResolver(esiClient, opts.Logger),
		Guard:      write.NewGuard(persist, "", opts.Logger),
		Logger:     opts.Logger,
	}, nil
}

func NewHTTPClient(opts Options) *http.Client {
	opts = normalize(opts)

	return &http.Client{
		Timeout: time.Duration(opts.RequestTimeoutSec * float64(time.Second)),
		Transport: &http.Transport{
			MaxIdleConns:        opts.MaxConcurrency * idleConnFactor,
			MaxIdleConnsPerHost: opts.MaxConcurrency * idleConnFactor,
		},
	}
}

func normalize(opts Options) Options {
	if opts.RequestTimeoutSec <= 0 {
		opts.RequestTimeoutSec = 30
	}
	if opts.MaxConcurrency < 1 {
		opts.MaxConcurrency = 8
	}

	return opts
}

func purgeExpired(ctx context.Context, opts Options) {
	var n int64
	if opts.Logins != nil {
		k, err := opts.Logins.DeleteExpired(ctx)
		if err != nil {
			opts.Logger.Error("session: purge login states", "err", err)
		} else {
			n += k
		}
	}
	if opts.Codes != nil {
		k, err := opts.Codes.DeleteExpired(ctx)
		if err != nil {
			opts.Logger.Error("session: purge auth codes", "err", err)
		} else {
			n += k
		}
	}
	if opts.Confirms != nil {
		k, err := opts.Confirms.DeleteExpired(ctx)
		if err != nil {
			opts.Logger.Error("session: purge confirm tokens", "err", err)
		} else {
			n += k
		}
	}
	if n > 0 {
		opts.Logger.Info("session: purged expired rows", "n", n)
	}
}

func (s *Session) Close() {
	if s.Store != nil {
		s.Store.Close()
	}
}

type ctxKey struct{}

func With(ctx context.Context, s *Session) context.Context {
	return context.WithValue(ctx, ctxKey{}, s)
}

func From(ctx context.Context) (*Session, error) {
	s, ok := ctx.Value(ctxKey{}).(*Session)
	if !ok || s == nil {
		return nil, character.NotFoundError{Msg: "This request is not tied to an EVE login. Re-authenticate the MCP server (Authentication required) and try again."}
	}

	return s, nil
}

// ForUser keeps the process EVE application and the shared HTTP cache;
// only character tokens are bound to this user.
func (s *Session) ForUser(userID string) *Session {
	opts := s.Opts
	ssoClient := s.Opts.SSO.ForUser(userID, s.Characters)
	esiClient := s.Opts.ESI.ForUser(ssoTokens{sso: ssoClient})

	return &Session{
		Opts:       opts,
		HTTP:       s.HTTP,
		Store:      s.Store,
		Characters: s.Characters,
		Clients:    s.Clients,
		Logins:     s.Logins,
		Codes:      s.Codes,
		Confirms:   s.Confirms,
		UserID:     userID,
		SSO:        ssoClient,
		ESI:        esiClient,
		Resolver:   s.Resolver.ForUser(esiClient),
		Guard:      write.NewGuard(guardPersist{db: s.Store, confirms: s.Confirms}, userID, s.Logger),
		Logger:     s.Logger,
	}
}

type AltLogin struct {
	URL   string
	State string
}

func (s *Session) StartAltLogin(ctx context.Context) (AltLogin, error) {
	if s.UserID == "" {
		return AltLogin{}, wrap("StartAltLogin", sso.Err("This request is not tied to an EVE login. Re-authenticate the MCP server (Authentication required) and try again."))
	}
	prep, err := s.SSO.PrepareLogin(nil)
	if err != nil {
		return AltLogin{}, wrap("StartAltLogin", err)
	}
	if err := s.Logins.Put(ctx, loginstate.Login{
		State:        prep.State,
		PKCEVerifier: prep.Verifier,
		Scopes:       prep.Scopes,
		Kind:         loginstate.KindAlt,
		UserID:       s.UserID,
	}); err != nil {
		return AltLogin{}, wrap("StartAltLogin", err)
	}

	return AltLogin{URL: prep.URL, State: prep.State}, nil
}

func (s *Session) DomainToken(tok *sso.CharacterToken) *character.Token {
	if tok == nil {
		return nil
	}

	return &character.Token{
		CharacterID: tok.CharacterID, CharacterName: tok.CharacterName,
		Scopes: tok.Scopes, OwnerHash: tok.OwnerHash,
	}
}

func (s *Session) ResolveCharacter(ctx context.Context, spec string) (*sso.CharacterToken, error) {
	tokens := s.SSO.All(ctx)
	if len(tokens) == 0 {
		return nil, character.NotFoundError{Msg: "No characters are authorized yet. Call eve_auth_login_url and open the link in a browser to authorize one."}
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

		return nil, character.NotFoundError{Msg: "Several characters are authorized, so 'character' is required. Available: " + strings.Join(names, ", ")}
	}
	if id, err := strconv.Atoi(spec); err == nil {
		token := s.SSO.Get(ctx, id)
		if token == nil {
			return nil, character.NotFoundError{Msg: fmt.Sprintf("Character id %s is not authorized.", spec)}
		}

		return token, nil
	}
	token := s.SSO.FindByName(ctx, spec)
	if token == nil {
		var have []string
		for _, t := range tokens {
			have = append(have, t.CharacterName)
		}

		return nil, character.NotFoundError{Msg: fmt.Sprintf("No authorized character matches %q. Have: %s", spec, strings.Join(have, ", "))}
	}

	return token, nil
}

func (s *Session) RequireScope(token *sso.CharacterToken, scope, what string) error {
	return s.RequireGranted(token.CharacterName, token.Scopes, scope, what)
}

func (s *Session) RequireGranted(characterName string, scopes []string, scope, what string) error {
	if s.HasGranted(scopes, scope) {
		return nil
	}
	extra := ""
	if slices.Contains(write.CorpReadScopes(), scope) {
		extra = " That is a corporation scope: add the matching permissions on the EVE developer application and re-authorize this character with eve_auth_login_url."
	}

	return wrap("RequireScope", sso.Err(fmt.Sprintf("%s was not authorized with '%s', which is required to read %s. Re-run the login for this character.%s", characterName, scope, what, extra)))
}

func (s *Session) HasScope(token *sso.CharacterToken, scope string) bool {
	return s.HasGranted(token.Scopes, scope)
}

func (s *Session) HasGranted(scopes []string, scope string) bool {
	return slices.Contains(scopes, scope)
}

func (s *Session) ResolveCorporation(ctx context.Context, spec string) (*character.Corporation, error) {
	token, err := s.ResolveCharacter(ctx, spec)
	if err != nil {
		return nil, err
	}
	sheet, err := s.ESI.Get(ctx, esi.Path("characters", esi.ID(token.CharacterID)), nil, nil, nil)
	if err != nil {
		return nil, wrap("ResolveCorporation", err)
	}
	info := j.Map(sheet.Data)
	corpID := j.Int(info["corporation_id"])
	if corpID == 0 {
		return nil, wrap("ResolveCorporation", sso.Err(token.CharacterName+" has no corporation_id from ESI. Try again shortly."))
	}
	publicRes, err := s.ESI.Get(ctx, esi.Path("corporations", esi.ID(corpID)), nil, nil, nil)
	if err != nil {
		return nil, wrap("ResolveCorporation", err)
	}
	public := j.Map(publicRes.Data)
	roles := map[string]struct{}{}
	hq, base, other := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	if s.HasScope(token, "esi-characters.read_corporation_roles.v1") {
		granted, err := s.ESI.Get(ctx, esi.Path("characters", esi.ID(token.CharacterID), "roles"), &token.CharacterID, nil, nil)
		if err != nil {
			s.Logger.Error("session: corporation roles", "character", token.CharacterName, "err", err)
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

	return &character.Corporation{
		Token: s.DomainToken(token), CorporationID: corpID, CorporationName: name,
		Ticker: j.Str(public["ticker"]), Public: public,
		Roles: roles, RolesAtHQ: hq, RolesAtBase: base, RolesAtOther: other,
	}, nil
}

func (s *Session) RequirePlayerCorp(corp *character.Corporation) error {
	if !corp.IsNPC() {
		return nil
	}

	return wrap("RefuseNPCCorp", sso.Err(fmt.Sprintf("%s is in the NPC corporation %s [%s] (#%d). ESI corporation hangars, wallets and jobs only exist for player-created corporations. There is nothing for eve_corp_* to read.", corp.CharacterName(), corp.CorporationName, corp.Ticker, corp.CorporationID)))
}

func (s *Session) RequireCorpRole(corp *character.Corporation, needed []string, what string) error {
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

	return wrap("RequireCorpRole", sso.Err(fmt.Sprintf("%s has no %s role (nor Director) in %s, which ESI requires to read %s. Roles granted everywhere: %s. Location-specific roles (HQ/base/other) do not unlock these endpoints. eve_corp_overview lists this character's roles.", corp.CharacterName(), strings.Join(needed, " or "), corp.CorporationName, what, strings.Join(have, ", "))))
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
