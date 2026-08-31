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
	MCPClientID   string
	RedirectURI   string
	CodeChallenge string
	ExpiresAt     time.Time
}

//go:generate go tool go.uber.org/mock/mockgen -destination=../../mocks/authcode.go -package=mocks -mock_names=Repository=MockAuthcodeRepository github.com/truewebber/eve-online-mcp/internal/domain/authcode Repository
type Repository interface {
	Put(ctx context.Context, c Code) error
	Take(ctx context.Context, value string) (*Code, error)
	DeleteExpired(ctx context.Context) (int64, error)
}
