package pgx

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/domain/oauthclient"
	"github.com/truewebber/eve-online-mcp/internal/postgres"

	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	upsertSQL = `
		INSERT INTO oauth_clients (client_id, client_name, redirect_uris, created_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (client_id) DO UPDATE SET
			client_name = EXCLUDED.client_name,
			redirect_uris = EXCLUDED.redirect_uris,
			deleted_at = NULL`
	getSQL = `
		SELECT client_id, client_name, redirect_uris, created_at
		FROM oauth_clients WHERE client_id = $1 AND deleted_at IS NULL`
	softDeleteAbandonedSQL = `
		UPDATE oauth_clients c
		SET deleted_at = now()
		WHERE c.deleted_at IS NULL
		  AND c.created_at < now() - interval '30 days'
		  AND NOT EXISTS (
			SELECT 1 FROM sessions s WHERE s.mcp_client_id = c.client_id
		  )`
	deleteLongSoftDeletedSQL = `
		DELETE FROM oauth_clients
		WHERE deleted_at < now() - interval '30 days'`
)

type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

func (r *Repo) Upsert(ctx context.Context, c oauthclient.Client) error {
	if c.RedirectURIs == nil {
		c.RedirectURIs = []string{}
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	_, err := postgres.Q(ctx, r.pool).Exec(ctx, upsertSQL, c.ID, c.Name, c.RedirectURIs, c.CreatedAt)

	return wrap("Upsert", err)
}

func (r *Repo) Get(ctx context.Context, id string) (*oauthclient.Client, error) {
	var c oauthclient.Client
	err := postgres.Q(ctx, r.pool).QueryRow(ctx, getSQL, id).Scan(&c.ID, &c.Name, &c.RedirectURIs, &c.CreatedAt)
	if errors.Is(err, jackpgx.ErrNoRows) {
		return nil, oauthclient.ErrNotFound
	}
	if err != nil {
		return nil, wrap("Get", err)
	}
	if c.RedirectURIs == nil {
		c.RedirectURIs = []string{}
	}

	return &c, nil
}

func (r *Repo) SoftDeleteAbandoned(ctx context.Context) (int64, error) {
	tag, err := postgres.Q(ctx, r.pool).Exec(ctx, softDeleteAbandonedSQL)
	if err != nil {
		return 0, wrap("SoftDeleteAbandoned", err)
	}

	return tag.RowsAffected(), nil
}

func (r *Repo) DeleteLongSoftDeleted(ctx context.Context) (int64, error) {
	tag, err := postgres.Q(ctx, r.pool).Exec(ctx, deleteLongSoftDeletedSQL)
	if err != nil {
		return 0, wrap("DeleteLongSoftDeleted", err)
	}

	return tag.RowsAffected(), nil
}

func wrap(op string, err error) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("oauthclient: %s: %w", op, err)
}
