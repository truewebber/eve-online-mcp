package session

import (
	"context"
	"errors"
	"time"
)

const Lifetime = 30 * 24 * time.Hour

var (
	ErrNotFound = errors.New("session: not found")
	ErrNeedTx   = errors.New("session: advisory lock requires a transaction")
)

type Session struct {
	ID           int64
	CharacterID  int64
	RefreshToken string
	Scopes       []string
	MCPClientID  string
	ClientName   string
	IP           string
	CreatedAt    time.Time
	ValidTil     time.Time
	RevokedAt    *time.Time
}

// Tokens cleared from unrevoked rows, to revoke at CCP after the commit.
type Revoked struct {
	IDs    []int64
	Tokens []string
}

//go:generate go tool go.uber.org/mock/mockgen -destination=../../mocks/session.go -package=mocks -mock_names=Repository=MockSessionRepository github.com/truewebber/eve-online-mcp/internal/domain/session Repository
type Repository interface {
	Create(ctx context.Context, s Session) (*Session, error)
	RevokeAllForCharacter(ctx context.Context, characterID int64) (Revoked, error)
	Revoke(ctx context.Context, id int64) (Revoked, error)
	ExpireValidTil(ctx context.Context) (Revoked, error)
	PurgeRevoked(ctx context.Context) (int64, error)
	LiveByID(ctx context.Context, id int64) (*Session, error)
	LockForRefresh(ctx context.Context, id int64, fn func(string) (string, error)) error
	LockCharacter(ctx context.Context, characterID int64) error
}
