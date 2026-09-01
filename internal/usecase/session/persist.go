package session

import (
	"context"
	"errors"

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
		Value: c.Token, SessionID: c.SessionID, Tool: c.Tool,
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
		Token: row.Value, SessionID: row.SessionID, Tool: row.Tool,
		ArgsDigest: row.ArgsDigest, CreatedAt: row.CreatedAt,
	}, nil
}

func (p guardPersist) DeleteConfirm(ctx context.Context, token string) error {
	return wrap("DeleteConfirm", p.confirms.Delete(ctx, token))
}

func (p guardPersist) CountConfirm(ctx context.Context, sessionID int64) (int, error) {
	n, err := p.confirms.Count(ctx, sessionID)

	return n, wrap("CountConfirm", err)
}

func (p guardPersist) CountMailCap(ctx context.Context, characterID int64) (int, error) {
	n, err := p.mutations.CountMailCap(ctx, characterID)

	return n, wrap("CountMailCap", err)
}

func (p guardPersist) AppendMutation(ctx context.Context, m write.Mutation) error {
	return wrap("AppendMutation", p.mutations.Append(ctx, mutation.Mutation{
		CharacterID: m.CharacterID,
		SessionID:   m.SessionID,
		Tool:        m.Tool,
		Capability:  m.Capability,
		ArgsDigest:  m.ArgsDigest,
		Summary:     m.Summary,
		Outcome:     m.Outcome,
		ESIStatus:   m.ESIStatus,
		Error:       m.Error,
	}))
}

func (p guardPersist) HoldMailCap(ctx context.Context, characterID int64) (*write.MailCapHold, error) {
	h, err := p.mutations.HoldMailCap(ctx, characterID)
	if err != nil {
		return nil, wrap("HoldMailCap", err)
	}

	hold, err := write.NewMailCapHold(h.Count, h.Do, h.Release)
	if err != nil {
		return nil, wrap("HoldMailCap", err)
	}

	return hold, nil
}
