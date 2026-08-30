package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Store) CacheGet(ctx context.Context, key string) (*CachedResponse, bool, error) {
	var c CachedResponse
	var pages *int
	err := s.pool.QueryRow(ctx, `
		SELECT etag, expires_at, stored_at, pages, body FROM http_cache WHERE key = $1`, key,
	).Scan(&c.ETag, &c.ExpiresAt, &c.StoredAt, &pages, &c.Body)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	c.Pages = pages

	return &c, true, nil
}

func (s *Store) CachePut(ctx context.Context, key string, c CachedResponse) error {
	if c.StoredAt.IsZero() {
		c.StoredAt = time.Now().UTC()
	}
	if c.Body == nil {
		c.Body = json.RawMessage("null")
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO http_cache (key, etag, expires_at, stored_at, pages, body)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (key) DO UPDATE SET
			etag = EXCLUDED.etag,
			expires_at = EXCLUDED.expires_at,
			stored_at = EXCLUDED.stored_at,
			pages = EXCLUDED.pages,
			body = EXCLUDED.body`,
		key, c.ETag, c.ExpiresAt, c.StoredAt, c.Pages, c.Body)

	return err
}

func (s *Store) CacheTouch(ctx context.Context, key string, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE http_cache SET expires_at = $2, stored_at = $3 WHERE key = $1`,
		key, expiresAt, time.Now().UTC())

	return err
}

func (s *Store) CachePurgeExpired(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM http_cache WHERE expires_at < now()`)
	if err != nil {
		return 0, err
	}

	return tag.RowsAffected(), nil
}

func (s *Store) NameGet(ctx context.Context, ids []int64) (map[int64]NameRow, error) {
	out := map[int64]NameRow{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT id, name, category FROM names WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var n NameRow
		err := rows.Scan(&n.ID, &n.Name, &n.Category)
		if err != nil {
			return nil, err
		}
		out[n.ID] = n
	}

	return out, rows.Err()
}

func (s *Store) NamePut(ctx context.Context, rows []NameRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, n := range rows {
		if _, err := tx.Exec(ctx, `
			INSERT INTO names (id, name, category) VALUES ($1, $2, $3)
			ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, category = EXCLUDED.category`,
			n.ID, n.Name, n.Category); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func (s *Store) BlobGet(ctx context.Context, key string, maxAge *time.Duration) (json.RawMessage, error) {
	var stored time.Time
	var value json.RawMessage
	err := s.pool.QueryRow(ctx, `SELECT stored_at, value FROM blobs WHERE key = $1`, key).Scan(&stored, &value)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if maxAge != nil && time.Since(stored) > *maxAge {
		return nil, nil
	}

	return value, nil
}

func (s *Store) BlobPut(ctx context.Context, key string, value json.RawMessage) error {
	if value == nil {
		value = json.RawMessage("null")
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO blobs (key, stored_at, value) VALUES ($1, now(), $2)
		ON CONFLICT (key) DO UPDATE SET stored_at = EXCLUDED.stored_at, value = EXCLUDED.value`,
		key, value)

	return err
}

// PurgeExpired deletes handshake rows, confirm tokens and stale cache
// past their TTL. Safe to call on a ticker or on access.
func (s *Store) PurgeExpired(ctx context.Context) (int64, error) {
	var n int64
	for _, q := range []string{
		`DELETE FROM login_states WHERE created_at < now() - interval '15 minutes'`,
		`DELETE FROM auth_codes WHERE expires_at < now()`,
		`DELETE FROM confirm_tokens WHERE created_at < now() - interval '300 seconds'`,
		`DELETE FROM http_cache WHERE expires_at < now()`,
	} {
		tag, err := s.pool.Exec(ctx, q)
		if err != nil {
			return n, err
		}
		n += tag.RowsAffected()
	}

	return n, nil
}
