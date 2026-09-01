package write

import (
	"context"
	"errors"
	"time"
)

var (
	ErrConfirmNotFound = errors.New("write: confirm not found")
	errNoMailCapHold   = errors.New("write: no mail-cap hold")
)

const (
	OutcomeOK           = "ok"
	OutcomeError        = "error"
	ToolMailSend        = "eve_mail_send"
	ToolMailMark        = "eve_mail_mark"
	ToolMailDelete      = "eve_mail_delete"
	ToolMailCompose     = "eve_mail_compose"
	ToolCalendarRespond = "eve_calendar_respond"
	ToolContactsSet     = "eve_contacts_set"
	ToolContactsDelete  = "eve_contacts_delete"
	ToolFittingSave     = "eve_fitting_save"
	ToolFittingDelete   = "eve_fitting_delete"
	ToolUISetWaypoint   = "eve_ui_set_waypoint"
	ToolUIOpenWindow    = "eve_ui_open_window"
)

// One-shot: consume on the mutating call, not on preview.
type Confirm struct {
	Token      string
	SessionID  int64
	Tool       string
	ArgsDigest string
	CreatedAt  time.Time
}

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

// MailCapHold is an open mail-cap transaction. Count and the insert it
// authorises share one pg_advisory_xact_lock (SPEC §5.4).
type MailCapHold struct {
	Count int
	do    func(func(context.Context) error) error
	end   func(error) error
}

func NewMailCapHold(n int, do func(func(context.Context) error) error, end func(error) error) *MailCapHold {
	return &MailCapHold{Count: n, do: do, end: end}
}

func (h *MailCapHold) Do(fn func(context.Context) error) error {
	if h == nil || h.do == nil {
		return errNoMailCapHold
	}

	return h.do(fn)
}

func (h *MailCapHold) Release(err error) error {
	if h == nil || h.end == nil {
		return nil
	}
	fn := h.end
	h.end = nil

	return fn(err)
}

// Implemented outside this package so domain/write does not import the adapter.
//
//go:generate go tool go.uber.org/mock/mockgen -destination=../../mocks/write.go -package=mocks -mock_names=Persist=MockWritePersist github.com/truewebber/eve-online-mcp/internal/domain/write Persist
type Persist interface {
	PutConfirm(ctx context.Context, c Confirm) error
	GetConfirm(ctx context.Context, token string) (*Confirm, error)
	DeleteConfirm(ctx context.Context, token string) error
	CountConfirm(ctx context.Context, sessionID int64) (int, error)
	CountMailCap(ctx context.Context, characterID int64) (int, error)
	AppendMutation(ctx context.Context, m Mutation) error
	HoldMailCap(ctx context.Context, characterID int64) (*MailCapHold, error)
}
