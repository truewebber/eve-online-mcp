package oauth

import (
	"context"
	"net/http"
	"strconv"

	dbsession "github.com/truewebber/eve-online-mcp/internal/domain/session"
	"github.com/truewebber/eve-online-mcp/internal/domain/write"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"

	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
)

func (s *Server) VerifyAccess(ctx context.Context, token string, _ *http.Request) (*mcpauth.TokenInfo, error) {
	info, ref, err := s.verifyAccess(token)
	if err != nil {
		return nil, err
	}
	if err := s.requireLive(ctx, ref); err != nil {
		return nil, mcpauth.ErrInvalidToken
	}

	return info, nil
}

func (s *Server) SessionFor(characterID int, sessionID int64) *session.Session {
	key := strconv.Itoa(characterID)
	if v, ok := s.sessions.Load(key); ok {
		if sess, ok := v.(*session.Session); ok {
			if sess.SessionID != sessionID {
				sess.RebuildGrant(sessionID)
			}

			return sess
		}
	}
	sess := s.runtime.ForCharacter(characterID, sessionID)
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
		var sid int64
		ok := false
		if ti.Extra != nil {
			sid, ok = claimInt64(ti.Extra[claimSID])
		}
		if !ok || sid == 0 {
			http.Error(w, "unknown character", http.StatusUnauthorized)

			return
		}
		if err := s.requireLive(r.Context(), issued{CharacterID: int(id), SessionID: sid}); err != nil {
			http.Error(w, "unknown character", http.StatusUnauthorized)

			return
		}
		next.ServeHTTP(w, r.WithContext(session.With(r.Context(), s.SessionFor(int(id), sid))))
	})

	return mcpauth.RequireBearerToken(s.VerifyAccess, &mcpauth.RequireBearerTokenOptions{
		ResourceMetadataURL: s.MetadataURL(),
		Scopes:              []string{scopeEve},
		ClockSkew:           jwtLeeway,
	})(inner)
}

func (s *Server) refreshGrant(w http.ResponseWriter, r *http.Request) {
	raw := r.Form.Get(grantRefresh)
	ref, err := s.parseRefresh(raw)
	if err != nil {
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)

		return
	}
	row, err := s.runtime.Sessions.LiveByID(r.Context(), ref.SessionID)
	if err != nil || row.CharacterID != int64(ref.CharacterID) {
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)

		return
	}
	s.writeTokens(w, ref.CharacterID, ref.SessionID, row.ValidTil)
}

func (s *Server) requireLive(ctx context.Context, ref issued) error {
	row, err := s.runtime.Sessions.LiveByID(ctx, ref.SessionID)
	if err != nil || row.CharacterID != int64(ref.CharacterID) {
		return dbsession.ErrNotFound
	}
	if len(write.MissingScopes(row.Scopes)) == 0 {
		return nil
	}
	if _, err := s.runtime.Sessions.Revoke(ctx, ref.SessionID); err != nil {
		s.logger.Error("oauth: revoke after scope drift", "sid", ref.SessionID, "err", err)
	}

	return dbsession.ErrNotFound
}
