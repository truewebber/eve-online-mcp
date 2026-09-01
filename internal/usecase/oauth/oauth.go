package oauth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/truewebber/gopkg/log"

	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	"github.com/truewebber/eve-online-mcp/internal/domain/authcode"
	"github.com/truewebber/eve-online-mcp/internal/domain/character"
	"github.com/truewebber/eve-online-mcp/internal/domain/loginstate"
	"github.com/truewebber/eve-online-mcp/internal/domain/oauthclient"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"

	"github.com/golang-jwt/jwt/v5"
	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

const (
	accessTTL     = time.Hour
	refreshTTL    = 30 * 24 * time.Hour
	codeTTL       = 2 * time.Minute
	scopeEve      = "eve"
	hmacMinBytes  = 32
	clientIDBytes = 16
	authCodeBytes = 24
	jwtLeeway     = 30 * time.Second

	grantAuthCode     = "authorization_code"
	grantRefresh      = "refresh_token"
	paramClientID     = "client_id"
	paramCode         = "code"
	paramCodeVerifier = "code_verifier"
	paramRedirectURI  = "redirect_uri"
	paramGrantType    = "grant_type"
	typRefresh        = "refresh"
	schemeHTTP        = "http"
	schemeHTTPS       = "https"
)

var (
	ErrUnknownLogin = errors.New("unknown or expired login state")
	ErrHMACTooShort = errors.New("oauth: HMAC key is too short")
	errBadAlg       = errors.New("alg")
	errInvalidToken = errors.New("invalid")
	errNotRefresh   = errors.New("not refresh")
	errNoSub        = errors.New("no sub")
	errBadCharacter = errors.New("oauth: character id")
)

type Host struct {
	Listen      string
	PublicURL   string
	MCPPath     string
	CallbackURL string
	base        url.URL
}

func (h Host) BaseURL() string {
	u := h.root()

	return u.String()
}

func (h Host) URL(elem ...string) string {
	u := h.root()

	return u.JoinPath(elem...).String()
}

func (h Host) withBase() Host {
	h.base = h.computeBase()

	return h
}

func (h Host) computeBase() url.URL {
	if h.PublicURL != "" {
		u, err := url.Parse(strings.TrimRight(h.PublicURL, "/"))
		if err == nil && u.Host != "" {
			return *u
		}
	}
	host, port, err := net.SplitHostPort(h.Listen)
	if err != nil {
		return url.URL{Scheme: schemeHTTP, Host: "127.0.0.1:8765"}
	}
	if host == "0.0.0.0" || host == "::" || host == "" {
		host = "127.0.0.1"
	}

	return url.URL{Scheme: schemeHTTP, Host: net.JoinHostPort(host, port)}
}

func (h Host) root() url.URL {
	if h.base.Scheme != "" {
		return h.base
	}

	return h.computeBase()
}

type Options struct {
	HMACKey []byte
}

type Server struct {
	pub      Host
	runtime  *session.Session
	clients  oauthclient.Repository
	logins   loginstate.Repository
	codes    authcode.Repository
	login    sso.Client
	hmacKey  []byte
	sessions sync.Map
	logger   log.Logger
}

func Open(pub Host, runtime *session.Session, opts Options, logger log.Logger) (*Server, error) {
	if pub.MCPPath == "" {
		pub.MCPPath = "/mcp"
	}
	pub = pub.withBase()
	if len(opts.HMACKey) < hmacMinBytes {
		return nil, ErrHMACTooShort
	}

	return &Server{
		pub:     pub,
		runtime: runtime,
		clients: runtime.Clients,
		logins:  runtime.Logins,
		codes:   runtime.Codes,
		login:   runtime.Opts.SSO.ForCharacter(0, nil),
		hmacKey: opts.HMACKey,
		logger:  logger,
	}, nil
}

func (s *Server) IssueAccess(characterID int) (string, error) {
	return s.issueAccess(characterID)
}

func (s *Server) Base() string { return s.pub.BaseURL() }

func (s *Server) ResourceURL() string {
	return s.pub.URL(strings.TrimPrefix(s.pub.MCPPath, "/"))
}

func (s *Server) MetadataURL() string {
	return s.pub.URL(".well-known", "oauth-protected-resource")
}

func (s *Server) ProtectedResource() *oauthex.ProtectedResourceMetadata {
	return &oauthex.ProtectedResourceMetadata{
		Resource:               s.ResourceURL(),
		AuthorizationServers:   []string{s.Base()},
		ScopesSupported:        []string{scopeEve},
		BearerMethodsSupported: []string{"header"},
		ResourceName:           "EVE Online MCP",
	}
}

func (s *Server) AuthServerMeta() *oauthex.AuthServerMeta {
	return &oauthex.AuthServerMeta{
		Issuer:                            s.Base(),
		AuthorizationEndpoint:             s.pub.URL("oauth", "authorize"),
		TokenEndpoint:                     s.pub.URL("oauth", "token"),
		RegistrationEndpoint:              s.pub.URL("oauth", "register"),
		ResponseTypesSupported:            []string{paramCode},
		GrantTypesSupported:               []string{grantAuthCode, grantRefresh},
		CodeChallengeMethodsSupported:     []string{"S256"},
		TokenEndpointAuthMethodsSupported: []string{"none", "client_secret_post", "client_secret_basic"},
		ScopesSupported:                   []string{scopeEve},
	}
}

func (s *Server) ProtectedResourceHandler() http.Handler {
	return mcpauth.ProtectedResourceMetadataHandler(s.ProtectedResource())
}

func (s *Server) ServeASMeta(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(s.AuthServerMeta()); err != nil {
		s.logger.Error("oauth: encode AS metadata", "err", err)
	}
}

func (s *Server) ServeRegister(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)

		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

		return
	}
	var req struct {
		RedirectURIs []string `json:"redirect_uris"`
		ClientName   string   `json:"client_name"`
	}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil || len(req.RedirectURIs) == 0 {
		http.Error(w, `{"error":"invalid_client_metadata"}`, http.StatusBadRequest)

		return
	}
	var allowed []string
	for _, u := range req.RedirectURIs {
		if redirectOK(u) {
			allowed = append(allowed, u)
		}
	}
	if len(allowed) == 0 {
		http.Error(w, `{"error":"invalid_redirect_uri"}`, http.StatusBadRequest)

		return
	}
	id := randomID(clientIDBytes)
	err = s.clients.Upsert(r.Context(), oauthclient.Client{ID: id, RedirectURIs: allowed})
	if err != nil {
		http.Error(w, `{"error":"server_error"}`, http.StatusInternalServerError)

		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(map[string]any{
		paramClientID:                id,
		"redirect_uris":              allowed,
		"grant_types":                []string{grantAuthCode, grantRefresh},
		"response_types":             []string{paramCode},
		"token_endpoint_auth_method": "none",
		"client_name":                req.ClientName,
	}); err != nil {
		s.logger.Error("oauth: encode client registration", "err", err)
	}
}

// No form: the instance owns the one EVE application; the player only
// picks a character at CCP.
func (s *Server) ServeAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	mcpClient := q.Get(paramClientID)
	redirect := q.Get(paramRedirectURI)
	state := q.Get("state")
	challenge := q.Get("code_challenge")
	if mcpClient == "" || redirect == "" || challenge == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><meta charset=utf-8><title>EVE MCP</title>
<body style="font:15px/1.6 system-ui;max-width:36rem;margin:4rem auto;padding:0 1.5rem">
<h1>Connect from Cursor or Claude</h1>
<p>Add <code>%s</code> as an HTTP MCP server. The client will show Authentication required and send you to the EVE login.</p>
<p class=dim>EVE callback: <code>%s</code></p>`,
			html.EscapeString(s.ResourceURL()), html.EscapeString(s.pub.CallbackURL))

		return
	}
	if !s.clientRedirectOK(r.Context(), mcpClient, redirect) {
		http.Error(w, "unknown client or redirect_uri", http.StatusBadRequest)

		return
	}

	s.purge(r.Context())
	prep, err := s.login.PrepareLogin(nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}
	if err := s.logins.Put(r.Context(), loginstate.Login{
		State:         prep.State,
		PKCEVerifier:  prep.Verifier,
		Scopes:        prep.Scopes,
		MCPClientID:   mcpClient,
		RedirectURI:   redirect,
		MCPState:      state,
		CodeChallenge: challenge,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}
	http.Redirect(w, r, prep.URL, http.StatusFound)
}

type Callback struct {
	Redirect string
	Token    *sso.CharacterToken
}

func (s *Server) CompleteCallback(ctx context.Context, code, eveState string) (Callback, error) {
	s.purge(ctx)
	st, err := s.logins.Take(ctx, eveState)
	if errors.Is(err, loginstate.ErrNotFound) {
		return Callback{}, ErrUnknownLogin
	}
	if err != nil {
		return Callback{}, wrap("CompleteCallback", err)
	}
	token, err := s.login.ExchangeCode(ctx, code, st.PKCEVerifier)
	if err != nil {
		return Callback{}, wrap("CompleteCallback", err)
	}
	loc, err := s.finishMCP(ctx, st, token)

	return Callback{Redirect: loc, Token: token}, err
}

func (s *Server) ServeToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

		return
	}
	s.purge(r.Context())
	err := r.ParseForm()
	if err != nil {
		http.Error(w, `{"error":"invalid_request"}`, http.StatusBadRequest)

		return
	}
	switch r.Form.Get(paramGrantType) {
	case grantAuthCode:
		s.exchangeCode(w, r)
	case grantRefresh:
		s.refresh(w, r)
	default:
		http.Error(w, `{"error":"unsupported_grant_type"}`, http.StatusBadRequest)
	}
}

func (s *Server) VerifyAccess(_ context.Context, token string, _ *http.Request) (*mcpauth.TokenInfo, error) {
	return s.verifyAccess(token)
}

func (s *Server) SessionFor(characterID int) *session.Session {
	key := strconv.Itoa(characterID)
	if v, ok := s.sessions.Load(key); ok {
		if sess, ok := v.(*session.Session); ok {
			return sess
		}
	}
	sess := s.runtime.ForCharacter(characterID)
	s.sessions.Store(key, sess)

	return sess
}

func (s *Server) ProtectMCP(next http.Handler) http.Handler {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ti := mcpauth.TokenInfoFromContext(r.Context())
		if ti == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)

			return
		}
		id, err := strconv.ParseInt(ti.UserID, 10, 64)
		if err != nil || id == 0 {
			http.Error(w, "unknown character", http.StatusUnauthorized)

			return
		}
		row, err := s.runtime.Characters.Get(r.Context(), id)
		if err != nil || row == nil || !row.Live() {
			http.Error(w, "unknown character", http.StatusUnauthorized)

			return
		}
		next.ServeHTTP(w, r.WithContext(session.With(r.Context(), s.SessionFor(int(id)))))
	})

	return mcpauth.RequireBearerToken(s.VerifyAccess, &mcpauth.RequireBearerTokenOptions{
		ResourceMetadataURL: s.MetadataURL(),
		Scopes:              []string{scopeEve},
		ClockSkew:           jwtLeeway,
	})(inner)
}

func (s *Server) finishMCP(ctx context.Context, p *loginstate.Login, token *sso.CharacterToken) (string, error) {
	existing, err := s.runtime.Characters.Get(ctx, int64(token.CharacterID))
	if err != nil && !errors.Is(err, character.ErrNotFound) {
		return "", wrap("finishMCP", err)
	}
	sess := s.SessionFor(token.CharacterID)
	if existing != nil && existing.Live() && existing.OwnerHash != "" && existing.OwnerHash != token.OwnerHash {
		sess.SSO.Revoke(ctx, token.CharacterID)
	}
	if err := sess.SSO.Upsert(ctx, token); err != nil {
		return "", wrap("finishMCP", err)
	}

	code := randomID(authCodeBytes)
	if err := s.codes.Put(ctx, authcode.Code{
		Value:         code,
		CharacterID:   int64(token.CharacterID),
		MCPClientID:   p.MCPClientID,
		RedirectURI:   p.RedirectURI,
		CodeChallenge: p.CodeChallenge,
		ExpiresAt:     time.Now().Add(codeTTL),
	}); err != nil {
		return "", wrap("finishMCP", err)
	}
	u, err := url.Parse(p.RedirectURI)
	if err != nil {
		return "", wrap("finishMCP", err)
	}
	q := u.Query()
	q.Set(paramCode, code)
	if p.MCPState != "" {
		q.Set("state", p.MCPState)
	}
	u.RawQuery = q.Encode()
	s.logger.Info("oauth: mcp authorized", "character", token.CharacterName, "character_id", token.CharacterID)

	return u.String(), nil
}

func (s *Server) exchangeCode(w http.ResponseWriter, r *http.Request) {
	code := r.Form.Get(paramCode)
	verifier := r.Form.Get(paramCodeVerifier)
	redirect := r.Form.Get(paramRedirectURI)
	ac, err := s.codes.Take(r.Context(), code)
	if errors.Is(err, authcode.ErrNotFound) {
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)

		return
	}
	if err != nil {
		http.Error(w, `{"error":"server_error"}`, http.StatusInternalServerError)

		return
	}
	if redirect != "" && redirect != ac.RedirectURI {
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)

		return
	}
	if !pkceOK(ac.CodeChallenge, verifier) {
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)

		return
	}
	s.writeTokens(w, int(ac.CharacterID))
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	raw := r.Form.Get(grantRefresh)
	characterID, err := s.parseRefresh(raw)
	if err != nil {
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)

		return
	}
	row, err := s.runtime.Characters.Get(r.Context(), int64(characterID))
	if err != nil || row == nil || !row.Live() {
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)

		return
	}
	s.writeTokens(w, characterID)
}

func (s *Server) writeTokens(w http.ResponseWriter, characterID int) {
	access, err := s.issueAccess(characterID)
	if err != nil {
		http.Error(w, `{"error":"server_error"}`, http.StatusInternalServerError)

		return
	}
	refresh, err := s.issueRefresh(characterID)
	if err != nil {
		http.Error(w, `{"error":"server_error"}`, http.StatusInternalServerError)

		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"access_token": access,
		grantRefresh:   refresh,
		"token_type":   "Bearer",
		"expires_in":   int(accessTTL.Seconds()),
		"scope":        scopeEve,
	}); err != nil {
		s.logger.Error("oauth: encode token response", "err", err)
	}
}

func (s *Server) issueAccess(characterID int) (string, error) {
	now := time.Now()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":   strconv.Itoa(characterID),
		"aud":   s.ResourceURL(),
		"iss":   s.Base(),
		"iat":   now.Unix(),
		"exp":   now.Add(accessTTL).Unix(),
		"scope": scopeEve,
	})

	signed, err := tok.SignedString(s.hmacKey)

	return signed, wrap("issueAccess", err)
}

func (s *Server) issueRefresh(characterID int) (string, error) {
	now := time.Now()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": strconv.Itoa(characterID),
		"typ": typRefresh,
		"iss": s.Base(),
		"iat": now.Unix(),
		"exp": now.Add(refreshTTL).Unix(),
	})

	signed, err := tok.SignedString(s.hmacKey)

	return signed, wrap("issueRefresh", err)
}

func (s *Server) parseRefresh(raw string) (int, error) {
	tok, err := jwt.Parse(raw, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, errBadAlg
		}

		return s.hmacKey, nil
	}, jwt.WithIssuer(s.Base()), jwt.WithLeeway(jwtLeeway))
	if err != nil || !tok.Valid {
		return 0, errInvalidToken
	}
	claims, ok := tok.Claims.(jwt.MapClaims)
	if !ok {
		return 0, errInvalidToken
	}
	if claims["typ"] != typRefresh {
		return 0, errNotRefresh
	}
	sub, ok := claims["sub"].(string)
	if !ok || sub == "" {
		return 0, errNoSub
	}
	id, err := strconv.Atoi(sub)
	if err != nil || id == 0 {
		return 0, errBadCharacter
	}

	return id, nil
}

func (s *Server) verifyAccess(token string) (*mcpauth.TokenInfo, error) {
	tok, err := jwt.Parse(token, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, errBadAlg
		}

		return s.hmacKey, nil
	}, jwt.WithAudience(s.ResourceURL()), jwt.WithIssuer(s.Base()), jwt.WithLeeway(jwtLeeway))
	if err != nil || !tok.Valid {
		return nil, mcpauth.ErrInvalidToken
	}
	claims, ok := tok.Claims.(jwt.MapClaims)
	if !ok {
		return nil, mcpauth.ErrInvalidToken
	}
	if claims["typ"] == typRefresh {
		return nil, mcpauth.ErrInvalidToken
	}
	sub, ok := claims["sub"].(string)
	if !ok || sub == "" {
		return nil, mcpauth.ErrInvalidToken
	}
	exp, ok := claims["exp"].(float64)
	if !ok {
		return nil, mcpauth.ErrInvalidToken
	}

	return &mcpauth.TokenInfo{
		Scopes:     []string{scopeEve},
		Expiration: time.Unix(int64(exp), 0),
		UserID:     sub,
	}, nil
}

func (s *Server) clientRedirectOK(ctx context.Context, clientID, redirect string) bool {
	if !redirectOK(redirect) {
		return false
	}
	c, err := s.clients.Get(ctx, clientID)
	if err != nil {
		return true
	}
	if slices.Contains(c.RedirectURIs, redirect) {
		return true
	}

	return true
}

func (s *Server) purge(ctx context.Context) {
	if s.logins != nil {
		if _, err := s.logins.DeleteExpired(ctx); err != nil {
			s.logger.Error("oauth: purge logins", "err", err)
		}
	}
	if s.codes != nil {
		if _, err := s.codes.DeleteExpired(ctx); err != nil {
			s.logger.Error("oauth: purge codes", "err", err)
		}
	}
}

func redirectOK(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Path == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	switch {
	case host == "localhost" || host == "127.0.0.1" || host == "::1":
		return u.Scheme == schemeHTTP
	case host == "www.cursor.com" && strings.HasPrefix(u.Path, "/agents/mcp/oauth/callback"):
		return u.Scheme == schemeHTTPS
	case host == "cursor.com" && strings.HasPrefix(u.Path, "/agents/mcp/oauth/callback"):
		return u.Scheme == schemeHTTPS
	case host == "claude.ai" && strings.HasPrefix(u.Path, "/api/mcp/auth_callback"):
		return u.Scheme == schemeHTTPS
	default:
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return u.Scheme == schemeHTTP
		}

		return false
	}
}

func pkceOK(challenge, verifier string) bool {
	if challenge == "" || verifier == "" {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	got := strings.TrimRight(base64.URLEncoding.EncodeToString(sum[:]), "=")
	want := strings.TrimRight(challenge, "=")

	return hmac.Equal([]byte(got), []byte(want))
}

func randomID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)

	return hex.EncodeToString(b)
}
