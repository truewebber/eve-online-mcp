package session

import (
	"context"
	"errors"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	dbsession "github.com/truewebber/eve-online-mcp/internal/domain/session"
	"github.com/truewebber/eve-online-mcp/internal/domain/write"
)

const refreshMargin = 60 * time.Second

type grantState struct {
	refresh string
	access  string
	expiry  time.Time
}

func (g *grantState) live() string {
	if g == nil || g.access == "" {
		return ""
	}
	if time.Now().After(g.expiry.Add(-refreshMargin)) {
		return ""
	}

	return g.access
}

func (g *grantState) set(tok *sso.CharacterToken) {
	if g == nil || tok == nil {
		return
	}
	g.refresh = tok.RefreshToken
	g.access = tok.AccessToken
	g.expiry = tok.AccessExpiresAt
}

type ParkedGrant struct {
	Code         string
	CharacterID  int64
	RefreshToken string
	Scopes       []string
	MCPClientID  string
	ClientName   string
	IP           string
}

type Exchanged struct {
	Session dbsession.Session
	Revoked dbsession.Revoked
}

type LoggedOut struct {
	CharacterID   int64
	CharacterName string
}

func (s *Session) Redeem(ctx context.Context, parked ParkedGrant) (Exchanged, error) {
	if s.WithinTx == nil {
		return Exchanged{}, wrap("Redeem", dbsession.ErrNeedTx)
	}
	var out Exchanged
	err := s.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.Sessions.LockCharacter(ctx, parked.CharacterID); err != nil {
			return wrap("Redeem", err)
		}
		if _, err := s.Codes.Take(ctx, parked.Code); err != nil {
			return wrap("Redeem", err)
		}
		revoked, err := s.Sessions.RevokeAllForCharacter(ctx, parked.CharacterID)
		if err != nil {
			return wrap("Redeem", err)
		}
		created, err := s.Sessions.Create(ctx, dbsession.Session{
			CharacterID:  parked.CharacterID,
			RefreshToken: parked.RefreshToken,
			Scopes:       parked.Scopes,
			MCPClientID:  parked.MCPClientID,
			ClientName:   parked.ClientName,
			IP:           parked.IP,
		})
		if err != nil {
			return wrap("Redeem", err)
		}
		out = Exchanged{Session: *created, Revoked: revoked}

		return nil
	})
	if err != nil {
		return Exchanged{}, wrap("Redeem", err)
	}

	return out, nil
}

func (s *Session) Logout(ctx context.Context) (LoggedOut, error) {
	if s.WithinTx == nil {
		return LoggedOut{}, wrap("Logout", dbsession.ErrNeedTx)
	}
	row, err := s.Characters.Get(ctx, int64(s.CharacterID))
	if err != nil {
		return LoggedOut{}, wrap("Logout", err)
	}
	var revoked dbsession.Revoked
	err = s.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.Sessions.LockCharacter(ctx, int64(s.CharacterID)); err != nil {
			return wrap("Logout", err)
		}
		revoked, err = s.Sessions.Revoke(ctx, s.SessionID)
		if err != nil {
			return wrap("Logout", err)
		}

		return wrap("Logout", s.Characters.Delete(ctx, int64(s.CharacterID)))
	})
	if err != nil {
		return LoggedOut{}, wrap("Logout", err)
	}
	s.revokeAtCCP(ctx, revoked.Tokens)

	return LoggedOut{CharacterID: row.ID, CharacterName: row.Name}, nil
}

func (s *Session) RevokeAtCCP(ctx context.Context, tokens []string) {
	s.revokeAtCCP(ctx, tokens)
}

func (s *Session) revokeAtCCP(ctx context.Context, tokens []string) {
	for _, tok := range tokens {
		s.SSO.Revoke(ctx, tok)
	}
}

func (s *Session) RebuildGrant(sessionID int64) {
	s.grantMu.Lock()
	defer s.grantMu.Unlock()
	s.SessionID = sessionID
	s.grant = &grantState{}
	if s.Mutations != nil && s.Confirms != nil {
		s.Guard = write.NewGuard(guardPersist{mutations: s.Mutations, confirms: s.Confirms}, int64(s.CharacterID), sessionID, s.Logger)
	}
}

func (s *Session) eveAccess(ctx context.Context) (string, error) {
	s.grantMu.Lock()
	sid := s.SessionID
	if tok := s.grant.live(); tok != "" {
		s.grantMu.Unlock()

		return tok, nil
	}
	expected := s.grant.refresh
	s.grantMu.Unlock()
	if expected == "" {
		if row, err := s.Sessions.LiveByID(ctx, sid); err == nil {
			expected = row.RefreshToken
		}
	}

	var access string
	err := s.Sessions.LockForRefresh(ctx, sid, func(row string) (string, error) {
		s.grantMu.Lock()
		defer s.grantMu.Unlock()
		if expected != "" && row != expected {
			s.grant.refresh = row
			if tok := s.grant.live(); tok != "" {
				access = tok

				return row, nil
			}
		}
		tok, err := s.SSO.AccessToken(ctx, row)
		if err != nil {
			return "", wrap("eveAccess", err)
		}
		s.grant.set(tok)
		access = tok.AccessToken

		return tok.RefreshToken, nil
	})
	if errors.Is(err, sso.ErrInvalidGrant) {
		if _, revErr := s.Sessions.Revoke(ctx, sid); revErr != nil {
			s.Logger.Error("session: revoke after invalid_grant", "sid", sid, "err", revErr)
		}

		return "", wrap("eveAccess", sso.Err("This connection's EVE grant was revoked or expired. Re-authenticate the MCP server (Authentication required) and try again."))
	}
	if err != nil {
		return "", wrap("eveAccess", err)
	}

	return access, nil
}

func (s *Session) revokeIfScopesFallShort(ctx context.Context, granted []string) error {
	if len(write.MissingScopes(granted)) == 0 {
		return nil
	}
	if _, err := s.Sessions.Revoke(ctx, s.SessionID); err != nil {
		s.Logger.Error("session: revoke after scope drift", "sid", s.SessionID, "err", err)
	}

	return wrap("revokeIfScopesFallShort", sso.Err("This connection's EVE grant is missing scopes this server requires. Re-authenticate the MCP server (Authentication required) and try again."))
}
