package pgx

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/truewebber/gopkg/log"

	"github.com/truewebber/eve-online-mcp/internal/domain/character"

	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	upsertSQL = `
		INSERT INTO characters (
			character_id, name, owner_hash, refresh_token, scopes, added_at, deleted_at
		) VALUES ($1, $2, $3, $4, $5, $6, NULL)
		ON CONFLICT (character_id) DO UPDATE SET
			name = EXCLUDED.name,
			owner_hash = EXCLUDED.owner_hash,
			refresh_token = EXCLUDED.refresh_token,
			scopes = EXCLUDED.scopes,
			deleted_at = NULL`
	getSQL = `
		SELECT character_id, name, owner_hash, refresh_token, scopes, added_at, deleted_at
		FROM characters WHERE character_id = $1`
	deleteSQL = `
		UPDATE characters
		SET deleted_at = now(), refresh_token = ''
		WHERE character_id = $1 AND deleted_at IS NULL`
	lockRefreshSQL  = `SELECT refresh_token FROM characters WHERE character_id = $1 AND deleted_at IS NULL FOR UPDATE`
	writeRefreshSQL = `UPDATE characters SET refresh_token = $2 WHERE character_id = $1`
)

type Repo struct {
	pool   *pgxpool.Pool
	logger log.Logger
}

func New(pool *pgxpool.Pool, logger log.Logger) *Repo {
	return &Repo{pool: pool, logger: logger}
}

func (r *Repo) Upsert(ctx context.Context, c character.Character) error {
	if c.Scopes == nil {
		c.Scopes = []string{}
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	_, err := r.pool.Exec(ctx, upsertSQL,
		c.ID, c.Name, c.OwnerHash, c.RefreshToken, c.Scopes, c.CreatedAt,
	)
	if err != nil {
		return wrap("Upsert", err)
	}

	return nil
}

func (r *Repo) Get(ctx context.Context, id int64) (*character.Character, error) {
	row, err := scan(r.pool.QueryRow(ctx, getSQL, id))
	if errors.Is(err, jackpgx.ErrNoRows) {
		return nil, character.ErrNotFound
	}

	return row, err
}

func (r *Repo) Delete(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, deleteSQL, id)
	if err != nil {
		return wrap("Delete", err)
	}
	if tag.RowsAffected() == 0 {
		return character.ErrNotFound
	}

	return nil
}

func (r *Repo) UpdateRefresh(ctx context.Context, id int64, fn func(string) (string, error)) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return wrap("UpdateRefresh", err)
	}
	defer r.rollback(ctx, tx)

	var refresh string
	err = tx.QueryRow(ctx, lockRefreshSQL, id).Scan(&refresh)
	if errors.Is(err, jackpgx.ErrNoRows) {
		return character.ErrNotFound
	}
	if err != nil {
		return wrap("UpdateRefresh", err)
	}
	next, err := fn(refresh)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, writeRefreshSQL, id, next); err != nil {
		return wrap("UpdateRefresh", err)
	}

	return wrap("UpdateRefresh", tx.Commit(ctx))
}

func (r *Repo) rollback(ctx context.Context, tx jackpgx.Tx) {
	if err := tx.Rollback(ctx); err != nil && !errors.Is(err, jackpgx.ErrTxClosed) {
		r.logger.Error("character: rollback", "err", err)
	}
}

type scanner interface {
	Scan(dest ...any) error
}

func scan(row scanner) (*character.Character, error) {
	var c character.Character
	err := row.Scan(&c.ID, &c.Name, &c.OwnerHash, &c.RefreshToken, &c.Scopes, &c.CreatedAt, &c.DeletedAt)
	if err != nil {
		return nil, wrap("scan", err)
	}
	if c.Scopes == nil {
		c.Scopes = []string{}
	}

	return &c, nil
}

func wrap(op string, err error) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("character: %s: %w", op, err)
}
