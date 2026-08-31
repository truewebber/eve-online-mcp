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

	"github.com/truewebber/eve-online-mcp/internal/adapter/store"
	"github.com/truewebber/eve-online-mcp/internal/domain/j"

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

func knownIssuer(iss string) bool {
	switch iss {
	case "login.eveonline.com", "https://login.eveonline.com":
		return true
	default:
		return false
	}
}

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
	refreshMargin     = 60 * time.Second
	pkceVerifierBytes = 32
	oauthStateBytes   = 16
	maxTokenBody      = 1 << 20
	jwtLeeway         = 30 * time.Second
	bitsPerByte       = 8
	errorBodyPreview  = 200
	detailPartsCap    = 2

	formClientID     = "client_id"
	formRefreshToken = "refresh_token"
)

var (
	errKidNotInJWKS = errors.New("kid not in JWKS")
	errNotRSA       = errors.New("not rsa")
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

// Persist State + Verifier in login_states so any replica can finish the handshake.
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
	verifier := b64url(random(pkceVerifierBytes))
	sum := sha256.Sum256([]byte(verifier))
	challenge := b64url(sum[:])
	state := b64url(random(oauthStateBytes))
	q := url.Values{
		"response_type":         {"code"},
		"redirect_uri":          {c.opts.CallbackURL},
		formClientID:            {c.opts.ClientID},
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

// ExchangeCode takes the PKCE verifier from the persisted login_states row,
// not process memory — any replica can finish the handshake.
func (c *Client) ExchangeCode(ctx context.Context, code, verifier string) (*CharacterToken, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		formClientID:    {c.opts.ClientID},
		"code_verifier": {verifier},
		"redirect_uri":  {c.opts.CallbackURL},
	}
	payload, err := c.tokenRequest(ctx, data, c.opts.ClientID, c.opts.ClientSecret)
	if err != nil {
		return nil, err
	}

	return c.tokenFromPayload(ctx, payload, nil)
}

func (c *Client) AccessToken(ctx context.Context, characterID int) (*CharacterToken, error) {
	token := c.Store.Get(ctx, characterID)
	if token == nil {
		return nil, Err(fmt.Sprintf("Character %d is not authorized. Run the login flow first.", characterID))
	}
	if token.AccessToken != "" && time.Now().Before(token.AccessExpiresAt.Add(-refreshMargin)) {
		return token, nil
	}
	lockI, _ := c.refreshLocks.LoadOrStore(characterID, &sync.Mutex{})
	lock, ok := lockI.(*sync.Mutex)
	if !ok {
		return nil, Err(fmt.Sprintf("refresh lock for character %d has an unexpected type", characterID))
	}
	lock.Lock()
	defer lock.Unlock()
	token = c.Store.Get(ctx, characterID)
	if token == nil {
		return nil, Err(fmt.Sprintf("Character %d was removed during refresh.", characterID))
	}
	if token.AccessToken != "" && time.Now().Before(token.AccessExpiresAt.Add(-refreshMargin)) {
		return token, nil
	}

	return c.refresh(ctx, token)
}

func (c *Client) Revoke(ctx context.Context, characterID int) {
	token := c.Store.Get(ctx, characterID)
	if token == nil {
		return
	}
	data := url.Values{
		"token_type_hint": {formRefreshToken},
		"token":           {token.RefreshToken},
		formClientID:      {c.opts.ClientID},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, RevokeURL, strings.NewReader(data.Encode()))
	if err != nil {
		log.Printf("revoke request for %d: %v", characterID, err)
		c.Store.Remove(ctx, characterID)

		return
	}
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
	c.Store.Remove(ctx, characterID)
}

func (c *Client) refresh(ctx context.Context, token *CharacterToken) (*CharacterToken, error) {
	if c.Store.durable() {
		return c.refreshLocked(ctx, token)
	}

	return c.refreshMemory(ctx, token)
}

func (c *Client) refreshLocked(ctx context.Context, token *CharacterToken) (*CharacterToken, error) {
	var out *CharacterToken
	err := c.Store.db.WithCharacterForUpdate(ctx, int64(token.CharacterID), func(refreshToken string) (string, error) {
		refreshed, err := c.exchangeRefresh(ctx, refreshToken, token)
		if err != nil {
			return "", err
		}
		out = refreshed

		return refreshed.RefreshToken, nil
	})
	if err != nil {
		if strings.Contains(err.Error(), "invalid_grant") {
			c.Store.Remove(ctx, token.CharacterID)

			return nil, Err(fmt.Sprintf("Refresh token for %s was revoked or expired. Log this character in again.", token.CharacterName))
		}
		if errors.Is(err, store.ErrNotFound) {
			return nil, Err(fmt.Sprintf("Character %d was removed during refresh.", token.CharacterID))
		}

		return nil, wrap("refreshLocked", err)
	}
	c.Store.setAccess(out)

	return out, nil
}

func (c *Client) refreshMemory(ctx context.Context, token *CharacterToken) (*CharacterToken, error) {
	refreshed, err := c.exchangeRefresh(ctx, token.RefreshToken, token)
	if err != nil {
		if strings.Contains(err.Error(), "invalid_grant") {
			c.Store.Remove(ctx, token.CharacterID)

			return nil, Err(fmt.Sprintf("Refresh token for %s was revoked or expired. Log this character in again.", token.CharacterName))
		}

		return nil, err
	}
	if err := c.Store.Upsert(ctx, refreshed); err != nil {
		return nil, err
	}

	return refreshed, nil
}

func (c *Client) exchangeRefresh(ctx context.Context, refreshToken string, fallback *CharacterToken) (*CharacterToken, error) {
	data := url.Values{
		"grant_type":     {formRefreshToken},
		formRefreshToken: {refreshToken},
		formClientID:     {c.opts.ClientID},
	}
	payload, err := c.tokenRequest(ctx, data, c.opts.ClientID, c.opts.ClientSecret)
	if err != nil {
		return nil, err
	}

	return c.tokenFromPayload(ctx, payload, fallback)
}

func (c *Client) tokenRequest(ctx context.Context, data url.Values, clientID, secret string) (map[string]any, error) {
	if secret != "" {
		data.Del(formClientID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, wrap("tokenRequest", err)
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
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenBody))
	if err != nil {
		return nil, Err("SSO token request failed: " + err.Error())
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, Err(fmt.Sprintf("SSO token request failed (%d): %s", resp.StatusCode, ssoDetail(resp.Header.Get("Content-Type"), body)))
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, Err("SSO returned a malformed token payload")
	}

	return payload, nil
}

func (c *Client) tokenFromPayload(ctx context.Context, payload map[string]any, fallback *CharacterToken) (*CharacterToken, error) {
	access := j.Str(payload["access_token"])
	refresh := j.Str(payload[formRefreshToken])
	if refresh == "" && fallback != nil {
		refresh = fallback.RefreshToken
	}
	if access == "" || refresh == "" {
		return nil, Err("SSO response was missing access_token or refresh_token.")
	}
	claims, err := c.decode(ctx, access)
	if err != nil {
		return nil, err
	}
	subject := j.Str(claims["sub"])
	if !strings.HasPrefix(subject, "CHARACTER:EVE:") {
		return nil, Err(fmt.Sprintf("Unexpected token subject: %q", subject))
	}
	var characterID int
	if _, err := fmt.Sscanf(strings.TrimPrefix(subject, "CHARACTER:EVE:"), "%d", &characterID); err != nil || characterID == 0 {
		return nil, Err(fmt.Sprintf("Unexpected token subject: %q", subject))
	}
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
		if t, ok := v.(float64); ok {
			expiresIn = t
		}
	}
	name := j.Str(claims["name"])
	owner := j.Str(claims["owner"])
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

func (c *Client) decode(ctx context.Context, accessToken string) (jwt.MapClaims, error) {
	parser := jwt.NewParser(jwt.WithValidMethods([]string{"RS256", "ES256"}), jwt.WithAudience(TokenAudience), jwt.WithLeeway(jwtLeeway))
	key, err := c.signingKey(ctx, accessToken)
	if err != nil {
		log.Printf("JWKS unavailable (%v); accepting SSO token on TLS alone", err)
		tok, _, err := jwt.NewParser(jwt.WithoutClaimsValidation()).ParseUnverified(accessToken, jwt.MapClaims{})
		if err != nil {
			return nil, Err("Malformed token from the SSO: " + err.Error())
		}
		claims, err := jwtMapClaims(tok)
		if err != nil {
			return nil, err
		}

		return c.checkIssuer(claims)
	}
	tok, err := parser.Parse(accessToken, func(_ *jwt.Token) (any, error) { return key, nil })
	if err != nil {
		return nil, Err("The SSO token failed verification (" + err.Error() + "). Nothing was stored.")
	}
	claims, err := jwtMapClaims(tok)
	if err != nil {
		return nil, err
	}

	return c.checkIssuer(claims)
}

func (c *Client) checkIssuer(claims jwt.MapClaims) (jwt.MapClaims, error) {
	iss := j.Str(claims["iss"])
	if knownIssuer(iss) {
		return claims, nil
	}

	return nil, Err(fmt.Sprintf("Unexpected token issuer: %q", iss))
}

func (c *Client) signingKey(ctx context.Context, accessToken string) (any, error) {
	tok, _, err := jwt.NewParser().ParseUnverified(accessToken, jwt.MapClaims{})
	if err != nil {
		return nil, wrap("signingKey", err)
	}
	kid := j.Str(tok.Header["kid"])
	c.jwksMu.Lock()
	defer c.jwksMu.Unlock()
	if c.jwks == nil || time.Since(c.jwksAt) > time.Hour {
		keys, err := fetchJWKS(ctx, c.http)
		if err != nil {
			return nil, err
		}
		c.jwks = keys
		c.jwksAt = time.Now()
	}
	key, ok := c.jwks[kid]
	if !ok {
		return nil, fmt.Errorf("%w: %s", errKidNotInJWKS, kid)
	}

	return key, nil
}

func fetchJWKS(ctx context.Context, httpClient *http.Client) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, JWKSURL, nil)
	if err != nil {
		return nil, wrap("fetchJWKS", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, wrap("fetchJWKS", err)
	}
	defer resp.Body.Close()
	var doc struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, wrap("fetchJWKS", err)
	}
	out := map[string]any{}
	for _, k := range doc.Keys {
		kid := j.Str(k["kid"])
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
	nStr := j.Str(k["n"])
	eStr := j.Str(k["e"])
	if nStr == "" || eStr == "" {
		return nil, errNotRSA
	}
	nBytes, err := base64.RawURLEncoding.DecodeString(nStr)
	if err != nil {
		return nil, wrap("rsaFromJWK", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eStr)
	if err != nil {
		return nil, wrap("rsaFromJWK", err)
	}
	var e int
	for _, b := range eBytes {
		e = e<<bitsPerByte | int(b)
	}

	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: e}, nil
}

func ssoDetail(contentType string, body []byte) string {
	if d := ssoJSONDetail(contentType, body); d != "" {
		return d
	}
	if strings.Contains(contentType, "html") {
		return "the SSO rejected the request (bad client_id, wrong callback URL, or a refresh token that is no longer valid)"
	}
	if len(body) > errorBodyPreview {
		return string(body[:errorBodyPreview])
	}

	return string(body)
}

func ssoJSONDetail(contentType string, body []byte) string {
	if !strings.Contains(contentType, "json") {
		return ""
	}
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return ""
	}
	errS := j.Str(payload["error"])
	if errS == "" {
		errS = j.Str(payload["message"])
	}
	desc := j.Str(payload["error_description"])
	parts := make([]string, 0, detailPartsCap)
	if errS != "" {
		parts = append(parts, errS)
	}
	if desc != "" {
		parts = append(parts, desc)
	}

	return strings.Join(parts, " - ")
}

func jwtMapClaims(tok *jwt.Token) (jwt.MapClaims, error) {
	claims, ok := tok.Claims.(jwt.MapClaims)
	if !ok {
		return nil, Err("SSO token claims were not a JSON object")
	}

	return claims, nil
}

func b64url(raw []byte) string {
	return strings.TrimRight(base64.URLEncoding.EncodeToString(raw), "=")
}

func random(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)

	return b
}
