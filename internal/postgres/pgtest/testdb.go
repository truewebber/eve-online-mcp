package pgtest

import (
	"context"
	"sync"

	"github.com/truewebber/gopkg/log"

	"github.com/truewebber/eve-online-mcp/internal/postgres"
)

const (
	testAdvisoryKey = int64(87265001)
	lockSQL         = `SELECT pg_advisory_lock($1)`
	unlockSQL       = `SELECT pg_advisory_unlock($1)`
	resetTablesSQL  = `
	TRUNCATE mail_log, confirm_tokens, auth_codes, login_states,
	         sessions, oauth_clients, characters CASCADE`
)

func HoldTestLock(ctx context.Context, db *postgres.DB, logger log.Logger) (func(), error) {
	conn, err := db.Pool().Acquire(ctx)
	if err != nil {
		return nil, wrap("test lock", err)
	}
	if _, err := conn.Exec(ctx, lockSQL, testAdvisoryKey); err != nil {
		conn.Release()

		return nil, wrap("test lock", err)
	}
	var once sync.Once
	unlockCtx := context.WithoutCancel(ctx)

	return func() {
		once.Do(func() {
			if _, err := conn.Exec(unlockCtx, unlockSQL, testAdvisoryKey); err != nil {
				logger.Error("pgtest: test lock unlock", "err", err)
			}
			conn.Release()
		})
	}, nil
}

func ResetTables(ctx context.Context, db *postgres.DB) error {
	_, err := db.Pool().Exec(ctx, resetTablesSQL)

	return wrap("ResetTables", err)
}
