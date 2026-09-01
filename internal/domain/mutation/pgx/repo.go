package pgx

import (
	"context"
	"fmt"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/domain/mutation"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	countSinceSQL = `SELECT COUNT(*) FROM mail_log WHERE character_id = $1 AND sent_at >= $2`
	insertSQL     = `INSERT INTO mail_log (character_id, sent_at) VALUES ($1, $2)`
)

type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

func (r *Repo) CountSince(ctx context.Context, characterID int64, since time.Time) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, countSinceSQL, characterID, since).Scan(&n)

	return n, wrap("CountSince", err)
}

func (r *Repo) Insert(ctx context.Context, mail mutation.Mail) error {
	_, err := r.pool.Exec(ctx, insertSQL, mail.CharacterID, mail.SentAt)

	return wrap("Insert", err)
}

func wrap(op string, err error) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("mutation: %s: %w", op, err)
}
