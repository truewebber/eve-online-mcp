package store

import (
	"context"
	"crypto/rand"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Store) PutClient(ctx context.Context, c Client) error {
	if c.RedirectURIs == nil {
		c.RedirectURIs = []string{}
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO oauth_clients (client_id, redirect_uris, created_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (client_id) DO UPDATE SET redirect_uris = EXCLUDED.redirect_uris`,
		c.ID, c.RedirectURIs, c.CreatedAt)
	return err
}

func (s *Store) GetClient(ctx context.Context, clientID string) (*Client, bool, error) {
	var c Client
	err := s.pool.QueryRow(ctx,
		`SELECT client_id, redirect_uris, created_at FROM oauth_clients WHERE client_id = $1`, clientID,
	).Scan(&c.ID, &c.RedirectURIs, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &c, true, nil
}

func (s *Store) PutLoginState(ctx context.Context, st LoginState) error {
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
	_, err := s.pool.Exec(ctx, `
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
			created_at = EXCLUDED.created_at`,
		st.State, st.PKCEVerifier, st.Scopes, string(st.Kind), userID,
		st.MCPClientID, st.RedirectURI, st.MCPState, st.CodeChallenge, st.CreatedAt)
	return err
}

func (s *Store) GetLoginState(ctx context.Context, state string) (*LoginState, bool, error) {
	st, err := scanLogin(s.pool.QueryRow(ctx, `
		SELECT state, pkce_verifier, scopes, kind, user_id,
		       mcp_client_id, redirect_uri, mcp_state, code_challenge, created_at
		FROM login_states WHERE state = $1`, state))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if time.Since(st.CreatedAt) > LoginStateTTL {
		_, _ = s.pool.Exec(ctx, `DELETE FROM login_states WHERE state = $1`, state)
		return nil, false, nil
	}
	return st, true, nil
}

func (s *Store) DeleteLoginState(ctx context.Context, state string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM login_states WHERE state = $1`, state)
	return err
}

// TakeLoginState deletes the row and returns it so a callback is one-shot
// across replicas. Expired rows are discarded.
func (s *Store) TakeLoginState(ctx context.Context, state string) (*LoginState, bool, error) {
	st, err := scanLogin(s.pool.QueryRow(ctx, `
		DELETE FROM login_states WHERE state = $1
		RETURNING state, pkce_verifier, scopes, kind, user_id,
		          mcp_client_id, redirect_uri, mcp_state, code_challenge, created_at`, state))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if time.Since(st.CreatedAt) > LoginStateTTL {
		return nil, false, nil
	}
	return st, true, nil
}

func (s *Store) PutAuthCode(ctx context.Context, c AuthCode) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO auth_codes (code, user_id, mcp_client_id, redirect_uri, code_challenge, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		c.Code, c.UserID, c.MCPClientID, c.RedirectURI, c.CodeChallenge, c.ExpiresAt)
	return err
}

func (s *Store) TakeAuthCode(ctx context.Context, code string) (*AuthCode, bool, error) {
	var c AuthCode
	err := s.pool.QueryRow(ctx, `
		DELETE FROM auth_codes WHERE code = $1
		RETURNING code, user_id, mcp_client_id, redirect_uri, code_challenge, expires_at`, code,
	).Scan(&c.Code, &c.UserID, &c.MCPClientID, &c.RedirectURI, &c.CodeChallenge, &c.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if time.Now().After(c.ExpiresAt) {
		return nil, false, nil
	}
	return &c, true, nil
}

func (s *Store) GetOrCreateSecret(ctx context.Context, name string) ([]byte, error) {
	value := make([]byte, SecretBytes)
	if _, err := rand.Read(value); err != nil {
		return nil, err
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO app_secrets (name, value) VALUES ($1, $2)
		ON CONFLICT (name) DO NOTHING`, name, value)
	if err != nil {
		return nil, err
	}
	var out []byte
	err = s.pool.QueryRow(ctx, `SELECT value FROM app_secrets WHERE name = $1`, name).Scan(&out)
	return out, err
}

func scanLogin(row characterScanner) (*LoginState, error) {
	var st LoginState
	var kind string
	var userID *string
	err := row.Scan(
		&st.State, &st.PKCEVerifier, &st.Scopes, &kind, &userID,
		&st.MCPClientID, &st.RedirectURI, &st.MCPState, &st.CodeChallenge, &st.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	st.Kind = LoginKind(kind)
	if userID != nil {
		st.UserID = *userID
	}
	if st.Scopes == nil {
		st.Scopes = []string{}
	}
	return &st, nil
}
