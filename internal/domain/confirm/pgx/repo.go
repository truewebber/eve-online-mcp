package pgx

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/domain/confirm"

	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	putSQL = `
		INSERT INTO confirm_tokens (token, user_id, tool, args_digest, created_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (token) DO UPDATE SET
			user_id = EXCLUDED.user_id,
			tool = EXCLUDED.tool,
			args_digest = EXCLUDED.args_digest,
			created_at = EXCLUDED.created_at`
	getSQL = `
		SELECT token, user_id, tool, args_digest, created_at
		FROM confirm_tokens WHERE token = $1`
	takeSQL = `
		DELETE FROM confirm_tokens WHERE token = $1
		RETURNING token, user_id, tool, args_digest, created_at`
	deleteSQL        = `DELETE FROM confirm_tokens WHERE token = $1`
	countSQL         = `SELECT COUNT(*) FROM confirm_tokens WHERE user_id = $1 AND created_at > now() - interval '300 seconds'`
	deleteExpiredSQL = `DELETE FROM confirm_tokens WHERE created_at < now() - interval '300 seconds'`
)

type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

func (r *Repo) Put(ctx context.Context, c confirm.Confirm) error {
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	_, err := r.pool.Exec(ctx, putSQL, c.Value, c.UserID, c.Tool, c.ArgsDigest, c.CreatedAt)

	return wrap("Put", err)
}

func (r *Repo) Get(ctx context.Context, value string) (*confirm.Confirm, error) {
	row, err := scan(r.pool.QueryRow(ctx, getSQL, value))
	if errors.Is(err, jackpgx.ErrNoRows) {
		return nil, confirm.ErrNotFound
	}

	return row, err
}

func (r *Repo) Take(ctx context.Context, value string) (*confirm.Confirm, error) {
	row, err := scan(r.pool.QueryRow(ctx, takeSQL, value))
	if errors.Is(err, jackpgx.ErrNoRows) {
		return nil, confirm.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if time.Since(row.CreatedAt) > confirm.TTL {
		return nil, confirm.ErrNotFound
	}

	return row, nil
}

func (r *Repo) Delete(ctx context.Context, value string) error {
	_, err := r.pool.Exec(ctx, deleteSQL, value)

	return wrap("Delete", err)
}

func (r *Repo) Count(ctx context.Context, userID string) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, countSQL, userID).Scan(&n)

	return n, wrap("Count", err)
}

func (r *Repo) DeleteExpired(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx, deleteExpiredSQL)
	if err != nil {
		return 0, wrap("DeleteExpired", err)
	}

	return tag.RowsAffected(), nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scan(row scanner) (*confirm.Confirm, error) {
	var t confirm.Confirm
	err := row.Scan(&t.Value, &t.UserID, &t.Tool, &t.ArgsDigest, &t.CreatedAt)
	if err != nil {
		return nil, wrap("scan", err)
	}

	return &t, nil
}

func wrap(op string, err error) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("confirm: %s: %w", op, err)
}
