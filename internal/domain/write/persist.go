package write

import (
	"context"
	"time"
)

// Confirm is a one-shot preview token for a mutating tool call.
type Confirm struct {
	Token      string
	UserID     string
	Tool       string
	ArgsDigest string
	CreatedAt  time.Time
}

// Persist is the durable side of the write guard. Implemented by adapter/store
// (via a thin session wrapper) so domain/write does not import the adapter.
type Persist interface {
	PutConfirm(ctx context.Context, c Confirm) error
	GetConfirm(ctx context.Context, token string) (*Confirm, bool, error)
	DeleteConfirm(ctx context.Context, token string) error
	CountConfirm(ctx context.Context, userID string) (int, error)
	CountMailSince(ctx context.Context, userID string, since time.Time) (int, error)
	InsertMail(ctx context.Context, userID string, at time.Time) error
}
