package session

import (
	"context"
	"time"

	"eve-mcp/internal/adapter/store"
	"eve-mcp/internal/domain/write"
)

// guardPersist adapts adapter/store to write.Persist.
type guardPersist struct{ db *store.Store }

func (p guardPersist) PutConfirm(ctx context.Context, c write.Confirm) error {
	return p.db.PutConfirmToken(ctx, store.ConfirmToken{
		Token: c.Token, UserID: c.UserID, Tool: c.Tool,
		ArgsDigest: c.ArgsDigest, CreatedAt: c.CreatedAt,
	})
}

func (p guardPersist) GetConfirm(ctx context.Context, token string) (*write.Confirm, bool, error) {
	row, ok, err := p.db.GetConfirmToken(ctx, token)
	if err != nil || !ok {
		return nil, ok, err
	}
	return &write.Confirm{
		Token: row.Token, UserID: row.UserID, Tool: row.Tool,
		ArgsDigest: row.ArgsDigest, CreatedAt: row.CreatedAt,
	}, true, nil
}

func (p guardPersist) DeleteConfirm(ctx context.Context, token string) error {
	return p.db.DeleteConfirmToken(ctx, token)
}

func (p guardPersist) CountConfirm(ctx context.Context, userID string) (int, error) {
	return p.db.CountConfirmTokens(ctx, userID)
}

func (p guardPersist) CountMailSince(ctx context.Context, userID string, since time.Time) (int, error) {
	return p.db.CountMailSince(ctx, userID, since)
}

func (p guardPersist) InsertMail(ctx context.Context, userID string, at time.Time) error {
	return p.db.InsertMail(ctx, userID, at)
}
