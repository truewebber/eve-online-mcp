package write

import (
	"context"
	"time"
)

// One-shot: consume on the mutating call, not on preview.
type Confirm struct {
	Token      string
	UserID     string
	Tool       string
	ArgsDigest string
	CreatedAt  time.Time
}

// Implemented outside this package so domain/write does not import the adapter.
//
//go:generate go tool go.uber.org/mock/mockgen -destination=../../mocks/write.go -package=mocks -mock_names=Persist=MockWritePersist github.com/truewebber/eve-online-mcp/internal/domain/write Persist
type Persist interface {
	PutConfirm(ctx context.Context, c Confirm) error
	GetConfirm(ctx context.Context, token string) (*Confirm, bool, error)
	DeleteConfirm(ctx context.Context, token string) error
	CountConfirm(ctx context.Context, userID string) (int, error)
	CountMailSince(ctx context.Context, userID string, since time.Time) (int, error)
	InsertMail(ctx context.Context, userID string, at time.Time) error
}
