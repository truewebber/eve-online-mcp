package session

import (
	"context"
	"errors"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/domain/confirm"
	"github.com/truewebber/eve-online-mcp/internal/domain/mutation"
	"github.com/truewebber/eve-online-mcp/internal/domain/write"
)

type guardPersist struct {
	mutations mutation.Repository
	confirms  confirm.Repository
}

func (p guardPersist) PutConfirm(ctx context.Context, c write.Confirm) error {
	return wrap("PutConfirm", p.confirms.Put(ctx, confirm.Confirm{
		Value: c.Token, CharacterID: c.CharacterID, Tool: c.Tool,
		ArgsDigest: c.ArgsDigest, CreatedAt: c.CreatedAt,
	}))
}

func (p guardPersist) GetConfirm(ctx context.Context, token string) (*write.Confirm, error) {
	row, err := p.confirms.Get(ctx, token)
	if errors.Is(err, confirm.ErrNotFound) {
		return nil, write.ErrConfirmNotFound
	}
	if err != nil {
		return nil, wrap("GetConfirm", err)
	}

	return &write.Confirm{
		Token: row.Value, CharacterID: row.CharacterID, Tool: row.Tool,
		ArgsDigest: row.ArgsDigest, CreatedAt: row.CreatedAt,
	}, nil
}

func (p guardPersist) DeleteConfirm(ctx context.Context, token string) error {
	return wrap("DeleteConfirm", p.confirms.Delete(ctx, token))
}

func (p guardPersist) CountConfirm(ctx context.Context, characterID int64) (int, error) {
	n, err := p.confirms.Count(ctx, characterID)

	return n, wrap("CountConfirm", err)
}

func (p guardPersist) CountMailSince(ctx context.Context, characterID int64, since time.Time) (int, error) {
	n, err := p.mutations.CountSince(ctx, characterID, since)

	return n, wrap("CountMailSince", err)
}

func (p guardPersist) InsertMail(ctx context.Context, characterID int64, at time.Time) error {
	return wrap("InsertMail", p.mutations.Insert(ctx, mutation.Mail{
		CharacterID: characterID, SentAt: at,
	}))
}
