package oauthclient

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("oauthclient: not found")

type Client struct {
	ID           string
	RedirectURIs []string
	CreatedAt    time.Time
}

type Repository interface {
	Upsert(ctx context.Context, c Client) error
	Get(ctx context.Context, id string) (*Client, error)
}
