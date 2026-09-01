package pgx

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/truewebber/eve-online-mcp/internal/domain/mutation"
	"github.com/truewebber/eve-online-mcp/internal/postgres"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// Distinct from the sign-in lock, which is pg_advisory_xact_lock(character_id).
	mailCapLockSalt int64 = 0x6d61696c5f636170 // "mail_cap"

	lockMailCapSQL  = `SELECT pg_advisory_xact_lock($1)`
	countMailCapSQL = `
		SELECT COUNT(*)
		FROM mutations
		WHERE character_id = $1
		  AND tool = 'eve_mail_send'
		  AND outcome = 'ok'
		  AND created_at >= now() - interval '1 hour'`
	appendSQL = `
		INSERT INTO mutations (
			character_id, session_id, tool, capability, args_digest,
			summary, outcome, esi_status, error
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
	deleteOldSQL = `DELETE FROM mutations WHERE created_at < now() - interval '90 days'`
)

type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

func mailCapLockKey(characterID int64) int64 {
	return characterID ^ mailCapLockSalt
}

func (r *Repo) Append(ctx context.Context, m mutation.Mutation) error {
	write := func(ctx context.Context) error {
		_, err := postgres.Q(ctx, r.pool).Exec(ctx, appendSQL,
			m.CharacterID, nullIfZero(m.SessionID), m.Tool, m.Capability, m.ArgsDigest,
			m.Summary, m.Outcome, nullIfZero(int64(m.ESIStatus)), nullIfEmpty(m.Error),
		)
		if err != nil {
			return wrap("Append", err)
		}

		return nil
	}
	if m.Tool == mutation.ToolMailSend && m.Outcome == mutation.OutcomeOK {
		return r.withMailCap(ctx, m.CharacterID, write)
	}

	return write(ctx)
}

func (r *Repo) DeleteOld(ctx context.Context) (int64, error) {
	tag, err := postgres.Q(ctx, r.pool).Exec(ctx, deleteOldSQL)
	if err != nil {
		return 0, wrap("DeleteOld", err)
	}

	return tag.RowsAffected(), nil
}

func (r *Repo) CountMailCap(ctx context.Context, characterID int64) (int, error) {
	var n int
	err := r.withMailCap(ctx, characterID, func(ctx context.Context) error {
		if err := postgres.Q(ctx, r.pool).QueryRow(ctx, countMailCapSQL, characterID).Scan(&n); err != nil {
			return wrap("CountMailCap", err)
		}

		return nil
	})

	return n, err
}

func (r *Repo) HoldMailCap(ctx context.Context, characterID int64) (*mutation.Hold, error) {
	if postgres.Tx(ctx) != nil {
		n, err := r.CountMailCap(ctx, characterID)
		if err != nil {
			return nil, err
		}

		return mutation.NewHold(n, func(fn func(context.Context) error) error {
			return fn(ctx)
		}, func(error) error { return nil }), nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, wrap("HoldMailCap", err)
	}
	holdCtx := postgres.WithTx(context.WithoutCancel(ctx), tx)
	n, err := r.CountMailCap(holdCtx, characterID)
	if err != nil {
		if rbErr := tx.Rollback(holdCtx); rbErr != nil && !postgres.IsTxClosed(rbErr) {
			return nil, wrap("HoldMailCap", errors.Join(err, rbErr))
		}

		return nil, err
	}
	var once sync.Once
	var endErr error

	return mutation.NewHold(n, func(fn func(context.Context) error) error {
		return fn(holdCtx)
	}, func(execErr error) error {
		once.Do(func() {
			if execErr != nil {
				endErr = tx.Rollback(holdCtx)
				if postgres.IsTxClosed(endErr) {
					endErr = nil
				}

				return
			}
			endErr = tx.Commit(holdCtx)
		})

		return wrap("HoldMailCap", endErr)
	}), nil
}

func (r *Repo) withMailCap(ctx context.Context, characterID int64, fn func(context.Context) error) error {
	run := func(ctx context.Context) error {
		if _, err := postgres.Q(ctx, r.pool).Exec(ctx, lockMailCapSQL, mailCapLockKey(characterID)); err != nil {
			return wrap("lockMailCap", err)
		}

		return fn(ctx)
	}
	if postgres.Tx(ctx) != nil {
		return run(ctx)
	}
	if err := postgres.WithinTx(ctx, r.pool, run); err != nil {
		return wrap("withMailCap", err)
	}

	return nil
}

func nullIfZero(n int64) any {
	if n == 0 {
		return nil
	}

	return n
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}

	return s
}

func wrap(op string, err error) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("mutation: %s: %w", op, err)
}
