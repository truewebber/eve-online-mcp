package store

import (
	"context"
	"crypto/rand"
)

func (s *Store) GetOrCreateSecret(ctx context.Context, name string) ([]byte, error) {
	value := make([]byte, SecretBytes)
	if _, err := rand.Read(value); err != nil {
		return nil, wrap("GetOrCreateSecret", err)
	}
	_, err := s.pool.Exec(ctx, insertAppRowSQL, name, value)
	if err != nil {
		return nil, wrap("GetOrCreateSecret", err)
	}
	var out []byte
	err = s.pool.QueryRow(ctx, selectAppValueSQL, name).Scan(&out)

	return out, wrap("GetOrCreateSecret", err)
}

const (
	insertAppRowSQL = `
		INSERT INTO app_secrets (name, value) VALUES ($1, $2)
		ON CONFLICT (name) DO NOTHING`
	selectAppValueSQL = `SELECT value FROM app_secrets WHERE name = $1`
)
