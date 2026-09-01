package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type txKey struct{}

type Querier interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func WithTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, txKey{}, tx)
}

func Tx(ctx context.Context) pgx.Tx {
	tx, ok := ctx.Value(txKey{}).(pgx.Tx)
	if !ok {
		return nil
	}

	return tx
}

func Q(ctx context.Context, pool *pgxpool.Pool) Querier {
	if tx := Tx(ctx); tx != nil {
		return tx
	}

	return pool
}

func WithinTx(ctx context.Context, pool *pgxpool.Pool, fn func(context.Context) error) error {
	if Tx(ctx) != nil {
		return fn(ctx)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin: %w", err)
	}
	if err := fn(WithTx(ctx, tx)); err != nil {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !IsTxClosed(rbErr) {
			return fmt.Errorf("postgres: rollback: %w", errors.Join(err, rbErr))
		}

		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit: %w", err)
	}

	return nil
}

func IsTxClosed(err error) bool {
	return errors.Is(err, pgx.ErrTxClosed)
}
