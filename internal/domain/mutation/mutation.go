package mutation

import (
	"context"
	"errors"
)

var errHoldRequired = errors.New("mutation: hold funcs are required")

const (
	OutcomeOK    = "ok"
	OutcomeError = "error"
	ToolMailSend = "eve_mail_send"
)

type Mutation struct {
	CharacterID int64
	SessionID   int64
	Tool        string
	Capability  string
	ArgsDigest  string
	Summary     string
	Outcome     string
	ESIStatus   int
	Error       string
}

// Hold is an open mail-cap transaction: the count that authorised a
// send and the insert that records it share one pg_advisory_xact_lock.
type Hold struct {
	Count int
	do    func(func(context.Context) error) error
	end   func(error) error
}

func NewHold(n int, do func(func(context.Context) error) error, end func(error) error) (*Hold, error) {
	if do == nil || end == nil {
		return nil, errHoldRequired
	}

	return &Hold{Count: n, do: do, end: end}, nil
}

func (h *Hold) Do(fn func(context.Context) error) error {
	return h.do(fn)
}

func (h *Hold) Release(err error) error {
	fn := h.end
	h.end = nil

	return fn(err)
}

//go:generate go tool go.uber.org/mock/mockgen -destination=../../mocks/mutation.go -package=mocks -mock_names=Repository=MockMutationRepository github.com/truewebber/eve-online-mcp/internal/domain/mutation Repository
type Repository interface {
	Append(ctx context.Context, m Mutation) error
	CountMailCap(ctx context.Context, characterID int64) (int, error)
	HoldMailCap(ctx context.Context, characterID int64) (*Hold, error)
	DeleteOld(ctx context.Context) (int64, error)
}
