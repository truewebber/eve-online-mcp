package postgres

import (
	"context"
	"fmt"

	"github.com/truewebber/gopkg/log"

	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, databaseURL string, logger log.Logger) (*DB, error) {
	if databaseURL == "" {
		return nil, ErrEmptyDatabaseURL
	}
	if logger == nil {
		return nil, errLoggerRequired
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("postgres: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		logger.Error("postgres: ping", "err", err)

		return nil, fmt.Errorf("postgres: ping: %w", err)
	}

	return &DB{pool: pool}, nil
}

func (db *DB) Pool() *pgxpool.Pool {
	if db == nil {
		return nil
	}

	return db.pool
}

func (db *DB) Ping(ctx context.Context) error {
	if db == nil || db.pool == nil {
		return ErrEmptyDatabaseURL
	}

	if err := db.pool.Ping(ctx); err != nil {
		return fmt.Errorf("postgres: ping: %w", err)
	}

	return nil
}

func (db *DB) Close() {
	if db != nil && db.pool != nil {
		db.pool.Close()
	}
}
