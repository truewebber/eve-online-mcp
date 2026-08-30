package sso

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	SSOBase       = "https://login.eveonline.com"
	AuthorizeURL  = SSOBase + "/v2/oauth/authorize"
	TokenURL      = SSOBase + "/v2/oauth/token"
	RevokeURL     = SSOBase + "/v2/oauth/revoke"
	JWKSURL       = SSOBase + "/oauth/jwks"
	TokenAudience = "EVE Online"
)

var TokenIssuers = []string{"login.eveonline.com", "https://login.eveonline.com"}

// Options is the EVE SSO client config. Built at the composition root.
type Options struct {
	ClientID     string
	ClientSecret string
	CallbackURL  string
	TokenFile    string
	UserAgent    string
	Scopes       []string
}

const (
	refreshMargin = 60 * time.Second
	loginTTL      = 15 * time.Minute
)

type Error struct{ Msg string }

func (e Error) Error() string { return e.Msg }

func Err(msg string) error { return Error{Msg: msg} }

type CharacterToken struct {
	CharacterID     int      `json:"character_id"`
	CharacterName   string   `json:"character_name"`
	RefreshToken    string   `json:"refresh_token"`
	Scopes          []string `json:"scopes"`
	OwnerHash       string   `json:"owner_hash"`
	AddedAt         float64  `json:"added_at"`
	AccessToken     string   `json:"-"`
	AccessExpiresAt time.Time
}

type pendingLogin struct {
	state     string
	verifier  string
	scopes    []string
	createdAt time.Time
}

type TokenStore struct {
	path   string
	tokens map[int]*CharacterToken
	mu     sync.Mutex
}

// OpenStore opens the token file. An empty path keeps tokens in memory only —
// used by the MCP OAuth login broker, which hands finished tokens to a user store.
func OpenStore(path string) *TokenStore {
	s := &TokenStore{path: path, tokens: map[int]*CharacterToken{}}
	s.load()
	return s
}

// ReadCharacterIDs lists character ids stored in a tokens.json without
// opening a full store. Missing or malformed files yield an empty list.
func ReadCharacterIDs(path string) []int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var payload struct {
		Characters []struct {
			CharacterID int `json:"character_id"`
		} `json:"characters"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return nil
	}
	out := make([]int, 0, len(payload.Characters))
	for _, c := range payload.Characters {
		if c.CharacterID != 0 {
			out = append(out, c.CharacterID)
		}
	}
	return out
}

func (s *TokenStore) load() {
	if s.path == "" {
		return
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var payload struct {
		Characters []CharacterToken `json:"characters"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		log.Printf("could not read token file %s: %v", s.path, err)
		return
	}
	for i := range payload.Characters {
		t := payload.Characters[i]
		if t.CharacterID == 0 || t.RefreshToken == "" {
			continue
		}
		cp := t
		s.tokens[t.CharacterID] = &cp
	}
}

func (s *TokenStore) flush() error {
	if s.path == "" {
		return nil
	}
	chars := make([]CharacterToken, 0, len(s.tokens))
	for _, t := range s.tokens {
		chars = append(chars, CharacterToken{
			CharacterID: t.CharacterID, CharacterName: t.CharacterName,
			RefreshToken: t.RefreshToken, Scopes: t.Scopes,
			OwnerHash: t.OwnerHash, AddedAt: t.AddedAt,
		})
	}
	raw, err := json.MarshalIndent(map[string]any{"version": 1, "characters": chars}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *TokenStore) Upsert(token *CharacterToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing := s.tokens[token.CharacterID]; existing != nil {
		if token.AccessToken == "" {
			token.AccessToken = existing.AccessToken
			token.AccessExpiresAt = existing.AccessExpiresAt
		}
		if token.AddedAt == 0 {
			token.AddedAt = existing.AddedAt
		}
	}
	if token.AddedAt == 0 {
		token.AddedAt = float64(time.Now().Unix())
	}
	s.tokens[token.CharacterID] = token
	return s.flush()
}

func (s *TokenStore) Remove(id int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tokens[id]; !ok {
		return false
	}
	delete(s.tokens, id)
	_ = s.flush()
	return true
}

func (s *TokenStore) Get(id int) *CharacterToken {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokens[id]
}

func (s *TokenStore) All() []*CharacterToken {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*CharacterToken, 0, len(s.tokens))
	for _, t := range s.tokens {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].CharacterName) < strings.ToLower(out[j].CharacterName)
	})
	return out
}

func (s *TokenStore) FindByName(name string) *CharacterToken {
	lowered := strings.ToLower(strings.TrimSpace(name))
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.tokens {
		if strings.ToLower(t.CharacterName) == lowered {
			return t
		}
	}
	for _, t := range s.tokens {
		if lowered != "" && strings.Contains(strings.ToLower(t.CharacterName), lowered) {
			return t
		}
	}
	return nil
}

type Client struct {
	opts         Options
	http         *http.Client
	Store        *TokenStore
	pending      map[string]pendingLogin
	pendingMu    sync.Mutex
	refreshLocks sync.Map
	jwks         map[string]any
	jwksAt       time.Time
	jwksMu       sync.Mutex
}

func New(opts Options, httpClient *http.Client) *Client {
	return &Client{
		opts:    opts,
		http:    httpClient,
		Store:   OpenStore(opts.TokenFile),
		pending: map[string]pendingLogin{},
	}
}

func (c *Client) BuildLogin(scopes []string) (string, string, error) {
	if c.opts.ClientID == "" {
		return "", "", Err("EVE CLIENT_ID is not configured on this server.")
	}
	c.expirePending()
	if scopes == nil {
		scopes = append([]string{}, c.opts.Scopes...)
	}
	verifier := b64url(random(32))
	sum := sha256.Sum256([]byte(verifier))
	challenge := b64url(sum[:])
	state := b64url(random(16))
	c.pendingMu.Lock()
	c.pending[state] = pendingLogin{
		state: state, verifier: verifier, scopes: scopes, createdAt: time.Now(),
	}
	c.pendingMu.Unlock()
	q := url.Values{
		"response_type":         {"code"},
		"redirect_uri":          {c.opts.CallbackURL},
		"client_id":             {c.opts.ClientID},
		"scope":                 {strings.Join(scopes, " ")},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	return AuthorizeURL + "?" + q.Encode(), state, nil
}

// HasPending reports whether this client started the login with that state.
func (c *Client) HasPending(state string) bool {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	_, ok := c.pending[state]
	return ok
}

func (c *Client) expirePending() {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	cutoff := time.Now().Add(-loginTTL)
	for k, p := range c.pending {
		if p.createdAt.Before(cutoff) {
			delete(c.pending, k)
		}
	}
}

func (c *Client) CompleteLogin(code, state string) (*CharacterToken, error) {
	c.pendingMu.Lock()
	pending, ok := c.pending[state]
	if ok {
		delete(c.pending, state)
	}
	c.pendingMu.Unlock()
	if !ok {
		return nil, Err("Unknown or expired login state — start the login again.")
	}
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {c.opts.ClientID},
		"code_verifier": {pending.verifier},
		"redirect_uri":  {c.opts.CallbackURL},
	}
	payload, err := c.tokenRequest(data, c.opts.ClientID, c.opts.ClientSecret)
	if err != nil {
		return nil, err
	}
	token, err := c.tokenFromPayload(payload, nil)
	if err != nil {
		return nil, err
	}
	if err := c.Store.Upsert(token); err != nil {
		return nil, err
	}
	log.Printf("authorized %s (%d) with %d scopes", token.CharacterName, token.CharacterID, len(token.Scopes))
	return token, nil
}

func (c *Client) AccessToken(characterID int) (*CharacterToken, error) {
	token := c.Store.Get(characterID)
	if token == nil {
		return nil, Err(fmt.Sprintf("Character %d is not authorized. Run the login flow first.", characterID))
	}
	if token.AccessToken != "" && time.Now().Before(token.AccessExpiresAt.Add(-refreshMargin)) {
		return token, nil
	}
	lockI, _ := c.refreshLocks.LoadOrStore(characterID, &sync.Mutex{})
	lock := lockI.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	token = c.Store.Get(characterID)
	if token == nil {
		return nil, Err(fmt.Sprintf("Character %d was removed during refresh.", characterID))
	}
	if token.AccessToken != "" && time.Now().Before(token.AccessExpiresAt.Add(-refreshMargin)) {
		return token, nil
	}
	return c.refresh(token)
}

func (c *Client) refresh(token *CharacterToken) (*CharacterToken, error) {
	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {token.RefreshToken},
		"client_id":     {c.opts.ClientID},
	}
	payload, err := c.tokenRequest(data, c.opts.ClientID, c.opts.ClientSecret)
	if err != nil {
		if strings.Contains(err.Error(), "invalid_grant") {
			c.Store.Remove(token.CharacterID)
			return nil, Err(fmt.Sprintf("Refresh token for %s was revoked or expired. Log this character in again.", token.CharacterName))
		}
		return nil, err
	}
	refreshed, err := c.tokenFromPayload(payload, token)
	if err != nil {
		return nil, err
	}
	if err := c.Store.Upsert(refreshed); err != nil {
		return nil, err
	}
	return refreshed, nil
}

func (c *Client) Revoke(characterID int) {
	token := c.Store.Get(characterID)
	if token == nil {
		return
	}
	data := url.Values{
		"token_type_hint": {"refresh_token"},
		"token":           {token.RefreshToken},
		"client_id":       {c.opts.ClientID},
	}
	req, _ := http.NewRequest(http.MethodPost, RevokeURL, strings.NewReader(data.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", c.opts.UserAgent)
	if c.opts.ClientSecret != "" {
		req.SetBasicAuth(c.opts.ClientID, c.opts.ClientSecret)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		log.Printf("revoke call failed for %d: %v", characterID, err)
	} else {
		resp.Body.Close()
	}
	c.Store.Remove(characterID)
}

func (c *Client) tokenRequest(data url.Values, clientID, secret string) (map[string]any, error) {
	if secret != "" {
		data.Del("client_id")
	}
	req, err := http.NewRequest(http.MethodPost, TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Host", "login.eveonline.com")
	req.Header.Set("User-Agent", c.opts.UserAgent)
	if secret != "" {
		req.SetBasicAuth(clientID, secret)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, Err("SSO token request failed: " + err.Error())
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return nil, Err(fmt.Sprintf("SSO token request failed (%d): %s", resp.StatusCode, ssoDetail(resp.Header.Get("Content-Type"), body)))
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, Err("SSO returned a malformed token payload")
	}
	return payload, nil
}

func (c *Client) tokenFromPayload(payload map[string]any, fallback *CharacterToken) (*CharacterToken, error) {
	access, _ := payload["access_token"].(string)
	refresh, _ := payload["refresh_token"].(string)
	if refresh == "" && fallback != nil {
		refresh = fallback.RefreshToken
	}
	if access == "" || refresh == "" {
		return nil, Err("SSO response was missing access_token or refresh_token.")
	}
	claims, err := c.decode(access)
	if err != nil {
		return nil, err
	}
	subject, _ := claims["sub"].(string)
	if !strings.HasPrefix(subject, "CHARACTER:EVE:") {
		return nil, Err(fmt.Sprintf("Unexpected token subject: %q", subject))
	}
	var characterID int
	fmt.Sscanf(strings.TrimPrefix(subject, "CHARACTER:EVE:"), "%d", &characterID)
	var scopes []string
	switch scp := claims["scp"].(type) {
	case string:
		scopes = []string{scp}
	case []any:
		for _, v := range scp {
			if s, ok := v.(string); ok {
				scopes = append(scopes, s)
			}
		}
	}
	expiresIn := 1200.0
	if v, ok := payload["expires_in"]; ok {
		switch t := v.(type) {
		case float64:
			expiresIn = t
		}
	}
	name, _ := claims["name"].(string)
	owner, _ := claims["owner"].(string)
	if name == "" && fallback != nil {
		name = fallback.CharacterName
	}
	if owner == "" && fallback != nil {
		owner = fallback.OwnerHash
	}
	return &CharacterToken{
		CharacterID:     characterID,
		CharacterName:   name,
		RefreshToken:    refresh,
		Scopes:          scopes,
		OwnerHash:       owner,
		AccessToken:     access,
		AccessExpiresAt: time.Now().Add(time.Duration(expiresIn * float64(time.Second))),
	}, nil
}

func (c *Client) decode(accessToken string) (jwt.MapClaims, error) {
	parser := jwt.NewParser(jwt.WithValidMethods([]string{"RS256", "ES256"}), jwt.WithAudience(TokenAudience), jwt.WithLeeway(30*time.Second))
	key, err := c.signingKey(accessToken)
	if err != nil {
		log.Printf("JWKS unavailable (%v); accepting SSO token on TLS alone", err)
		tok, _, err := jwt.NewParser(jwt.WithoutClaimsValidation()).ParseUnverified(accessToken, jwt.MapClaims{})
		if err != nil {
			return nil, Err("Malformed token from the SSO: " + err.Error())
		}
		claims, _ := tok.Claims.(jwt.MapClaims)
		return c.checkIssuer(claims)
	}
	tok, err := parser.Parse(accessToken, func(t *jwt.Token) (any, error) { return key, nil })
	if err != nil {
		return nil, Err("The SSO token failed verification (" + err.Error() + "). Nothing was stored.")
	}
	claims, _ := tok.Claims.(jwt.MapClaims)
	return c.checkIssuer(claims)
}

func (c *Client) checkIssuer(claims jwt.MapClaims) (jwt.MapClaims, error) {
	iss, _ := claims["iss"].(string)
	for _, allowed := range TokenIssuers {
		if iss == allowed {
			return claims, nil
		}
	}
	return nil, Err(fmt.Sprintf("Unexpected token issuer: %q", iss))
}

func (c *Client) signingKey(accessToken string) (any, error) {
	tok, _, err := jwt.NewParser().ParseUnverified(accessToken, jwt.MapClaims{})
	if err != nil {
		return nil, err
	}
	kid, _ := tok.Header["kid"].(string)
	c.jwksMu.Lock()
	defer c.jwksMu.Unlock()
	if c.jwks == nil || time.Since(c.jwksAt) > time.Hour {
		keys, err := fetchJWKS(c.http)
		if err != nil {
			return nil, err
		}
		c.jwks = keys
		c.jwksAt = time.Now()
	}
	key, ok := c.jwks[kid]
	if !ok {
		return nil, fmt.Errorf("kid %s not in JWKS", kid)
	}
	return key, nil
}

func fetchJWKS(httpClient *http.Client) (map[string]any, error) {
	resp, err := httpClient.Get(JWKSURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var doc struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, err
	}
	out := map[string]any{}
	for _, k := range doc.Keys {
		kid, _ := k["kid"].(string)
		if kid == "" {
			continue
		}
		if pub, err := rsaFromJWK(k); err == nil {
			out[kid] = pub
		}
	}
	return out, nil
}

func rsaFromJWK(k map[string]any) (*rsa.PublicKey, error) {
	nStr, _ := k["n"].(string)
	eStr, _ := k["e"].(string)
	if nStr == "" || eStr == "" {
		return nil, fmt.Errorf("not rsa")
	}
	nBytes, err := base64.RawURLEncoding.DecodeString(nStr)
	if err != nil {
		return nil, err
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eStr)
	if err != nil {
		return nil, err
	}
	var e int
	for _, b := range eBytes {
		e = e<<8 | int(b)
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: e}, nil
}

func ssoDetail(contentType string, body []byte) string {
	if strings.Contains(contentType, "json") {
		var payload map[string]any
		if json.Unmarshal(body, &payload) == nil {
			errS, _ := payload["error"].(string)
			if errS == "" {
				errS, _ = payload["message"].(string)
			}
			desc, _ := payload["error_description"].(string)
			parts := []string{}
			if errS != "" {
				parts = append(parts, errS)
			}
			if desc != "" {
				parts = append(parts, desc)
			}
			if len(parts) > 0 {
				return strings.Join(parts, " - ")
			}
		}
	}
	if strings.Contains(contentType, "html") {
		return "the SSO rejected the request (bad client_id, wrong callback URL, or a refresh token that is no longer valid)"
	}
	if len(body) > 200 {
		return string(body[:200])
	}
	return string(body)
}

func b64url(raw []byte) string {
	return strings.TrimRight(base64.URLEncoding.EncodeToString(raw), "=")
}

func random(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}
