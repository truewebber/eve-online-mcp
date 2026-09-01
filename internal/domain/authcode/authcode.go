package authcode

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("authcode: not found")

type Code struct {
	Value         string
	CharacterID   int64
	RefreshToken  string
	Scopes        []string
	MCPClientID   string
	RedirectURI   string
	CodeChallenge string
	ExpiresAt     time.Time
}

// Tokens of rows the sweep deleted, to revoke at CCP after the commit.
type Swept struct {
	Count  int64
	Tokens []string
}

//go:generate go tool go.uber.org/mock/mockgen -destination=../../mocks/authcode.go -package=mocks -mock_names=Repository=MockAuthcodeRepository github.com/truewebber/eve-online-mcp/internal/domain/authcode Repository
type Repository interface {
	Put(ctx context.Context, c Code) error
	Get(ctx context.Context, value string) (*Code, error)
	Take(ctx context.Context, value string) (*Code, error)
	SweepExpired(ctx context.Context) (Swept, error)
}
