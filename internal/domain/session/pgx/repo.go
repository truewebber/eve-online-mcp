package pgx

import (
	"context"
	"errors"
	"fmt"

	"github.com/truewebber/gopkg/log"

	"github.com/truewebber/eve-online-mcp/internal/domain/session"
	"github.com/truewebber/eve-online-mcp/internal/postgres"

	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repo struct {
	pool *pgxpool.Pool
}

const (
	createSQL = `
		INSERT INTO sessions (
			character_id, refresh_token, scopes, mcp_client_id, client_name, ip, valid_til
		) VALUES ($1, $2, $3, $4, $5, $6, now() + interval '30 days')
		RETURNING id, character_id, refresh_token, scopes, mcp_client_id, client_name,
			ip, created_at, valid_til, revoked_at`
	revokeAllSQL = `
		WITH doomed AS (
			SELECT id, refresh_token
			FROM sessions
			WHERE character_id = $1 AND revoked_at IS NULL
			FOR UPDATE
		)
		UPDATE sessions s
		SET revoked_at = now(), refresh_token = NULL
		FROM doomed
		WHERE s.id = doomed.id
		RETURNING doomed.id, doomed.refresh_token`
	revokeOneSQL = `
		WITH doomed AS (
			SELECT id, refresh_token
			FROM sessions
			WHERE id = $1 AND revoked_at IS NULL
			FOR UPDATE
		)
		UPDATE sessions s
		SET revoked_at = now(), refresh_token = NULL
		FROM doomed
		WHERE s.id = doomed.id
		RETURNING doomed.id, doomed.refresh_token`
	liveByIDSQL = `
		SELECT id, character_id, refresh_token, scopes, mcp_client_id, client_name,
			ip, created_at, valid_til, revoked_at
		FROM sessions
		WHERE id = $1 AND revoked_at IS NULL AND now() < valid_til`
	lockRefreshSQL = `
		SELECT refresh_token
		FROM sessions
		WHERE id = $1 AND revoked_at IS NULL AND now() < valid_til
		FOR UPDATE`
	rereadRefreshSQL = `
		SELECT refresh_token
		FROM sessions
		WHERE id = $1`
	writeRefreshSQL  = `UPDATE sessions SET refresh_token = $2 WHERE id = $1`
	lockCharacterSQL = `SELECT pg_advisory_xact_lock($1)`
)

func New(pool *pgxpool.Pool, _ log.Logger) *Repo {
	return &Repo{pool: pool}
}

func (r *Repo) Create(ctx context.Context, s session.Session) (*session.Session, error) {
	if s.Scopes == nil {
		s.Scopes = []string{}
	}
	row, err := scan(postgres.Q(ctx, r.pool).QueryRow(ctx, createSQL,
		s.CharacterID, nullIfEmpty(s.RefreshToken), s.Scopes,
		s.MCPClientID, s.ClientName, s.IP,
	))
	if err != nil {
		return nil, wrap("Create", err)
	}

	return row, nil
}

func (r *Repo) RevokeAllForCharacter(ctx context.Context, characterID int64) (session.Revoked, error) {
	return r.revoke(ctx, "RevokeAllForCharacter", revokeAllSQL, characterID)
}

func (r *Repo) Revoke(ctx context.Context, id int64) (session.Revoked, error) {
	return r.revoke(ctx, "Revoke", revokeOneSQL, id)
}

func (r *Repo) LiveByID(ctx context.Context, id int64) (*session.Session, error) {
	row, err := scan(postgres.Q(ctx, r.pool).QueryRow(ctx, liveByIDSQL, id))
	if errors.Is(err, jackpgx.ErrNoRows) {
		return nil, session.ErrNotFound
	}
	if err != nil {
		return nil, wrap("LiveByID", err)
	}

	return row, nil
}

func (r *Repo) LockForRefresh(ctx context.Context, id int64, fn func(string) (string, error)) error {
	run := func(ctx context.Context) error {
		q := postgres.Q(ctx, r.pool)
		var refresh *string
		err := q.QueryRow(ctx, lockRefreshSQL, id).Scan(&refresh)
		if errors.Is(err, jackpgx.ErrNoRows) {
			return session.ErrNotFound
		}
		if err != nil {
			return wrap("LockForRefresh", err)
		}
		if err := q.QueryRow(ctx, rereadRefreshSQL, id).Scan(&refresh); err != nil {
			return wrap("LockForRefresh", err)
		}
		next, err := fn(deref(refresh))
		if err != nil {
			return err
		}
		if _, err := q.Exec(ctx, writeRefreshSQL, id, nullIfEmpty(next)); err != nil {
			return wrap("LockForRefresh", err)
		}

		return nil
	}
	if postgres.Tx(ctx) != nil {
		return run(ctx)
	}

	return wrap("LockForRefresh", postgres.WithinTx(ctx, r.pool, run))
}

func (r *Repo) LockCharacter(ctx context.Context, characterID int64) error {
	if postgres.Tx(ctx) == nil {
		return session.ErrNeedTx
	}
	_, err := postgres.Q(ctx, r.pool).Exec(ctx, lockCharacterSQL, characterID)

	return wrap("LockCharacter", err)
}

func (r *Repo) revoke(ctx context.Context, op, query string, arg int64) (session.Revoked, error) {
	rows, err := postgres.Q(ctx, r.pool).Query(ctx, query, arg)
	if err != nil {
		return session.Revoked{}, wrap(op, err)
	}
	defer rows.Close()
	out := session.Revoked{}
	for rows.Next() {
		var id int64
		var token *string
		if err := rows.Scan(&id, &token); err != nil {
			return session.Revoked{}, wrap(op, err)
		}
		out.IDs = append(out.IDs, id)
		if token != nil && *token != "" {
			out.Tokens = append(out.Tokens, *token)
		}
	}
	if err := rows.Err(); err != nil {
		return session.Revoked{}, wrap(op, err)
	}

	return out, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scan(row scanner) (*session.Session, error) {
	var (
		s       session.Session
		refresh *string
	)
	err := row.Scan(
		&s.ID, &s.CharacterID, &refresh, &s.Scopes, &s.MCPClientID, &s.ClientName,
		&s.IP, &s.CreatedAt, &s.ValidTil, &s.RevokedAt,
	)
	if err != nil {
		return nil, wrap("scan", err)
	}
	s.RefreshToken = deref(refresh)
	if s.Scopes == nil {
		s.Scopes = []string{}
	}

	return &s, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}

	return s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}

func wrap(op string, err error) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("session: %s: %w", op, err)
}
