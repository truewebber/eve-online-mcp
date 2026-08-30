package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Store) UpsertCharacter(ctx context.Context, userID string, row CharacterRow) error {
	if row.Scopes == nil {
		row.Scopes = []string{}
	}
	if row.AddedAt.IsZero() {
		row.AddedAt = time.Now().UTC()
	}
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO characters (
			character_id, user_id, name, owner_hash, refresh_token, scopes, added_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (character_id) DO UPDATE SET
			name = EXCLUDED.name,
			owner_hash = EXCLUDED.owner_hash,
			refresh_token = EXCLUDED.refresh_token,
			scopes = EXCLUDED.scopes
		WHERE characters.user_id = EXCLUDED.user_id`,
		row.CharacterID, userID, row.Name, row.OwnerHash, row.RefreshToken, row.Scopes, row.AddedAt,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrOwned
	}

	return nil
}

func (s *Store) GetCharacter(ctx context.Context, characterID int64) (*CharacterRow, error) {
	row, err := scanCharacter(s.pool.QueryRow(ctx, `
		SELECT character_id, user_id, name, owner_hash, refresh_token, scopes, added_at
		FROM characters WHERE character_id = $1`, characterID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}

	return row, err
}

func (s *Store) ListCharacters(ctx context.Context, userID string) ([]CharacterRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT character_id, user_id, name, owner_hash, refresh_token, scopes, added_at
		FROM characters WHERE user_id = $1 ORDER BY added_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CharacterRow
	for rows.Next() {
		row, err := scanCharacter(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *row)
	}

	return out, rows.Err()
}

func (s *Store) DeleteCharacter(ctx context.Context, characterID int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM characters WHERE character_id = $1`, characterID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}

	return nil
}

func (s *Store) OwnerOf(ctx context.Context, characterID int64) (userID string, ok bool, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT user_id FROM characters WHERE character_id = $1`, characterID,
	).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}

	return userID, true, nil
}

// WithCharacterForUpdate locks the character row, passes the current refresh
// token to fn, and writes the returned token (CCP may rotate it).
func (s *Store) WithCharacterForUpdate(ctx context.Context, characterID int64, fn func(refreshToken string) (string, error)) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var refresh string
	err = tx.QueryRow(ctx,
		`SELECT refresh_token FROM characters WHERE character_id = $1 FOR UPDATE`, characterID,
	).Scan(&refresh)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	next, err := fn(refresh)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE characters SET refresh_token = $2 WHERE character_id = $1`, characterID, next,
	); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

type characterScanner interface {
	Scan(dest ...any) error
}

func scanCharacter(row characterScanner) (*CharacterRow, error) {
	var c CharacterRow
	err := row.Scan(&c.CharacterID, &c.UserID, &c.Name, &c.OwnerHash, &c.RefreshToken, &c.Scopes, &c.AddedAt)
	if err != nil {
		return nil, err
	}
	if c.Scopes == nil {
		c.Scopes = []string{}
	}

	return &c, nil
}
