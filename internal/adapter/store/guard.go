package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Store) PutConfirmToken(ctx context.Context, t ConfirmToken) error {
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO confirm_tokens (token, user_id, tool, args_digest, created_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (token) DO UPDATE SET
			user_id = EXCLUDED.user_id,
			tool = EXCLUDED.tool,
			args_digest = EXCLUDED.args_digest,
			created_at = EXCLUDED.created_at`,
		t.Token, t.UserID, t.Tool, t.ArgsDigest, t.CreatedAt)
	return err
}

func (s *Store) TakeConfirmToken(ctx context.Context, token string) (*ConfirmToken, bool, error) {
	var t ConfirmToken
	err := s.pool.QueryRow(ctx, `
		DELETE FROM confirm_tokens WHERE token = $1
		RETURNING token, user_id, tool, args_digest, created_at`, token,
	).Scan(&t.Token, &t.UserID, &t.Tool, &t.ArgsDigest, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if time.Since(t.CreatedAt) > ConfirmTokenTTL {
		return nil, false, nil
	}
	return &t, true, nil
}

func (s *Store) CountMailSince(ctx context.Context, userID string, since time.Time) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM mail_log WHERE user_id = $1 AND sent_at >= $2`, userID, since,
	).Scan(&n)
	return n, err
}

func (s *Store) InsertMail(ctx context.Context, userID string, at time.Time) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO mail_log (user_id, sent_at) VALUES ($1, $2)`, userID, at)
	return err
}
