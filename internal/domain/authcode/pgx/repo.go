package pgx

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/domain/authcode"

	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	putSQL = `
		INSERT INTO auth_codes (code, user_id, mcp_client_id, redirect_uri, code_challenge, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`
	takeSQL = `
		DELETE FROM auth_codes WHERE code = $1
		RETURNING code, user_id, mcp_client_id, redirect_uri, code_challenge, expires_at`
	deleteExpiredSQL = `DELETE FROM auth_codes WHERE expires_at < now()`
)

type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

func (r *Repo) Put(ctx context.Context, c authcode.Code) error {
	_, err := r.pool.Exec(ctx, putSQL,
		c.Value, c.UserID, c.MCPClientID, c.RedirectURI, c.CodeChallenge, c.ExpiresAt,
	)

	return wrap("Put", err)
}

func (r *Repo) Take(ctx context.Context, value string) (*authcode.Code, error) {
	var c authcode.Code
	err := r.pool.QueryRow(ctx, takeSQL, value).Scan(
		&c.Value, &c.UserID, &c.MCPClientID, &c.RedirectURI, &c.CodeChallenge, &c.ExpiresAt,
	)
	if errors.Is(err, jackpgx.ErrNoRows) {
		return nil, authcode.ErrNotFound
	}
	if err != nil {
		return nil, wrap("Take", err)
	}
	if time.Now().After(c.ExpiresAt) {
		return nil, authcode.ErrNotFound
	}

	return &c, nil
}

func (r *Repo) DeleteExpired(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx, deleteExpiredSQL)
	if err != nil {
		return 0, wrap("DeleteExpired", err)
	}

	return tag.RowsAffected(), nil
}

func wrap(op string, err error) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("authcode: %s: %w", op, err)
}
