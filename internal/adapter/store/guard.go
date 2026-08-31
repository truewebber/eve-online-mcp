package store

import (
	"context"
	"time"
)

const (
	countMailSQL  = `SELECT COUNT(*) FROM mail_log WHERE character_id = $1 AND sent_at >= $2`
	insertMailSQL = `INSERT INTO mail_log (character_id, sent_at) VALUES ($1, $2)`
)

func (s *Store) CountMailSince(ctx context.Context, characterID int64, since time.Time) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, countMailSQL, characterID, since).Scan(&n)

	return n, wrap("CountMailSince", err)
}

func (s *Store) InsertMail(ctx context.Context, characterID int64, at time.Time) error {
	_, err := s.pool.Exec(ctx, insertMailSQL, characterID, at)

	return wrap("InsertMail", err)
}
