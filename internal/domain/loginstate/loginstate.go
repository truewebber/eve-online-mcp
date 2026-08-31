package loginstate

import (
	"context"
	"errors"
	"time"
)

const TTL = 15 * time.Minute

var ErrNotFound = errors.New("loginstate: not found")

type Kind string

const (
	KindMCP Kind = "mcp"
	KindAlt Kind = "alt"
)

type Login struct {
	State         string
	PKCEVerifier  string
	Scopes        []string
	Kind          Kind
	UserID        string
	MCPClientID   string
	RedirectURI   string
	MCPState      string
	CodeChallenge string
	CreatedAt     time.Time
}

type Repository interface {
	Put(ctx context.Context, st Login) error
	Get(ctx context.Context, state string) (*Login, error)
	Take(ctx context.Context, state string) (*Login, error)
	DeleteExpired(ctx context.Context) (int64, error)
}
