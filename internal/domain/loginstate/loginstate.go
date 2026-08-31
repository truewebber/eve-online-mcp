package loginstate

import (
	"context"
	"errors"
	"time"
)

const TTL = 15 * time.Minute

var ErrNotFound = errors.New("loginstate: not found")

type Login struct {
	State         string
	PKCEVerifier  string
	Scopes        []string
	MCPClientID   string
	RedirectURI   string
	MCPState      string
	CodeChallenge string
	CreatedAt     time.Time
}

//go:generate go tool go.uber.org/mock/mockgen -destination=../../mocks/loginstate.go -package=mocks -mock_names=Repository=MockLoginstateRepository github.com/truewebber/eve-online-mcp/internal/domain/loginstate Repository
type Repository interface {
	Put(ctx context.Context, st Login) error
	Get(ctx context.Context, state string) (*Login, error)
	Take(ctx context.Context, state string) (*Login, error)
	DeleteExpired(ctx context.Context) (int64, error)
}
