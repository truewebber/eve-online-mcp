package sso

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"eve-mcp/internal/adapter/store"

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
	UserAgent    string
	Scopes       []string
	DB           *store.Store
	UserID       string // empty = in-memory broker (MCP authorize)
}

const (
	refreshMargin = 60 * time.Second
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

// PreparedLogin is an EVE SSO authorize URL plus the PKCE verifier the
// callback must present. The caller persists State + Verifier (login_states)
// so any replica can finish the handshake.
type PreparedLogin struct {
	URL      string
	State    string
	Verifier string
	Scopes   []string
}

type Client struct {
	opts         Options
	http         *http.Client
	Store        *TokenStore
	refreshLocks sync.Map
	jwks         map[string]any
	jwksAt       time.Time
	jwksMu       sync.Mutex
}

func New(opts Options, httpClient *http.Client) *Client {
	return &Client{
		opts:  opts,
		http:  httpClient,
		Store: newTokenStore(opts.DB, opts.UserID),
	}
}

func (c *Client) PrepareLogin(scopes []string) (*PreparedLogin, error) {
	if c.opts.ClientID == "" {
		return nil, Err("EVE CLIENT_ID is not configured on this server.")
	}
	if scopes == nil {
		scopes = append([]string{}, c.opts.Scopes...)
	}
	verifier := b64url(random(32))
	sum := sha256.Sum256([]byte(verifier))
	challenge := b64url(sum[:])
	state := b64url(random(16))
	q := url.Values{
		"response_type":         {"code"},
		"redirect_uri":          {c.opts.CallbackURL},
		"client_id":             {c.opts.ClientID},
		"scope":                 {strings.Join(scopes, " ")},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	return &PreparedLogin{
		URL:      AuthorizeURL + "?" + q.Encode(),
		State:    state,
		Verifier: verifier,
		Scopes:   scopes,
	}, nil
}

// ExchangeCode trades an EVE authorization code for tokens. The PKCE
// verifier comes from the persisted login_states row, not process memory.
func (c *Client) ExchangeCode(code, verifier string) (*CharacterToken, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {c.opts.ClientID},
		"code_verifier": {verifier},
		"redirect_uri":  {c.opts.CallbackURL},
	}
	payload, err := c.tokenRequest(data, c.opts.ClientID, c.opts.ClientSecret)
	if err != nil {
		return nil, err
	}
	return c.tokenFromPayload(payload, nil)
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
	if c.Store.durable() {
		return c.refreshLocked(token)
	}
	return c.refreshMemory(token)
}

func (c *Client) refreshLocked(token *CharacterToken) (*CharacterToken, error) {
	var out *CharacterToken
	err := c.Store.db.WithCharacterForUpdate(context.Background(), int64(token.CharacterID), func(refreshToken string) (string, error) {
		refreshed, err := c.exchangeRefresh(refreshToken, token)
		if err != nil {
			return "", err
		}
		out = refreshed
		return refreshed.RefreshToken, nil
	})
	if err != nil {
		if strings.Contains(err.Error(), "invalid_grant") {
			c.Store.Remove(token.CharacterID)
			return nil, Err(fmt.Sprintf("Refresh token for %s was revoked or expired. Log this character in again.", token.CharacterName))
		}
		if errors.Is(err, store.ErrNotFound) {
			return nil, Err(fmt.Sprintf("Character %d was removed during refresh.", token.CharacterID))
		}
		return nil, err
	}
	c.Store.setAccess(out)
	return out, nil
}

func (c *Client) refreshMemory(token *CharacterToken) (*CharacterToken, error) {
	refreshed, err := c.exchangeRefresh(token.RefreshToken, token)
	if err != nil {
		if strings.Contains(err.Error(), "invalid_grant") {
			c.Store.Remove(token.CharacterID)
			return nil, Err(fmt.Sprintf("Refresh token for %s was revoked or expired. Log this character in again.", token.CharacterName))
		}
		return nil, err
	}
	if err := c.Store.Upsert(refreshed); err != nil {
		return nil, err
	}
	return refreshed, nil
}

func (c *Client) exchangeRefresh(refreshToken string, fallback *CharacterToken) (*CharacterToken, error) {
	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {c.opts.ClientID},
	}
	payload, err := c.tokenRequest(data, c.opts.ClientID, c.opts.ClientSecret)
	if err != nil {
		return nil, err
	}
	return c.tokenFromPayload(payload, fallback)
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
