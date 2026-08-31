package confirm

import (
	"context"
	"errors"
	"time"
)

const TTL = 300 * time.Second

var ErrNotFound = errors.New("confirm: not found")

type Confirm struct {
	Value       string
	CharacterID int64
	Tool        string
	ArgsDigest  string
	CreatedAt   time.Time
}

//go:generate go tool go.uber.org/mock/mockgen -destination=../../mocks/confirm.go -package=mocks -mock_names=Repository=MockConfirmRepository github.com/truewebber/eve-online-mcp/internal/domain/confirm Repository
type Repository interface {
	Put(ctx context.Context, c Confirm) error
	Get(ctx context.Context, value string) (*Confirm, error)
	Take(ctx context.Context, value string) (*Confirm, error)
	Delete(ctx context.Context, value string) error
	Count(ctx context.Context, characterID int64) (int, error)
	DeleteExpired(ctx context.Context) (int64, error)
}
