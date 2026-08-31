package store

import (
	"context"
	"errors"
	"time"

	domuser "github.com/truewebber/eve-online-mcp/internal/domain/user"

	"github.com/jackc/pgx/v5"
)

func (s *Store) CreateUser(ctx context.Context) (*domuser.User, error) {
	now := time.Now().UTC()
	u := &domuser.User{
		ID:        domuser.NewID(),
		CreatedAt: now.Format(time.RFC3339),
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO users (id, created_at) VALUES ($1, $2)`, u.ID, now)
	if err != nil {
		return nil, wrap("CreateUser", err)
	}

	return u, nil
}

func (s *Store) GetUser(ctx context.Context, id string) (*domuser.User, error) {
	var created time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT id, created_at FROM users WHERE id = $1`, id,
	).Scan(&id, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, wrap("GetUser", err)
	}

	return &domuser.User{ID: id, CreatedAt: created.UTC().Format(time.RFC3339)}, nil
}

func (s *Store) UserExists(ctx context.Context, id string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE id = $1)`, id,
	).Scan(&ok)

	return ok, wrap("UserExists", err)
}
