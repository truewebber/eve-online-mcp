package pgx

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/domain/character"
	"github.com/truewebber/eve-online-mcp/internal/postgres"

	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repo struct {
	pool *pgxpool.Pool
}

const (
	upsertSQL = `
		INSERT INTO characters (
			character_id, name, owner_hash, added_at, deleted_at
		) VALUES ($1, $2, $3, $4, NULL)
		ON CONFLICT (character_id) DO UPDATE SET
			name = EXCLUDED.name,
			owner_hash = EXCLUDED.owner_hash,
			deleted_at = NULL`
	getSQL = `
		SELECT character_id, name, owner_hash, added_at, deleted_at
		FROM characters WHERE character_id = $1`
	deleteSQL = `
		UPDATE characters
		SET deleted_at = now()
		WHERE character_id = $1 AND deleted_at IS NULL`
)

func New(pool *pgxpool.Pool) *Repo {
	if pool == nil {
		panic("character/pgx: pool is required")
	}

	return &Repo{pool: pool}
}

func (r *Repo) Upsert(ctx context.Context, c character.Character) error {
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	_, err := postgres.Q(ctx, r.pool).Exec(ctx, upsertSQL,
		c.ID, c.Name, c.OwnerHash, c.CreatedAt,
	)
	if err != nil {
		return wrap("Upsert", err)
	}

	return nil
}

func (r *Repo) Get(ctx context.Context, id int64) (*character.Character, error) {
	row, err := scan(postgres.Q(ctx, r.pool).QueryRow(ctx, getSQL, id))
	if errors.Is(err, jackpgx.ErrNoRows) {
		return nil, character.ErrNotFound
	}

	return row, err
}

func (r *Repo) Delete(ctx context.Context, id int64) error {
	tag, err := postgres.Q(ctx, r.pool).Exec(ctx, deleteSQL, id)
	if err != nil {
		return wrap("Delete", err)
	}
	if tag.RowsAffected() == 0 {
		return character.ErrNotFound
	}

	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scan(row scanner) (*character.Character, error) {
	var c character.Character
	err := row.Scan(&c.ID, &c.Name, &c.OwnerHash, &c.CreatedAt, &c.DeletedAt)
	if err != nil {
		return nil, wrap("scan", err)
	}

	return &c, nil
}

func wrap(op string, err error) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("character: %s: %w", op, err)
}
