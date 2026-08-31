// Package session is the per-user runtime: ESI, SSO, names and the write guard.
package session

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/adapter/names"
	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	"github.com/truewebber/eve-online-mcp/internal/adapter/store"
	"github.com/truewebber/eve-online-mcp/internal/domain/character"
	"github.com/truewebber/eve-online-mcp/internal/domain/j"
	"github.com/truewebber/eve-online-mcp/internal/domain/write"
)

// ErrStoreRequired is returned when Open is called without a Postgres store.
var ErrStoreRequired = errors.New("session: postgres store is required")

// Options is assembled at the composition root from process config.
type Options struct {
	UserAgent         string
	RequestTimeoutSec float64
	MaxConcurrency    int
	ESI               esi.Options
	SSO               sso.Options
	Store             *store.Store
}

type Session struct {
	Opts     Options
	HTTP     *http.Client
	Store    *store.Store
	SSO      *sso.Client
	ESI      *esi.Client
	Resolver *names.Resolver
	Guard    *write.Guard
}

func Open(opts Options) (*Session, error) {
	if opts.Store == nil {
		return nil, ErrStoreRequired
	}
	if opts.RequestTimeoutSec <= 0 {
		opts.RequestTimeoutSec = 30
	}
	if opts.MaxConcurrency < 1 {
		opts.MaxConcurrency = 8
	}
	httpClient := &http.Client{
		Timeout: time.Duration(opts.RequestTimeoutSec * float64(time.Second)),
		Transport: &http.Transport{
			MaxIdleConns:        opts.MaxConcurrency * 2,
			MaxIdleConnsPerHost: opts.MaxConcurrency * 2,
		},
	}
	if n, err := opts.Store.PurgeExpired(context.Background()); err == nil && n > 0 {
		log.Printf("purged %d expired store rows", n)
	}
	opts.SSO.DB = opts.Store
	ssoClient := sso.New(opts.SSO, httpClient)
	esiClient := esi.New(opts.ESI, httpClient, opts.Store, ssoClient)
	persist := guardPersist{db: opts.Store}

	return &Session{
		Opts:     opts,
		HTTP:     httpClient,
		Store:    opts.Store,
		SSO:      ssoClient,
		ESI:      esiClient,
		Resolver: names.New(esiClient, opts.Store),
		Guard:    write.NewGuard(persist, ""),
	}, nil
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
	s, _ := ctx.Value(ctxKey{}).(*Session)
	if s == nil {
		return nil, character.NotFoundError{Msg: "This request is not tied to an EVE login. Re-authenticate the MCP server (Authentication required) and try again."}
	}

	return s, nil
}

// ForUser returns a Session bound to one user's character tokens. The EVE
// application credentials stay the process ones; HTTP cache is shared.
func (s *Session) ForUser(userID string) *Session {
	opts := s.Opts
	opts.SSO.DB = s.Store
	opts.SSO.UserID = userID
	ssoClient := sso.New(opts.SSO, s.HTTP)
	esiClient := esi.New(opts.ESI, s.HTTP, s.Store, ssoClient)

	return &Session{
		Opts:     opts,
		HTTP:     s.HTTP,
		Store:    s.Store,
		SSO:      ssoClient,
		ESI:      esiClient,
		Resolver: names.New(esiClient, s.Store),
		Guard:    write.NewGuard(guardPersist{db: s.Store}, userID),
	}
}

// StartAltLogin begins an EVE SSO handshake for an extra character on
// this user. The PKCE verifier is stored in login_states with kind=alt
// so any replica can finish the callback.
type AltLogin struct {
	URL   string
	State string
}

func (s *Session) StartAltLogin(ctx context.Context) (AltLogin, error) {
	if s.Opts.SSO.UserID == "" {
		return AltLogin{}, sso.Err("This request is not tied to an EVE login. Re-authenticate the MCP server (Authentication required) and try again.")
	}
	prep, err := s.SSO.PrepareLogin(nil)
	if err != nil {
		return AltLogin{}, err
	}
	if err := s.Store.PutLoginState(ctx, store.LoginState{
		State:        prep.State,
		PKCEVerifier: prep.Verifier,
		Scopes:       prep.Scopes,
		Kind:         store.LoginAlt,
		UserID:       s.Opts.SSO.UserID,
	}); err != nil {
		return AltLogin{}, err
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

func (s *Session) ResolveCharacter(spec string) (*sso.CharacterToken, error) {
	tokens := s.SSO.Store.All()
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
		token := s.SSO.Store.Get(id)
		if token == nil {
			return nil, character.NotFoundError{Msg: fmt.Sprintf("Character id %s is not authorized.", spec)}
		}

		return token, nil
	}
	token := s.SSO.Store.FindByName(spec)
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

	return sso.Err(fmt.Sprintf("%s was not authorized with '%s', which is required to read %s. Re-run the login for this character.%s", characterName, scope, what, extra))
}

func (s *Session) HasScope(token *sso.CharacterToken, scope string) bool {
	return s.HasGranted(token.Scopes, scope)
}

func (s *Session) HasGranted(scopes []string, scope string) bool {
	return slices.Contains(scopes, scope)
}

func (s *Session) ResolveCorporation(spec string) (*character.Corporation, error) {
	token, err := s.ResolveCharacter(spec)
	if err != nil {
		return nil, err
	}
	sheet, err := s.ESI.Get(fmt.Sprintf("/characters/%d", token.CharacterID), nil, nil, nil)
	if err != nil {
		return nil, err
	}
	info := j.Map(sheet.Data)
	corpID := j.Int(info["corporation_id"])
	if corpID == 0 {
		return nil, sso.Err(token.CharacterName + " has no corporation_id from ESI. Try again shortly.")
	}
	publicRes, err := s.ESI.Get(fmt.Sprintf("/corporations/%d", corpID), nil, nil, nil)
	if err != nil {
		return nil, err
	}
	public := j.Map(publicRes.Data)
	roles := map[string]struct{}{}
	hq, base, other := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	if s.HasScope(token, "esi-characters.read_corporation_roles.v1") {
		granted, err := s.ESI.Get(fmt.Sprintf("/characters/%d/roles", token.CharacterID), &token.CharacterID, nil, nil)
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

	return sso.Err(fmt.Sprintf("%s is in the NPC corporation %s [%s] (#%d). ESI corporation hangars, wallets and jobs only exist for player-created corporations. There is nothing for eve_corp_* to read.", corp.CharacterName(), corp.CorporationName, corp.Ticker, corp.CorporationID))
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

	return sso.Err(fmt.Sprintf("%s has no %s role (nor Director) in %s, which ESI requires to read %s. Roles granted everywhere: %s. Location-specific roles (HQ/base/other) do not unlock these endpoints. eve_corp_overview lists this character's roles.", corp.CharacterName(), strings.Join(needed, " or "), corp.CorporationName, what, strings.Join(have, ", ")))
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
