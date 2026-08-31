package session

import (
	"context"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/adapter/store"
	"github.com/truewebber/eve-online-mcp/internal/domain/write"
)

type guardPersist struct{ db *store.Store }

func (p guardPersist) PutConfirm(ctx context.Context, c write.Confirm) error {
	return wrap("PutConfirm", p.db.PutConfirmToken(ctx, store.ConfirmToken{
		Token: c.Token, UserID: c.UserID, Tool: c.Tool,
		ArgsDigest: c.ArgsDigest, CreatedAt: c.CreatedAt,
	}))
}

func (p guardPersist) GetConfirm(ctx context.Context, token string) (*write.Confirm, bool, error) {
	row, ok, err := p.db.GetConfirmToken(ctx, token)
	if err != nil || !ok {
		return nil, ok, wrap("GetConfirm", err)
	}

	return &write.Confirm{
		Token: row.Token, UserID: row.UserID, Tool: row.Tool,
		ArgsDigest: row.ArgsDigest, CreatedAt: row.CreatedAt,
	}, true, nil
}

func (p guardPersist) DeleteConfirm(ctx context.Context, token string) error {
	return wrap("DeleteConfirm", p.db.DeleteConfirmToken(ctx, token))
}

func (p guardPersist) CountConfirm(ctx context.Context, userID string) (int, error) {
	n, err := p.db.CountConfirmTokens(ctx, userID)

	return n, wrap("CountConfirm", err)
}

func (p guardPersist) CountMailSince(ctx context.Context, userID string, since time.Time) (int, error) {
	n, err := p.db.CountMailSince(ctx, userID, since)

	return n, wrap("CountMailSince", err)
}

func (p guardPersist) InsertMail(ctx context.Context, userID string, at time.Time) error {
	return wrap("InsertMail", p.db.InsertMail(ctx, userID, at))
}
