package oauthclient

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("oauthclient: not found")

type Client struct {
	ID           string
	Name         string
	RedirectURIs []string
	CreatedAt    time.Time
}

//go:generate go tool go.uber.org/mock/mockgen -destination=../../mocks/oauthclient.go -package=mocks -mock_names=Repository=MockOauthclientRepository github.com/truewebber/eve-online-mcp/internal/domain/oauthclient Repository
type Repository interface {
	Upsert(ctx context.Context, c Client) error
	Get(ctx context.Context, id string) (*Client, error)
}
