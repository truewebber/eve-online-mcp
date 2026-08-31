package session

import (
	"context"

	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
)

type ssoTokens struct{ sso sso.Client }

func (t ssoTokens) AccessToken(ctx context.Context, characterID int) (string, error) {
	tok, err := t.sso.AccessToken(ctx, characterID)
	if err != nil {
		return "", wrap("AccessToken", err)
	}

	return tok.AccessToken, nil
}
