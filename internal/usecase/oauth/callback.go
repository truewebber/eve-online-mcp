package oauth

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	"github.com/truewebber/eve-online-mcp/internal/domain/authcode"
	"github.com/truewebber/eve-online-mcp/internal/domain/character"
	"github.com/truewebber/eve-online-mcp/internal/domain/loginstate"
)

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

func (s *Server) finishMCP(ctx context.Context, p *loginstate.Login, token *sso.CharacterToken) (string, error) {
	if err := s.runtime.Characters.Upsert(ctx, character.Character{
		ID: int64(token.CharacterID), Name: token.CharacterName, OwnerHash: token.OwnerHash,
	}); err != nil {
		return "", wrap("finishMCP", err)
	}
	scopes := token.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	code := randomID(authCodeBytes)
	if err := s.codes.Put(ctx, authcode.Code{
		Value:         code,
		CharacterID:   int64(token.CharacterID),
		RefreshToken:  token.RefreshToken,
		Scopes:        scopes,
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
