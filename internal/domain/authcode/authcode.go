package authcode

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("authcode: not found")

type Code struct {
	Value         string
	UserID        string
	MCPClientID   string
	RedirectURI   string
	CodeChallenge string
	ExpiresAt     time.Time
}

type Repository interface {
	Put(ctx context.Context, c Code) error
	Take(ctx context.Context, value string) (*Code, error)
	DeleteExpired(ctx context.Context) (int64, error)
}
