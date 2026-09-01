package pgx

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/domain/confirm"
	"github.com/truewebber/eve-online-mcp/internal/postgres"

	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	putSQL = `
		INSERT INTO confirm_tokens (token, session_id, tool, args_digest, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (token) DO UPDATE SET
			session_id = EXCLUDED.session_id,
			tool = EXCLUDED.tool,
			args_digest = EXCLUDED.args_digest,
			created_at = EXCLUDED.created_at,
			expires_at = EXCLUDED.expires_at`
	getSQL = `
		SELECT token, session_id, tool, args_digest, created_at, expires_at
		FROM confirm_tokens WHERE token = $1`
	takeSQL = `
		DELETE FROM confirm_tokens WHERE token = $1
		RETURNING token, session_id, tool, args_digest, created_at, expires_at`
	deleteSQL        = `DELETE FROM confirm_tokens WHERE token = $1`
	countSQL         = `SELECT COUNT(*) FROM confirm_tokens WHERE session_id = $1 AND expires_at > now()`
	deleteExpiredSQL = `DELETE FROM confirm_tokens WHERE expires_at < now()`
)

type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo {
	if pool == nil {
		panic("confirm/pgx: pool is required")
	}

	return &Repo{pool: pool}
}

func (r *Repo) Put(ctx context.Context, c confirm.Confirm) error {
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	if c.ExpiresAt.IsZero() {
		c.ExpiresAt = c.CreatedAt.Add(confirm.TTL)
	}
	_, err := postgres.Q(ctx, r.pool).Exec(ctx, putSQL,
		c.Value, c.SessionID, c.Tool, c.ArgsDigest, c.CreatedAt, c.ExpiresAt,
	)

	return wrap("Put", err)
}

func (r *Repo) Get(ctx context.Context, value string) (*confirm.Confirm, error) {
	row, err := scan(postgres.Q(ctx, r.pool).QueryRow(ctx, getSQL, value))
	if errors.Is(err, jackpgx.ErrNoRows) {
		return nil, confirm.ErrNotFound
	}

	return row, err
}

func (r *Repo) Take(ctx context.Context, value string) (*confirm.Confirm, error) {
	row, err := scan(postgres.Q(ctx, r.pool).QueryRow(ctx, takeSQL, value))
	if errors.Is(err, jackpgx.ErrNoRows) {
		return nil, confirm.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !row.ExpiresAt.IsZero() && time.Now().After(row.ExpiresAt) {
		return nil, confirm.ErrNotFound
	}

	return row, nil
}

func (r *Repo) Delete(ctx context.Context, value string) error {
	_, err := postgres.Q(ctx, r.pool).Exec(ctx, deleteSQL, value)

	return wrap("Delete", err)
}

func (r *Repo) Count(ctx context.Context, sessionID int64) (int, error) {
	var n int
	err := postgres.Q(ctx, r.pool).QueryRow(ctx, countSQL, sessionID).Scan(&n)

	return n, wrap("Count", err)
}

func (r *Repo) DeleteExpired(ctx context.Context) (int64, error) {
	tag, err := postgres.Q(ctx, r.pool).Exec(ctx, deleteExpiredSQL)
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
	err := row.Scan(&t.Value, &t.SessionID, &t.Tool, &t.ArgsDigest, &t.CreatedAt, &t.ExpiresAt)
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
