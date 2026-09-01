package oauth

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
)

type issued struct {
	CharacterID int
	SessionID   int64
	ValidTil    time.Time
}

func (s *Server) IssueAccess(characterID int, sessionID int64) (string, error) {
	return s.issueAccess(characterID, sessionID)
}

func (s *Server) writeTokens(w http.ResponseWriter, characterID int, sessionID int64, validTil time.Time) {
	access, err := s.issueAccess(characterID, sessionID)
	if err != nil {
		http.Error(w, `{"error":"server_error"}`, http.StatusInternalServerError)

		return
	}
	refresh, err := s.issueRefresh(characterID, sessionID, validTil)
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

func (s *Server) issueAccess(characterID int, sessionID int64) (string, error) {
	now := time.Now()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":    strconv.Itoa(characterID),
		claimSID: sessionID,
		"aud":    s.ResourceURL(),
		"iss":    s.Base(),
		"iat":    now.Unix(),
		"exp":    now.Add(accessTTL).Unix(),
		"scope":  scopeEve,
	})
	signed, err := tok.SignedString(s.hmacKey)

	return signed, wrap("issueAccess", err)
}

func (s *Server) issueRefresh(characterID int, sessionID int64, validTil time.Time) (string, error) {
	now := time.Now()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":    strconv.Itoa(characterID),
		claimSID: sessionID,
		"typ":    typRefresh,
		"iss":    s.Base(),
		"iat":    now.Unix(),
		"exp":    validTil.Unix(),
	})
	signed, err := tok.SignedString(s.hmacKey)

	return signed, wrap("issueRefresh", err)
}

func (s *Server) parseRefresh(raw string) (issued, error) {
	tok, err := jwt.Parse(raw, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, errBadAlg
		}

		return s.hmacKey, nil
	}, jwt.WithIssuer(s.Base()), jwt.WithLeeway(jwtLeeway))
	if err != nil || !tok.Valid {
		return issued{}, errInvalidToken
	}
	claims, ok := tok.Claims.(jwt.MapClaims)
	if !ok {
		return issued{}, errInvalidToken
	}
	if claims["typ"] != typRefresh {
		return issued{}, errNotRefresh
	}

	return claimsToIssued(claims)
}

func (s *Server) verifyAccess(token string) (*mcpauth.TokenInfo, issued, error) {
	tok, err := jwt.Parse(token, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, errBadAlg
		}

		return s.hmacKey, nil
	}, jwt.WithAudience(s.ResourceURL()), jwt.WithIssuer(s.Base()), jwt.WithLeeway(jwtLeeway))
	if err != nil || !tok.Valid {
		return nil, issued{}, mcpauth.ErrInvalidToken
	}
	claims, ok := tok.Claims.(jwt.MapClaims)
	if !ok {
		return nil, issued{}, mcpauth.ErrInvalidToken
	}
	if claims["typ"] == typRefresh {
		return nil, issued{}, mcpauth.ErrInvalidToken
	}
	ref, err := claimsToIssued(claims)
	if err != nil {
		return nil, issued{}, mcpauth.ErrInvalidToken
	}
	exp, ok := claims["exp"].(float64)
	if !ok {
		return nil, issued{}, mcpauth.ErrInvalidToken
	}

	return &mcpauth.TokenInfo{
		Scopes:     []string{scopeEve},
		Expiration: time.Unix(int64(exp), 0),
		UserID:     strconv.Itoa(ref.CharacterID),
		Extra:      map[string]any{claimSID: ref.SessionID},
	}, ref, nil
}

func claimsToIssued(claims jwt.MapClaims) (issued, error) {
	sub, ok := claims["sub"].(string)
	if !ok || sub == "" {
		return issued{}, errNoSub
	}
	id, err := strconv.Atoi(sub)
	if err != nil || id == 0 {
		return issued{}, errBadCharacter
	}
	sid, ok := claimInt64(claims[claimSID])
	if !ok || sid == 0 {
		return issued{}, errNoSID
	}

	return issued{CharacterID: id, SessionID: sid}, nil
}

func claimInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	case json.Number:
		i, err := n.Int64()

		return i, err == nil
	case string:
		i, err := strconv.ParseInt(n, 10, 64)

		return i, err == nil
	default:
		return 0, false
	}
}
