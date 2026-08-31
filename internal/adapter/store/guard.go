package store

import (
	"context"
	"time"
)

func (s *Store) CountMailSince(ctx context.Context, userID string, since time.Time) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM mail_log WHERE user_id = $1 AND sent_at >= $2`, userID, since,
	).Scan(&n)

	return n, wrap("CountMailSince", err)
}

func (s *Store) InsertMail(ctx context.Context, userID string, at time.Time) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO mail_log (user_id, sent_at) VALUES ($1, $2)`, userID, at)

	return wrap("InsertMail", err)
}
