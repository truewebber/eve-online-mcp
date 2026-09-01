package session

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/truewebber/gopkg/log"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	esihttp "github.com/truewebber/eve-online-mcp/internal/adapter/esi/http"
	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	"github.com/truewebber/eve-online-mcp/internal/domain/authcode"
	"github.com/truewebber/eve-online-mcp/internal/domain/character"
	"github.com/truewebber/eve-online-mcp/internal/domain/confirm"
	"github.com/truewebber/eve-online-mcp/internal/domain/loginstate"
	"github.com/truewebber/eve-online-mcp/internal/domain/mutation"
	"github.com/truewebber/eve-online-mcp/internal/domain/oauthclient"
	dbsession "github.com/truewebber/eve-online-mcp/internal/domain/session"
	"github.com/truewebber/eve-online-mcp/internal/domain/write"
	"github.com/truewebber/eve-online-mcp/internal/j"
)

const idleConnFactor = 2

var (
	ErrMutationsRequired = errors.New("session: mutation repository is required")
	ErrESIRequired       = errors.New("session: ESI client is required")
	ErrSSORequired       = errors.New("session: SSO client is required")
	ErrNoSession         = errors.New("session: not tied to a login")
	ErrDeadSession       = errors.New("session: character no longer authorized")
	ErrMissingScope      = errors.New("session: missing scope")
	ErrNPCCorp           = errors.New("session: npc corporation")
	ErrMissingCorpRole   = errors.New("session: missing corp role")
	ErrNoCorporation     = errors.New("session: no corporation_id")
)

type TxRunner func(ctx context.Context, fn func(context.Context) error) error

type Options struct {
	UserAgent         string
	RequestTimeoutSec float64
	MaxConcurrency    int
	HTTP              *http.Client
	ESI               esi.Client
	SSO               sso.Client
	Characters        character.Repository
	Sessions          dbsession.Repository
	Clients           oauthclient.Repository
	Logins            loginstate.Repository
	Codes             authcode.Repository
	Confirms          confirm.Repository
	Mutations         mutation.Repository
	WithinTx          TxRunner
	Logger            log.Logger
}

type Session struct {
	Opts        Options
	HTTP        *http.Client
	Characters  character.Repository
	Sessions    dbsession.Repository
	Clients     oauthclient.Repository
	Logins      loginstate.Repository
	Codes       authcode.Repository
	Confirms    confirm.Repository
	Mutations   mutation.Repository
	WithinTx    TxRunner
	CharacterID int
	SessionID   int64
	SSO         sso.Client
	ESI         esi.Client
	Resolver    *esihttp.Resolver
	Guard       *write.Guard
	Logger      log.Logger
	grantMu     sync.Mutex
	grant       *grantState
}

func Open(opts Options) (*Session, error) {
	if opts.Mutations == nil {
		return nil, ErrMutationsRequired
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
	s := &Session{
		Opts:       opts,
		HTTP:       opts.HTTP,
		Characters: opts.Characters,
		Sessions:   opts.Sessions,
		Clients:    opts.Clients,
		Logins:     opts.Logins,
		Codes:      opts.Codes,
		Confirms:   opts.Confirms,
		Mutations:  opts.Mutations,
		WithinTx:   opts.WithinTx,
		SSO:        opts.SSO,
		Resolver:   esihttp.NewResolver(opts.ESI, opts.Logger),
		Guard:      write.NewGuard(guardPersist{mutations: opts.Mutations, confirms: opts.Confirms}, 0, 0, opts.Logger),
		Logger:     opts.Logger,
		grant:      &grantState{},
	}
	s.ESI = opts.ESI.ForUser(ssoTokens{s: s})

	return s, nil
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

type ctxKey struct{}

func With(ctx context.Context, s *Session) context.Context {
	return context.WithValue(ctx, ctxKey{}, s)
}

func From(ctx context.Context) (*Session, error) {
	s, ok := ctx.Value(ctxKey{}).(*Session)
	if !ok || s == nil {
		return nil, wrap("From", ErrNoSession)
	}

	return s, nil
}

func (s *Session) ForCharacter(characterID int, sessionID int64) *Session {
	opts := s.Opts
	out := &Session{
		Opts:        opts,
		HTTP:        s.HTTP,
		Characters:  s.Characters,
		Sessions:    s.Sessions,
		Clients:     s.Clients,
		Logins:      s.Logins,
		Codes:       s.Codes,
		Confirms:    s.Confirms,
		Mutations:   s.Mutations,
		WithinTx:    s.WithinTx,
		CharacterID: characterID,
		SessionID:   sessionID,
		SSO:         s.SSO,
		Resolver:    s.Resolver,
		Guard: write.NewGuard(
			guardPersist{mutations: s.Mutations, confirms: s.Confirms},
			int64(characterID), sessionID, s.Logger,
		),
		Logger: s.Logger,
		grant:  &grantState{},
	}
	out.ESI = opts.ESI.ForUser(ssoTokens{s: out})
	out.Resolver = s.Resolver.ForUser(out.ESI)

	return out
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

func (s *Session) Character(ctx context.Context) (*sso.CharacterToken, error) {
	if s.CharacterID == 0 || s.SessionID == 0 {
		return nil, wrap("Character", ErrNoSession)
	}
	row, err := s.Sessions.LiveByID(ctx, s.SessionID)
	if err != nil || row.CharacterID != int64(s.CharacterID) {
		return nil, wrap("Character", ErrDeadSession)
	}
	ident, err := s.Characters.Get(ctx, int64(s.CharacterID))
	if err != nil || !ident.Live() {
		return nil, wrap("Character", ErrDeadSession)
	}
	if err := s.revokeIfScopesFallShort(ctx, row.Scopes); err != nil {
		return nil, wrap("Character", err)
	}

	return &sso.CharacterToken{
		CharacterID:   int(ident.ID),
		CharacterName: ident.Name,
		RefreshToken:  row.RefreshToken,
		Scopes:        row.Scopes,
		OwnerHash:     ident.OwnerHash,
		AddedAt:       float64(ident.CreatedAt.Unix()),
	}, nil
}

func (s *Session) Live(ctx context.Context) (*dbsession.Session, error) {
	if s.SessionID == 0 {
		return nil, wrap("Live", ErrNoSession)
	}
	row, err := s.Sessions.LiveByID(ctx, s.SessionID)
	if err != nil {
		return nil, wrap("Live", err)
	}

	return row, nil
}

func (s *Session) RequireScope(token *sso.CharacterToken, scope, what string) error {
	return s.RequireGranted(token.CharacterName, token.Scopes, scope, what)
}

func (s *Session) RequireGranted(characterName string, scopes []string, scope, what string) error {
	if s.HasGranted(scopes, scope) {
		return nil
	}

	return wrap("RequireScope", fmt.Errorf("%w: %s %s %s", ErrMissingScope, characterName, scope, what))
}

func (s *Session) HasScope(token *sso.CharacterToken, scope string) bool {
	return s.HasGranted(token.Scopes, scope)
}

func (s *Session) HasGranted(scopes []string, scope string) bool {
	return slices.Contains(scopes, scope)
}

func (s *Session) ResolveCorporation(ctx context.Context) (*character.Corporation, error) {
	token, err := s.Character(ctx)
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
		return nil, wrap("ResolveCorporation", fmt.Errorf("%w: %s", ErrNoCorporation, token.CharacterName))
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

	return wrap("RefuseNPCCorp", fmt.Errorf("%w: %s [%s] #%d", ErrNPCCorp, corp.CorporationName, corp.Ticker, corp.CorporationID))
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

	return wrap("RequireCorpRole", fmt.Errorf("%w: %s in %s for %s have %s", ErrMissingCorpRole, strings.Join(needed, " or "), corp.CorporationName, what, strings.Join(have, ", ")))
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
