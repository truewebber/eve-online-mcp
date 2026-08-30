package oauth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"eve-mcp/internal/adapter/sso"
	"eve-mcp/internal/adapter/user"
	"eve-mcp/internal/usecase/session"

	"github.com/golang-jwt/jwt/v5"
	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

const (
	accessTTL  = time.Hour
	refreshTTL = 30 * 24 * time.Hour
	codeTTL    = 2 * time.Minute
	scopeEve   = "eve"
)

type evePending struct {
	MCPClientID   string
	RedirectURI   string
	MCPState      string
	CodeChallenge string
	CreatedAt     time.Time
}

type authCode struct {
	UserID        string
	MCPClientID   string
	RedirectURI   string
	CodeChallenge string
	ExpiresAt     time.Time
}

// Host is the public HTTP identity of this process. Built at the composition root.
type Host struct {
	DataDir     string
	Listen      string
	PublicURL   string
	MCPPath     string
	CallbackURL string
	WriteMode   string
}

func (h Host) BaseURL() string {
	if h.PublicURL != "" {
		return strings.TrimRight(h.PublicURL, "/")
	}
	host, port, err := net.SplitHostPort(h.Listen)
	if err != nil {
		return "http://127.0.0.1:8765"
	}
	if host == "0.0.0.0" || host == "::" || host == "" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s:%s", host, port)
}

type Server struct {
	pub     Host
	users   *user.Store
	runtime *session.Session
	// login starts EVE SSO handshakes for MCP authorize flows. Its token
	// store is in-memory: finished tokens are upserted into the user's store.
	login    *sso.Client
	hmacKey  []byte
	mu       sync.Mutex
	clients  map[string]registeredClient
	pending  map[string]evePending
	codes    map[string]authCode
	sessions sync.Map
}

type registeredClient struct {
	ID           string   `json:"client_id"`
	RedirectURIs []string `json:"redirect_uris"`
}

func Open(pub Host, runtime *session.Session, users *user.Store) (*Server, error) {
	if pub.MCPPath == "" {
		pub.MCPPath = "/mcp"
	}
	dir := filepath.Join(pub.DataDir, "oauth")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	key, err := loadOrCreateKey(filepath.Join(dir, "hmac.key"))
	if err != nil {
		return nil, err
	}
	brokerOpts := runtime.Opts.SSO
	brokerOpts.TokenFile = ""
	s := &Server{
		pub:     pub,
		users:   users,
		runtime: runtime,
		login:   sso.New(brokerOpts, runtime.HTTP),
		hmacKey: key,
		clients: map[string]registeredClient{},
		pending: map[string]evePending{},
		codes:   map[string]authCode{},
	}
	s.loadClients()
	return s, nil
}

func (s *Server) Base() string { return s.pub.BaseURL() }

func (s *Server) ResourceURL() string { return s.Base() + s.pub.MCPPath }

func (s *Server) MetadataURL() string {
	return s.Base() + "/.well-known/oauth-protected-resource"
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
	base := s.Base()
	return &oauthex.AuthServerMeta{
		Issuer:                            base,
		AuthorizationEndpoint:             base + "/oauth/authorize",
		TokenEndpoint:                     base + "/oauth/token",
		RegistrationEndpoint:              base + "/oauth/register",
		ResponseTypesSupported:            []string{"code"},
		GrantTypesSupported:               []string{"authorization_code", "refresh_token"},
		CodeChallengeMethodsSupported:     []string{"S256"},
		TokenEndpointAuthMethodsSupported: []string{"none", "client_secret_post", "client_secret_basic"},
		ScopesSupported:                   []string{scopeEve},
	}
}

func (s *Server) ProtectedResourceHandler() http.Handler {
	return mcpauth.ProtectedResourceMetadataHandler(s.ProtectedResource())
}

func (s *Server) ServeASMeta(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.AuthServerMeta())
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.RedirectURIs) == 0 {
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
	id := randomID(16)
	s.mu.Lock()
	s.clients[id] = registeredClient{ID: id, RedirectURIs: allowed}
	s.mu.Unlock()
	s.saveClients()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"client_id":                  id,
		"redirect_uris":              allowed,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
		"client_name":                req.ClientName,
	})
}

// ServeAuthorize validates the MCP client and immediately redirects the
// browser to EVE SSO with the process application. No form: the instance
// owns the one EVE application, the player only picks a character at CCP.
func (s *Server) ServeAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	mcpClient := q.Get("client_id")
	redirect := q.Get("redirect_uri")
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
	if !s.clientRedirectOK(mcpClient, redirect) {
		http.Error(w, "unknown client or redirect_uri", 400)
		return
	}

	loginURL, eveState, err := s.login.BuildLogin(nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.mu.Lock()
	s.pending[eveState] = evePending{
		MCPClientID: mcpClient, RedirectURI: redirect,
		MCPState: state, CodeChallenge: challenge, CreatedAt: time.Now(),
	}
	s.mu.Unlock()
	http.Redirect(w, r, loginURL, http.StatusFound)
}

// SSOForState picks the SSO client that started this EVE login: the MCP
// authorize broker, or a user session that ran eve_auth_login_url to add
// an alt. Returns nil for unknown states.
func (s *Server) SSOForState(eveState string) *sso.Client {
	if s.login.HasPending(eveState) {
		return s.login
	}
	var found *sso.Client
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*session.Session)
		if sess.SSO.HasPending(eveState) {
			found = sess.SSO
			return false
		}
		return true
	})
	return found
}

// FinishEVE completes an MCP authorize flow: the character has logged in,
// now attach it to a user (an existing one if this character is already
// known, else a fresh one) and send the client its authorization code.
// Returns "" when the state belongs to a tool-started login instead.
func (s *Server) FinishEVE(eveState string, token *sso.CharacterToken) (string, error) {
	s.mu.Lock()
	p, ok := s.pending[eveState]
	if ok {
		delete(s.pending, eveState)
	}
	s.mu.Unlock()
	if !ok {
		return "", nil
	}
	if time.Since(p.CreatedAt) > 15*time.Minute {
		return "", fmt.Errorf("unknown or expired login")
	}

	userID := s.ownerOf(token.CharacterID)
	if userID == "" {
		u, err := s.users.Create()
		if err != nil {
			return "", err
		}
		userID = u.ID
	}
	if err := s.SessionFor(userID).SSO.Store.Upsert(token); err != nil {
		return "", err
	}

	code := randomID(24)
	s.mu.Lock()
	s.codes[code] = authCode{
		UserID: userID, MCPClientID: p.MCPClientID, RedirectURI: p.RedirectURI,
		CodeChallenge: p.CodeChallenge, ExpiresAt: time.Now().Add(codeTTL),
	}
	s.mu.Unlock()
	u, err := url.Parse(p.RedirectURI)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("code", code)
	if p.MCPState != "" {
		q.Set("state", p.MCPState)
	}
	u.RawQuery = q.Encode()
	log.Printf("mcp oauth: %s authorized user %s, redirecting client", token.CharacterName, userID)
	return u.String(), nil
}

// ownerOf finds the user who already holds this character, if any.
func (s *Server) ownerOf(characterID int) string {
	users, err := s.users.List()
	if err != nil {
		return ""
	}
	for _, u := range users {
		for _, id := range sso.ReadCharacterIDs(filepath.Join(u.Dir, "tokens.json")) {
			if id == characterID {
				return u.ID
			}
		}
	}
	return ""
}

func (s *Server) ServeToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, `{"error":"invalid_request"}`, 400)
		return
	}
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		s.exchangeCode(w, r)
	case "refresh_token":
		s.refresh(w, r)
	default:
		http.Error(w, `{"error":"unsupported_grant_type"}`, 400)
	}
}

func (s *Server) exchangeCode(w http.ResponseWriter, r *http.Request) {
	code := r.Form.Get("code")
	verifier := r.Form.Get("code_verifier")
	redirect := r.Form.Get("redirect_uri")
	s.mu.Lock()
	ac, ok := s.codes[code]
	if ok {
		delete(s.codes, code)
	}
	s.mu.Unlock()
	if !ok || time.Now().After(ac.ExpiresAt) {
		http.Error(w, `{"error":"invalid_grant"}`, 400)
		return
	}
	if redirect != "" && redirect != ac.RedirectURI {
		http.Error(w, `{"error":"invalid_grant"}`, 400)
		return
	}
	if !pkceOK(ac.CodeChallenge, verifier) {
		http.Error(w, `{"error":"invalid_grant"}`, 400)
		return
	}
	s.writeTokens(w, ac.UserID)
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	raw := r.Form.Get("refresh_token")
	userID, err := s.parseRefresh(raw)
	if err != nil {
		http.Error(w, `{"error":"invalid_grant"}`, 400)
		return
	}
	if _, err := s.users.Get(userID); err != nil {
		http.Error(w, `{"error":"invalid_grant"}`, 400)
		return
	}
	s.writeTokens(w, userID)
}

func (s *Server) writeTokens(w http.ResponseWriter, userID string) {
	access, err := s.issueAccess(userID)
	if err != nil {
		http.Error(w, `{"error":"server_error"}`, 500)
		return
	}
	refresh, err := s.issueRefresh(userID)
	if err != nil {
		http.Error(w, `{"error":"server_error"}`, 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token":  access,
		"refresh_token": refresh,
		"token_type":    "Bearer",
		"expires_in":    int(accessTTL.Seconds()),
		"scope":         scopeEve,
	})
}

func (s *Server) issueAccess(userID string) (string, error) {
	now := time.Now()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":   userID,
		"aud":   s.ResourceURL(),
		"iss":   s.Base(),
		"iat":   now.Unix(),
		"exp":   now.Add(accessTTL).Unix(),
		"scope": scopeEve,
	})
	return tok.SignedString(s.hmacKey)
}

func (s *Server) issueRefresh(userID string) (string, error) {
	now := time.Now()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": userID,
		"typ": "refresh",
		"iss": s.Base(),
		"iat": now.Unix(),
		"exp": now.Add(refreshTTL).Unix(),
	})
	return tok.SignedString(s.hmacKey)
}

func (s *Server) parseRefresh(raw string) (string, error) {
	tok, err := jwt.Parse(raw, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("alg")
		}
		return s.hmacKey, nil
	}, jwt.WithIssuer(s.Base()), jwt.WithLeeway(30*time.Second))
	if err != nil || !tok.Valid {
		return "", fmt.Errorf("invalid")
	}
	claims, _ := tok.Claims.(jwt.MapClaims)
	if claims["typ"] != "refresh" {
		return "", fmt.Errorf("not refresh")
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return "", fmt.Errorf("no sub")
	}
	return sub, nil
}

func (s *Server) VerifyAccess(_ context.Context, token string, _ *http.Request) (*mcpauth.TokenInfo, error) {
	return s.verifyAccess(token)
}

func (s *Server) verifyAccess(token string) (*mcpauth.TokenInfo, error) {
	tok, err := jwt.Parse(token, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("alg")
		}
		return s.hmacKey, nil
	}, jwt.WithAudience(s.ResourceURL()), jwt.WithIssuer(s.Base()), jwt.WithLeeway(30*time.Second))
	if err != nil || !tok.Valid {
		return nil, mcpauth.ErrInvalidToken
	}
	claims, _ := tok.Claims.(jwt.MapClaims)
	if claims["typ"] == "refresh" {
		return nil, mcpauth.ErrInvalidToken
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return nil, mcpauth.ErrInvalidToken
	}
	exp, _ := claims["exp"].(float64)
	return &mcpauth.TokenInfo{
		Scopes:     []string{scopeEve},
		Expiration: time.Unix(int64(exp), 0),
		UserID:     sub,
	}, nil
}

// SessionFor returns the cached per-user session, creating it on first use.
func (s *Server) SessionFor(userID string) *session.Session {
	if v, ok := s.sessions.Load(userID); ok {
		return v.(*session.Session)
	}
	sess := s.runtime.ForUser(s.users.Dir(userID))
	s.sessions.Store(userID, sess)
	return sess
}

func (s *Server) ProtectMCP(next http.Handler) http.Handler {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ti := mcpauth.TokenInfoFromContext(r.Context())
		if ti == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if _, err := s.users.Get(ti.UserID); err != nil {
			http.Error(w, "unknown user", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(session.With(r.Context(), s.SessionFor(ti.UserID))))
	})
	return mcpauth.RequireBearerToken(s.VerifyAccess, &mcpauth.RequireBearerTokenOptions{
		ResourceMetadataURL: s.MetadataURL(),
		Scopes:              []string{scopeEve},
		ClockSkew:           30 * time.Second,
	})(inner)
}

func (s *Server) clientRedirectOK(clientID, redirect string) bool {
	if !redirectOK(redirect) {
		return false
	}
	s.mu.Lock()
	c, ok := s.clients[clientID]
	s.mu.Unlock()
	if !ok {
		// CIMD / static clients we have not seen: still allow known loopback/Cursor/Claude URIs.
		return redirectOK(redirect)
	}
	for _, u := range c.RedirectURIs {
		if u == redirect {
			return true
		}
	}
	return redirectOK(redirect)
}

func (s *Server) loadClients() {
	raw, err := os.ReadFile(filepath.Join(s.pub.DataDir, "oauth", "clients.json"))
	if err != nil {
		return
	}
	var list []registeredClient
	if json.Unmarshal(raw, &list) != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range list {
		s.clients[c.ID] = c
	}
}

func (s *Server) saveClients() {
	s.mu.Lock()
	list := make([]registeredClient, 0, len(s.clients))
	for _, c := range s.clients {
		list = append(list, c)
	}
	s.mu.Unlock()
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(s.pub.DataDir, "oauth", "clients.json"), raw, 0o600)
}

func redirectOK(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Path == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	switch {
	case host == "localhost" || host == "127.0.0.1" || host == "::1":
		return u.Scheme == "http"
	case host == "www.cursor.com" && strings.HasPrefix(u.Path, "/agents/mcp/oauth/callback"):
		return u.Scheme == "https"
	case host == "cursor.com" && strings.HasPrefix(u.Path, "/agents/mcp/oauth/callback"):
		return u.Scheme == "https"
	case host == "claude.ai" && strings.HasPrefix(u.Path, "/api/mcp/auth_callback"):
		return u.Scheme == "https"
	default:
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return u.Scheme == "http"
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

func loadOrCreateKey(path string) ([]byte, error) {
	if raw, err := os.ReadFile(path); err == nil && len(raw) >= 32 {
		return raw, nil
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return nil, err
	}
	return b, nil
}
