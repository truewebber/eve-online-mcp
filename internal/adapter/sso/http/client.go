package http

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
	"math/big"
	nhttp "net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/truewebber/gopkg/log"

	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	"github.com/truewebber/eve-online-mcp/internal/j"

	"github.com/golang-jwt/jwt/v5"
)

const (
	ssoHost        = "login.eveonline.com"
	pathAuthorize  = "/v2/oauth/authorize"
	pathOAuthToken = "/v2/oauth/token" //nolint:gosec // G101: CCP endpoint path, not a secret
	pathRevoke     = "/v2/oauth/revoke"
	pathJWKS       = "/oauth/jwks"
)

func fail(msg string) error { return sso.Error{Msg: msg} }

func knownIssuer(iss string) bool {
	switch iss {
	case ssoHost, (&url.URL{Scheme: "https", Host: ssoHost}).String():
		return true
	default:
		return false
	}
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

type jwksState struct {
	mu   sync.Mutex
	keys map[string]any
	at   time.Time
}

type Client struct {
	opts         sso.Options
	base         url.URL
	http         *nhttp.Client
	access       *accessCache
	refreshLocks sync.Map
	jwks         *jwksState
	logger       log.Logger
}

func New(opts sso.Options, httpClient *nhttp.Client, logger log.Logger) *Client {
	return &Client{
		opts:   opts,
		base:   url.URL{Scheme: "https", Host: ssoHost},
		http:   httpClient,
		access: newAccessCache(),
		jwks:   &jwksState{},
		logger: logger,
	}
}

func (c *Client) PrepareLogin(scopes []string) (*sso.PreparedLogin, error) {
	if c.opts.ClientID == "" {
		return nil, fail("EVE CLIENT_ID is not configured on this server.")
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

	return &sso.PreparedLogin{
		URL:      c.endpoint(pathAuthorize, q),
		State:    state,
		Verifier: verifier,
		Scopes:   scopes,
	}, nil
}

// ExchangeCode takes the PKCE verifier from the persisted login_states row,
// not process memory — any replica can finish the handshake.
func (c *Client) ExchangeCode(ctx context.Context, code, verifier string) (*sso.CharacterToken, error) {
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

func (c *Client) AccessToken(ctx context.Context, refreshToken string) (*sso.CharacterToken, error) {
	if refreshToken == "" {
		return nil, fail("Missing EVE refresh token. Re-authenticate the MCP server (Authentication required) and try again.")
	}
	if mem, ok := c.access.live(refreshToken, refreshMargin); ok {
		return cachedToken(refreshToken, mem), nil
	}
	lockI, _ := c.refreshLocks.LoadOrStore(refreshToken, &sync.Mutex{})
	lock, ok := lockI.(*sync.Mutex)
	if !ok {
		return nil, fail("refresh lock has an unexpected type")
	}
	lock.Lock()
	defer lock.Unlock()
	if mem, ok := c.access.live(refreshToken, refreshMargin); ok {
		return cachedToken(refreshToken, mem), nil
	}

	return c.refresh(ctx, refreshToken)
}

func (c *Client) Revoke(ctx context.Context, refreshToken string) {
	if refreshToken == "" {
		return
	}
	data := url.Values{
		"token_type_hint": {formRefreshToken},
		"token":           {refreshToken},
		formClientID:      {c.opts.ClientID},
	}
	req, err := nhttp.NewRequestWithContext(ctx, nhttp.MethodPost, c.endpoint(pathRevoke, nil), strings.NewReader(data.Encode()))
	if err != nil {
		c.logger.Error("sso: revoke request", "err", err)
		c.access.drop(refreshToken)

		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", c.opts.UserAgent)
	if c.opts.ClientSecret != "" {
		req.SetBasicAuth(c.opts.ClientID, c.opts.ClientSecret)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.logger.Error("sso: revoke call", "err", err)
	} else {
		resp.Body.Close()
	}
	c.access.drop(refreshToken)
}

func (c *Client) endpoint(path string, q url.Values) string {
	u := c.base
	u.Path = path
	if q != nil {
		u.RawQuery = q.Encode()
	}

	return u.String()
}

func (c *Client) refresh(ctx context.Context, refreshToken string) (*sso.CharacterToken, error) {
	refreshed, err := c.exchangeRefresh(ctx, refreshToken, nil)
	if err != nil {
		if errors.Is(err, sso.ErrInvalidGrant) {
			c.access.drop(refreshToken)

			return nil, err
		}

		return nil, err
	}
	c.access.put(refreshed.RefreshToken, accessMem{
		AccessToken:     refreshed.AccessToken,
		AccessExpiresAt: refreshed.AccessExpiresAt,
	})
	if refreshed.RefreshToken != refreshToken {
		c.access.drop(refreshToken)
	}

	return refreshed, nil
}

func cachedToken(refreshToken string, mem accessMem) *sso.CharacterToken {
	return &sso.CharacterToken{
		RefreshToken:    refreshToken,
		AccessToken:     mem.AccessToken,
		AccessExpiresAt: mem.AccessExpiresAt,
	}
}

func (c *Client) exchangeRefresh(ctx context.Context, refreshToken string, fallback *sso.CharacterToken) (*sso.CharacterToken, error) {
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
	req, err := nhttp.NewRequestWithContext(ctx, nhttp.MethodPost, c.endpoint(pathOAuthToken, nil), strings.NewReader(data.Encode()))
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
		return nil, fail("SSO token request failed: " + err.Error())
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenBody))
	if err != nil {
		return nil, fail("SSO token request failed: " + err.Error())
	}
	if resp.StatusCode >= nhttp.StatusBadRequest {
		detail := ssoDetail(resp.Header.Get("Content-Type"), body)
		if strings.Contains(detail, "invalid_grant") {
			return nil, fmt.Errorf("%w: %s", sso.ErrInvalidGrant, detail)
		}

		return nil, fail(fmt.Sprintf("SSO token request failed (%d): %s", resp.StatusCode, detail))
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fail("SSO returned a malformed token payload")
	}

	return payload, nil
}

func (c *Client) tokenFromPayload(ctx context.Context, payload map[string]any, fallback *sso.CharacterToken) (*sso.CharacterToken, error) {
	access := j.Str(payload["access_token"])
	refresh := j.Str(payload[formRefreshToken])
	if refresh == "" && fallback != nil {
		refresh = fallback.RefreshToken
	}
	if access == "" || refresh == "" {
		return nil, fail("SSO response was missing access_token or refresh_token.")
	}
	claims, err := c.decode(ctx, access)
	if err != nil {
		return nil, err
	}
	subject := j.Str(claims["sub"])
	if !strings.HasPrefix(subject, "CHARACTER:EVE:") {
		return nil, fail(fmt.Sprintf("Unexpected token subject: %q", subject))
	}
	var characterID int
	if _, err := fmt.Sscanf(strings.TrimPrefix(subject, "CHARACTER:EVE:"), "%d", &characterID); err != nil || characterID == 0 {
		return nil, fail(fmt.Sprintf("Unexpected token subject: %q", subject))
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

	return &sso.CharacterToken{
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
	parser := jwt.NewParser(jwt.WithValidMethods([]string{"RS256", "ES256"}), jwt.WithAudience(sso.TokenAudience), jwt.WithLeeway(jwtLeeway))
	key, err := c.signingKey(ctx, accessToken)
	if err != nil {
		c.logger.Error("sso: JWKS unavailable; accepting token on TLS alone", "err", err)
		tok, _, err := jwt.NewParser(jwt.WithoutClaimsValidation()).ParseUnverified(accessToken, jwt.MapClaims{})
		if err != nil {
			return nil, fail("Malformed token from the SSO: " + err.Error())
		}
		claims, err := jwtMapClaims(tok)
		if err != nil {
			return nil, err
		}

		return c.checkIssuer(claims)
	}
	tok, err := parser.Parse(accessToken, func(_ *jwt.Token) (any, error) { return key, nil })
	if err != nil {
		return nil, fail("The SSO token failed verification (" + err.Error() + "). Nothing was stored.")
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

	return nil, fail(fmt.Sprintf("Unexpected token issuer: %q", iss))
}

func (c *Client) signingKey(ctx context.Context, accessToken string) (any, error) {
	tok, _, err := jwt.NewParser().ParseUnverified(accessToken, jwt.MapClaims{})
	if err != nil {
		return nil, wrap("signingKey", err)
	}
	kid := j.Str(tok.Header["kid"])
	c.jwks.mu.Lock()
	defer c.jwks.mu.Unlock()
	if c.jwks.keys == nil || time.Since(c.jwks.at) > time.Hour {
		keys, err := c.fetchJWKS(ctx)
		if err != nil {
			return nil, err
		}
		c.jwks.keys = keys
		c.jwks.at = time.Now()
	}
	key, ok := c.jwks.keys[kid]
	if !ok {
		return nil, fmt.Errorf("%w: %s", errKidNotInJWKS, kid)
	}

	return key, nil
}

func (c *Client) fetchJWKS(ctx context.Context) (map[string]any, error) {
	req, err := nhttp.NewRequestWithContext(ctx, nhttp.MethodGet, c.endpoint(pathJWKS, nil), nil)
	if err != nil {
		return nil, wrap("fetchJWKS", err)
	}
	resp, err := c.http.Do(req)
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
		return nil, fail("SSO token claims were not a JSON object")
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
