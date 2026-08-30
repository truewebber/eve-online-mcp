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
	"log"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	"github.com/truewebber/eve-online-mcp/internal/adapter/store"
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
	jwtSecretName = "mcp_jwt_hmac"
)

var (
	// ErrUnknownLogin is returned when the EVE callback state is missing or expired.
	ErrUnknownLogin = errors.New("Unknown or expired login state — start the login again.") //nolint:revive // shown on the login-failed HTML page
	// ErrStoreRequired is returned when Open is called without a Postgres store.
	ErrStoreRequired = errors.New("oauth: postgres store is required")
	// ErrHMACTooShort is returned when the persisted JWT HMAC is shorter than 32 bytes.
	ErrHMACTooShort = errors.New("oauth: mcp_jwt_hmac is too short")
	// ErrUnknownLoginKind is returned when a login_states row has an unexpected kind.
	ErrUnknownLoginKind = errors.New("unknown login kind")
	errAltMissingUser   = errors.New("alt login is missing user_id")
	errBadAlg           = errors.New("alg")
	errInvalidToken     = errors.New("invalid")
	errNotRefresh       = errors.New("not refresh")
	errNoSub            = errors.New("no sub")
)

// CharacterOwnedError is an alt-add refused because the character already
// belongs to a different user. The HTML callback page shows Error().
type CharacterOwnedError struct {
	CharacterName string
}

func (e CharacterOwnedError) Error() string {
	name := e.CharacterName
	if name == "" {
		name = "This character"
	}

	return name + " already belongs to another user on this server. Call eve_auth_logout on that other session first, or sign in from your MCP client as this character (Authentication required) to use that account."
}

// Host is the public HTTP identity of this process. Built at the composition root.
type Host struct {
	Listen      string
	PublicURL   string
	MCPPath     string
	CallbackURL string
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

	return "http://" + net.JoinHostPort(host, port)
}

type Server struct {
	pub     Host
	db      *store.Store
	runtime *session.Session
	// login talks to CCP for MCP authorize and for completing any replica's
	// handshake. Character tokens are written to the owning user's store.
	login    *sso.Client
	hmacKey  []byte
	sessions sync.Map
}

func Open(pub Host, runtime *session.Session, db *store.Store) (*Server, error) {
	if pub.MCPPath == "" {
		pub.MCPPath = "/mcp"
	}
	if db == nil {
		return nil, ErrStoreRequired
	}
	key, err := db.GetOrCreateSecret(context.Background(), jwtSecretName)
	if err != nil {
		return nil, err
	}
	if len(key) < 32 {
		return nil, ErrHMACTooShort
	}
	brokerOpts := runtime.Opts.SSO
	brokerOpts.UserID = ""
	brokerOpts.DB = nil

	return &Server{
		pub:     pub,
		db:      db,
		runtime: runtime,
		login:   sso.New(brokerOpts, runtime.HTTP),
		hmacKey: key,
	}, nil
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

func (s *Server) ServeASMeta(w http.ResponseWriter, _ *http.Request) {
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
	id := randomID(16)
	err = s.db.PutClient(r.Context(), store.Client{ID: id, RedirectURIs: allowed})
	if err != nil {
		http.Error(w, `{"error":"server_error"}`, http.StatusInternalServerError)

		return
	}
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
	if err := s.db.PutLoginState(r.Context(), store.LoginState{
		State:         prep.State,
		PKCEVerifier:  prep.Verifier,
		Scopes:        prep.Scopes,
		Kind:          store.LoginMCP,
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

// CompleteCallback finishes an EVE SSO callback using the login_states
// row: MCP authorize issues a client redirect; alt login attaches the
// character to the user recorded on the row.
func (s *Server) CompleteCallback(ctx context.Context, code, eveState string) (redirect string, token *sso.CharacterToken, err error) {
	s.purge(ctx)
	st, ok, err := s.db.TakeLoginState(ctx, eveState)
	if err != nil {
		return "", nil, err
	}
	if !ok {
		return "", nil, ErrUnknownLogin
	}
	token, err = s.login.ExchangeCode(code, st.PKCEVerifier)
	if err != nil {
		return "", nil, err
	}
	switch st.Kind {
	case store.LoginMCP:
		loc, err := s.finishMCP(ctx, st, token)

		return loc, token, err
	case store.LoginAlt:
		err := s.finishAlt(ctx, st, token)
		if err != nil {
			return "", token, err
		}

		return "", token, nil
	default:
		return "", token, fmt.Errorf("%w: %q", ErrUnknownLoginKind, st.Kind)
	}
}

func (s *Server) finishMCP(ctx context.Context, p *store.LoginState, token *sso.CharacterToken) (string, error) {
	userID, err := s.ownerOf(ctx, token.CharacterID)
	if err != nil {
		return "", err
	}
	if userID == "" {
		u, err := s.db.CreateUser(ctx)
		if err != nil {
			return "", err
		}
		userID = u.ID
	}
	if err := s.SessionFor(userID).SSO.Store.Upsert(token); err != nil {
		return "", err
	}

	code := randomID(24)
	if err := s.db.PutAuthCode(ctx, store.AuthCode{
		Code:          code,
		UserID:        userID,
		MCPClientID:   p.MCPClientID,
		RedirectURI:   p.RedirectURI,
		CodeChallenge: p.CodeChallenge,
		ExpiresAt:     time.Now().Add(codeTTL),
	}); err != nil {
		return "", err
	}
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

func (s *Server) finishAlt(ctx context.Context, st *store.LoginState, token *sso.CharacterToken) error {
	if st.UserID == "" {
		return errAltMissingUser
	}
	owner, err := s.ownerOf(ctx, token.CharacterID)
	if err != nil {
		return err
	}
	if owner != "" && owner != st.UserID {
		return CharacterOwnedError{CharacterName: token.CharacterName}
	}
	if err := s.SessionFor(st.UserID).SSO.Store.Upsert(token); err != nil {
		if errors.Is(err, store.ErrOwned) {
			return CharacterOwnedError{CharacterName: token.CharacterName}
		}

		return err
	}
	log.Printf("alt oauth: %s attached to user %s", token.CharacterName, st.UserID)

	return nil
}

// ownerOf finds the user who already holds this character, if any.
func (s *Server) ownerOf(ctx context.Context, characterID int) (string, error) {
	userID, ok, err := s.db.OwnerOf(ctx, int64(characterID))
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}

	return userID, nil
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
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		s.exchangeCode(w, r)
	case "refresh_token":
		s.refresh(w, r)
	default:
		http.Error(w, `{"error":"unsupported_grant_type"}`, http.StatusBadRequest)
	}
}

func (s *Server) exchangeCode(w http.ResponseWriter, r *http.Request) {
	code := r.Form.Get("code")
	verifier := r.Form.Get("code_verifier")
	redirect := r.Form.Get("redirect_uri")
	ac, ok, err := s.db.TakeAuthCode(r.Context(), code)
	if err != nil {
		http.Error(w, `{"error":"server_error"}`, http.StatusInternalServerError)

		return
	}
	if !ok {
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)

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
	s.writeTokens(w, ac.UserID)
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	raw := r.Form.Get("refresh_token")
	userID, err := s.parseRefresh(raw)
	if err != nil {
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)

		return
	}
	if _, err := s.db.GetUser(r.Context(), userID); err != nil {
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)

		return
	}
	s.writeTokens(w, userID)
}

func (s *Server) writeTokens(w http.ResponseWriter, userID string) {
	access, err := s.issueAccess(userID)
	if err != nil {
		http.Error(w, `{"error":"server_error"}`, http.StatusInternalServerError)

		return
	}
	refresh, err := s.issueRefresh(userID)
	if err != nil {
		http.Error(w, `{"error":"server_error"}`, http.StatusInternalServerError)

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
			return nil, errBadAlg
		}

		return s.hmacKey, nil
	}, jwt.WithIssuer(s.Base()), jwt.WithLeeway(30*time.Second))
	if err != nil || !tok.Valid {
		return "", errInvalidToken
	}
	claims, _ := tok.Claims.(jwt.MapClaims)
	if claims["typ"] != "refresh" {
		return "", errNotRefresh
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return "", errNoSub
	}

	return sub, nil
}

func (s *Server) VerifyAccess(_ context.Context, token string, _ *http.Request) (*mcpauth.TokenInfo, error) {
	return s.verifyAccess(token)
}

func (s *Server) verifyAccess(token string) (*mcpauth.TokenInfo, error) {
	tok, err := jwt.Parse(token, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, errBadAlg
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
	sess := s.runtime.ForUser(userID)
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
		if _, err := s.db.GetUser(r.Context(), ti.UserID); err != nil {
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

func (s *Server) clientRedirectOK(ctx context.Context, clientID, redirect string) bool {
	if !redirectOK(redirect) {
		return false
	}
	c, ok, err := s.db.GetClient(ctx, clientID)
	if err != nil || !ok {
		return true
	}
	if slices.Contains(c.RedirectURIs, redirect) {
		return true
	}

	return true
}

func (s *Server) purge(ctx context.Context) {
	if s.db == nil {
		return
	}
	if _, err := s.db.PurgeExpired(ctx); err != nil {
		log.Printf("oauth: purge: %v", err)
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
