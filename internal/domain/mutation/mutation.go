package mutation

import (
	"context"
	"time"
)

// Mail is one mail_log row. The mutations table replaces it in T19.
type Mail struct {
	CharacterID int64
	SentAt      time.Time
}

//go:generate go tool go.uber.org/mock/mockgen -destination=../../mocks/mutation.go -package=mocks -mock_names=Repository=MockMutationRepository github.com/truewebber/eve-online-mcp/internal/domain/mutation Repository
type Repository interface {
	CountSince(ctx context.Context, characterID int64, since time.Time) (int, error)
	Insert(ctx context.Context, mail Mail) error
}
