package pgx

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/domain/authcode"
	"github.com/truewebber/eve-online-mcp/internal/postgres"

	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	putSQL = `
		INSERT INTO auth_codes (
			code, character_id, refresh_token, scopes, mcp_client_id, redirect_uri, code_challenge, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	selectSQL = `
		SELECT code, character_id, refresh_token, scopes, mcp_client_id, redirect_uri, code_challenge, expires_at
		FROM auth_codes WHERE code = $1`
	takeSQL = `
		DELETE FROM auth_codes WHERE code = $1
		RETURNING code, character_id, refresh_token, scopes, mcp_client_id, redirect_uri, code_challenge, expires_at`
	revokeCeiling   = 100
	sweepExpiredSQL = `
		WITH doomed AS (
			SELECT code, refresh_token
			FROM auth_codes
			WHERE expires_at < now()
			ORDER BY expires_at
			LIMIT $1
			FOR UPDATE
		)
		DELETE FROM auth_codes a
		USING doomed
		WHERE a.code = doomed.code
		RETURNING doomed.refresh_token`
)

type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo {
	if pool == nil {
		panic("authcode/pgx: pool is required")
	}

	return &Repo{pool: pool}
}

func (r *Repo) Put(ctx context.Context, c authcode.Code) error {
	if c.Scopes == nil {
		c.Scopes = []string{}
	}
	_, err := postgres.Q(ctx, r.pool).Exec(ctx, putSQL,
		c.Value, c.CharacterID, c.RefreshToken, c.Scopes,
		c.MCPClientID, c.RedirectURI, c.CodeChallenge, c.ExpiresAt,
	)

	return wrap("Put", err)
}

func (r *Repo) Get(ctx context.Context, value string) (*authcode.Code, error) {
	row, err := scan(postgres.Q(ctx, r.pool).QueryRow(ctx, selectSQL, value))
	if errors.Is(err, jackpgx.ErrNoRows) {
		return nil, authcode.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if time.Now().After(row.ExpiresAt) {
		return nil, authcode.ErrNotFound
	}

	return row, nil
}

func (r *Repo) Take(ctx context.Context, value string) (*authcode.Code, error) {
	row, err := scan(postgres.Q(ctx, r.pool).QueryRow(ctx, takeSQL, value))
	if errors.Is(err, jackpgx.ErrNoRows) {
		return nil, authcode.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if time.Now().After(row.ExpiresAt) {
		return nil, authcode.ErrNotFound
	}

	return row, nil
}

func (r *Repo) SweepExpired(ctx context.Context) (authcode.Swept, error) {
	rows, err := postgres.Q(ctx, r.pool).Query(ctx, sweepExpiredSQL, revokeCeiling)
	if err != nil {
		return authcode.Swept{}, wrap("SweepExpired", err)
	}
	defer rows.Close()
	out := authcode.Swept{}
	for rows.Next() {
		var token string
		if err := rows.Scan(&token); err != nil {
			return authcode.Swept{}, wrap("SweepExpired", err)
		}
		out.Count++
		if token != "" {
			out.Tokens = append(out.Tokens, token)
		}
	}
	if err := rows.Err(); err != nil {
		return authcode.Swept{}, wrap("SweepExpired", err)
	}

	return out, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scan(row scanner) (*authcode.Code, error) {
	var c authcode.Code
	err := row.Scan(
		&c.Value, &c.CharacterID, &c.RefreshToken, &c.Scopes,
		&c.MCPClientID, &c.RedirectURI, &c.CodeChallenge, &c.ExpiresAt,
	)
	if err != nil {
		return nil, wrap("scan", err)
	}
	if c.Scopes == nil {
		c.Scopes = []string{}
	}

	return &c, nil
}

func wrap(op string, err error) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("authcode: %s: %w", op, err)
}
