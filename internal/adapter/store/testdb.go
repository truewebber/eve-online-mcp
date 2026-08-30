package store

import (
	"context"
	"fmt"
	"sync"
)

// testAdvisoryKey serializes integration tests that share one DATABASE_URL
// across packages (`go test ./...` runs packages in parallel).
const testAdvisoryKey int64 = 87265001

// HoldTestLock takes a session-level advisory lock on a dedicated pool
// connection so Truncate/CRUD in another package cannot interleave.
func (s *Store) HoldTestLock(ctx context.Context) (release func(), err error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: test lock: %w", err)
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, testAdvisoryKey); err != nil {
		conn.Release()

		return nil, fmt.Errorf("store: test lock: %w", err)
	}
	var once sync.Once

	return func() {
		once.Do(func() {
			_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, testAdvisoryKey)
			conn.Release()
		})
	}, nil
}

// ResetTables truncates durable tables. Integration tests only.
func (s *Store) ResetTables(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		TRUNCATE mail_log, confirm_tokens, auth_codes, login_states,
		         oauth_clients, http_cache, names, blobs, app_secrets,
		         characters, users CASCADE`)

	return err
}
