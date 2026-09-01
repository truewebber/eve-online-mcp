package session

import (
	"context"
)

type ssoTokens struct{ s *Session }

func (t ssoTokens) AccessToken(ctx context.Context, _ int) (string, error) {
	return t.s.eveAccess(ctx)
}
