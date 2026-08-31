package pgx

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/domain/loginstate"

	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	putSQL = `
		INSERT INTO login_states (
			state, pkce_verifier, scopes, kind, user_id,
			mcp_client_id, redirect_uri, mcp_state, code_challenge, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (state) DO UPDATE SET
			pkce_verifier = EXCLUDED.pkce_verifier,
			scopes = EXCLUDED.scopes,
			kind = EXCLUDED.kind,
			user_id = EXCLUDED.user_id,
			mcp_client_id = EXCLUDED.mcp_client_id,
			redirect_uri = EXCLUDED.redirect_uri,
			mcp_state = EXCLUDED.mcp_state,
			code_challenge = EXCLUDED.code_challenge,
			created_at = EXCLUDED.created_at`
	getSQL = `
		SELECT state, pkce_verifier, scopes, kind, user_id,
		       mcp_client_id, redirect_uri, mcp_state, code_challenge, created_at
		FROM login_states WHERE state = $1`
	takeSQL = `
		DELETE FROM login_states WHERE state = $1
		RETURNING state, pkce_verifier, scopes, kind, user_id,
		          mcp_client_id, redirect_uri, mcp_state, code_challenge, created_at`
	deleteSQL        = `DELETE FROM login_states WHERE state = $1`
	deleteExpiredSQL = `DELETE FROM login_states WHERE created_at < now() - interval '15 minutes'`
)

type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

func (r *Repo) Put(ctx context.Context, st loginstate.Login) error {
	if st.Scopes == nil {
		st.Scopes = []string{}
	}
	if st.CreatedAt.IsZero() {
		st.CreatedAt = time.Now().UTC()
	}
	var userID any
	if st.UserID != "" {
		userID = st.UserID
	}
	_, err := r.pool.Exec(ctx, putSQL,
		st.State, st.PKCEVerifier, st.Scopes, string(st.Kind), userID,
		st.MCPClientID, st.RedirectURI, st.MCPState, st.CodeChallenge, st.CreatedAt,
	)

	return wrap("Put", err)
}

func (r *Repo) Get(ctx context.Context, state string) (*loginstate.Login, error) {
	st, err := scan(r.pool.QueryRow(ctx, getSQL, state))
	if errors.Is(err, jackpgx.ErrNoRows) {
		return nil, loginstate.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if time.Since(st.CreatedAt) > loginstate.TTL {
		if _, err := r.pool.Exec(ctx, deleteSQL, state); err != nil {
			return nil, wrap("Get", err)
		}

		return nil, loginstate.ErrNotFound
	}

	return st, nil
}

func (r *Repo) Take(ctx context.Context, state string) (*loginstate.Login, error) {
	st, err := scan(r.pool.QueryRow(ctx, takeSQL, state))
	if errors.Is(err, jackpgx.ErrNoRows) {
		return nil, loginstate.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if time.Since(st.CreatedAt) > loginstate.TTL {
		return nil, loginstate.ErrNotFound
	}

	return st, nil
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

func scan(row scanner) (*loginstate.Login, error) {
	var st loginstate.Login
	var kind string
	var userID *string
	err := row.Scan(
		&st.State, &st.PKCEVerifier, &st.Scopes, &kind, &userID,
		&st.MCPClientID, &st.RedirectURI, &st.MCPState, &st.CodeChallenge, &st.CreatedAt,
	)
	if err != nil {
		return nil, wrap("scan", err)
	}
	st.Kind = loginstate.Kind(kind)
	if userID != nil {
		st.UserID = *userID
	}
	if st.Scopes == nil {
		st.Scopes = []string{}
	}

	return &st, nil
}

func wrap(op string, err error) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("loginstate: %s: %w", op, err)
}
