package sweep

import (
	"context"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// Distinct from pgtest's session lock and from character-keyed xact locks.
	lockKey    int64 = 0x6576657377656570 // "evesweep"
	tryLockSQL       = `SELECT pg_try_advisory_lock($1)`
	unlockSQL        = `SELECT pg_advisory_unlock($1)`
)

//go:generate go tool go.uber.org/mock/mockgen -destination=../../mocks/sweep.go -package=mocks -mock_names=Lock=MockSweepLock github.com/truewebber/eve-online-mcp/internal/usecase/sweep Lock
type Lock interface {
	Try(ctx context.Context) (bool, error)
	Release(ctx context.Context) error
}

type poolLock struct {
	pool *pgxpool.Pool
	mu   sync.Mutex
	conn *pgxpool.Conn
}

func NewPoolLock(pool *pgxpool.Pool) Lock {
	return &poolLock{pool: pool}
}

func (l *poolLock) Try(ctx context.Context) (bool, error) {
	conn, err := l.pool.Acquire(ctx)
	if err != nil {
		return false, wrap("lock", err)
	}
	var held bool
	if err := conn.QueryRow(ctx, tryLockSQL, lockKey).Scan(&held); err != nil {
		conn.Release()

		return false, wrap("lock", err)
	}
	if !held {
		conn.Release()

		return false, nil
	}
	l.mu.Lock()
	l.conn = conn
	l.mu.Unlock()

	return true, nil
}

func (l *poolLock) Release(ctx context.Context) error {
	l.mu.Lock()
	conn := l.conn
	l.conn = nil
	l.mu.Unlock()
	if conn == nil {
		return nil
	}
	_, err := conn.Exec(context.WithoutCancel(ctx), unlockSQL, lockKey)
	conn.Release()

	return wrap("unlock", err)
}
