package store

import (
	"context"
	"fmt"

	"github.com/truewebber/gopkg/log"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool   *pgxpool.Pool
	logger log.Logger
}

func Open(ctx context.Context, databaseURL string, logger log.Logger) (*Store, error) {
	if databaseURL == "" {
		return nil, ErrEmptyDatabaseURL
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("store: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()

		return nil, fmt.Errorf("store: ping: %w", err)
	}

	return &Store{pool: pool, logger: logger}, nil
}

func (s *Store) Pool() *pgxpool.Pool {
	if s == nil {
		return nil
	}

	return s.pool
}

func (s *Store) Close() {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
}
