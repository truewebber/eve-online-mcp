package write

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/truewebber/gopkg/log"
)

const errConfirmUnknown = "That confirm_token is unknown or has expired. Call the tool again without a token to get a fresh preview."

var (
	errMailHoldAbandoned = errors.New("write: abandoned mail-cap hold")
	errPersistRequired   = errors.New("write: persist is required")
	errLoggerRequired    = errors.New("write: logger is required")
	ErrMailCap           = errors.New("write: mail cap")
	ErrConfirmUnknown    = errors.New("write: confirm unknown")
	ErrConfirmArgs       = errors.New("write: confirm args changed")
	ErrConfirmTool       = errors.New("write: confirm tool mismatch")
	ErrUnknownCapability = errors.New("write: unknown capability")
	ErrMissingWriteScope = errors.New("write: missing write scope")
)

// MailCapRejections is the SPEC §11 counter for the mail-cap refusal.
var MailCapRejections atomic.Int64 //nolint:gochecknoglobals // SPEC §11: increment next to the refusal.

type BlockedError struct {
	Msg string
	why error
}

func (e BlockedError) Error() string { return e.Msg }

func (e BlockedError) Unwrap() error { return e.why }

func blocked(why error, msg string) BlockedError {
	return BlockedError{Msg: msg, why: why}
}

type Decision struct {
	Required map[string]any
}

type Record struct {
	Tool       string
	Capability string
	Args       map[string]any
	Outcome    string
	ESIStatus  int
	Error      string
}

type Guard struct {
	persist     Persist
	characterID int64
	sessionID   int64
	logger      log.Logger
	mailHold    *MailCapHold
}

func NewGuard(persist Persist, characterID, sessionID int64, logger log.Logger) (*Guard, error) {
	if persist == nil {
		return nil, errPersistRequired
	}
	if logger == nil {
		return nil, errLoggerRequired
	}

	return &Guard{persist: persist, characterID: characterID, sessionID: sessionID, logger: logger}, nil
}

func (g *Guard) CheckCapability(capability string) error {
	if _, ok := Capabilities()[capability]; !ok {
		return blocked(ErrUnknownCapability, fmt.Sprintf("Unknown write capability %q.", capability))
	}

	return nil
}

func (g *Guard) CheckScope(capability string, granted []string) error {
	need := Capabilities()[capability]
	have := map[string]struct{}{}
	for _, s := range granted {
		have[s] = struct{}{}
	}
	var missing []string
	for _, s := range need.Scopes {
		if _, ok := have[s]; !ok {
			missing = append(missing, s)
		}
	}
	if len(missing) > 0 {
		return blocked(ErrMissingWriteScope, fmt.Sprintf("This character was not authorized with %s. Re-authenticate the MCP server (Authentication required) and approve the full scope set.", strings.Join(missing, ", ")))
	}

	return nil
}

type Authz struct {
	Tool, Capability string
	Args, Preview    map[string]any
	Token            string
	Scopes           []string
}

func (g *Guard) Authorize(ctx context.Context, in Authz) (Decision, error) {
	if relErr := g.releaseMailHold(errMailHoldAbandoned); relErr != nil {
		g.logger.Error("write: mail-cap hold", "err", relErr)
	}
	err := g.CheckCapability(in.Capability)
	if err != nil {
		return Decision{}, err
	}
	err = g.CheckScope(in.Capability, in.Scopes)
	if err != nil {
		return Decision{}, err
	}
	if err := g.gateMailCap(ctx, in); err != nil {
		return Decision{}, err
	}
	digest, err := digestArgs(in.Args)
	if err != nil {
		if relErr := g.releaseMailHold(err); relErr != nil {
			g.logger.Error("write: mail-cap hold", "err", relErr)
		}

		return Decision{}, err
	}
	if in.Token != "" {
		return g.confirmWrite(ctx, in, digest)
	}

	return g.previewWrite(ctx, in, digest)
}

func (g *Guard) Record(ctx context.Context, rec Record) error {
	write := func(ctx context.Context) error {
		return g.appendRecord(ctx, rec)
	}
	var err error
	if g.mailHold != nil {
		err = g.mailHold.Do(write)
	} else {
		err = write(ctx)
	}
	if relErr := g.releaseMailHold(err); err == nil {
		err = relErr
	}

	return err
}

func (g *Guard) Status(ctx context.Context) map[string]any {
	ref := map[string]string{}
	for name, cap := range Capabilities() {
		ref[name] = cap.Summary
	}
	mails, pending := 0, 0
	if n, err := g.persist.CountMailCap(ctx, g.characterID); err == nil {
		mails = n
	}
	if n, err := g.persist.CountConfirm(ctx, g.sessionID); err == nil {
		pending = n
	}
	remaining := max(MailCap-mails, 0)

	return map[string]any{
		"capabilities":              CapabilityNames(),
		"capability_reference":      ref,
		"mails_last_hour":           mails,
		"mails_remaining_this_hour": remaining,
		"mail_cap_per_hour":         MailCap,
		"pending_confirmations":     pending,
		"confirm_ttl_seconds":       int(ConfirmTTL / time.Second),
		"confirm":                   "Each mutating tool returns a preview and confirm_token on the first call; a second call with identical arguments plus the token executes it. Mail is capped at 5 per rolling hour.",
	}
}

func (g *Guard) gateMailCap(ctx context.Context, in Authz) error {
	if in.Capability != CapMailSend {
		return nil
	}
	if in.Token != "" {
		return g.holdMailCap(ctx)
	}

	return g.checkMailCap(ctx)
}

func (g *Guard) confirmWrite(ctx context.Context, in Authz, digest string) (Decision, error) {
	err := g.consumeConfirm(ctx, in.Tool, digest, in.Token)
	if err != nil {
		if relErr := g.releaseMailHold(err); relErr != nil {
			g.logger.Error("write: mail-cap hold", "err", relErr)
		}

		return Decision{}, err
	}

	return Decision{}, nil
}

func (g *Guard) previewWrite(ctx context.Context, in Authz, digest string) (Decision, error) {
	token, err := randomToken()
	if err != nil {
		return Decision{}, err
	}
	err = g.persist.PutConfirm(ctx, Confirm{
		Token: token, SessionID: g.sessionID, Tool: in.Tool,
		ArgsDigest: digest, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		return Decision{}, wrap("Authorize", err)
	}
	ttlSec := int(ConfirmTTL / time.Second)

	return Decision{Required: map[string]any{
		"status": "confirmation_required", "tool": in.Tool, "capability": in.Capability,
		"will_do": in.Preview, "confirm_token": token,
		"expires_in_seconds": ttlSec,
		"next_step": fmt.Sprintf(
			"Show 'will_do' to the user and get their explicit go-ahead, then call %s again with identical arguments plus confirm_token='%s'.",
			in.Tool, token,
		),
	}}, nil
}

func (g *Guard) appendRecord(ctx context.Context, rec Record) error {
	digest, err := digestArgs(rec.Args)
	if err != nil {
		return err
	}
	outcome := rec.Outcome
	if outcome == "" {
		outcome = OutcomeOK
	}

	return wrap("Record", g.persist.AppendMutation(ctx, Mutation{
		CharacterID: g.characterID,
		SessionID:   g.sessionID,
		Tool:        rec.Tool,
		Capability:  rec.Capability,
		ArgsDigest:  digest,
		Summary:     truncate(summarize(rec.Tool, rec.Args), auditFieldMax),
		Outcome:     outcome,
		ESIStatus:   rec.ESIStatus,
		Error:       truncate(rec.Error, auditFieldMax),
	}))
}

func (g *Guard) checkMailCap(ctx context.Context) error {
	n, err := g.persist.CountMailCap(ctx, g.characterID)
	if err != nil {
		return wrap("checkMailCap", err)
	}
	if n >= MailCap {
		MailCapRejections.Add(1)

		return mailCapBlocked()
	}

	return nil
}

func (g *Guard) holdMailCap(ctx context.Context) error {
	hold, err := g.persist.HoldMailCap(ctx, g.characterID)
	if err != nil {
		return wrap("holdMailCap", err)
	}
	if hold.Count >= MailCap {
		if relErr := hold.Release(ErrMailCap); relErr != nil {
			g.logger.Error("write: mail-cap hold", "err", relErr)
		}
		MailCapRejections.Add(1)

		return mailCapBlocked()
	}
	g.mailHold = hold

	return nil
}

func (g *Guard) releaseMailHold(err error) error {
	if g.mailHold == nil {
		return nil
	}
	relErr := g.mailHold.Release(err)
	g.mailHold = nil

	return relErr
}

func mailCapBlocked() BlockedError {
	return blocked(ErrMailCap, fmt.Sprintf("Mail budget exhausted: %d mails in the last hour. Wait until an earlier send drops out of the rolling hour, then try again.", MailCap))
}

func (g *Guard) consumeConfirm(ctx context.Context, tool, digest, confirmToken string) error {
	pending, err := g.persist.GetConfirm(ctx, confirmToken)
	if errors.Is(err, ErrConfirmNotFound) || pending == nil {
		return blocked(ErrConfirmUnknown, errConfirmUnknown)
	}
	if err != nil {
		return wrap("consumeConfirm", err)
	}
	if pending.SessionID != g.sessionID {
		return blocked(ErrConfirmUnknown, errConfirmUnknown)
	}
	if time.Since(pending.CreatedAt) > ConfirmTTL {
		if err := g.persist.DeleteConfirm(ctx, confirmToken); err != nil {
			return wrap("consumeConfirm", err)
		}

		return blocked(ErrConfirmUnknown, errConfirmUnknown)
	}
	if pending.Tool != tool {
		return blocked(ErrConfirmTool, fmt.Sprintf("confirm_token was issued for %q, not %q.", pending.Tool, tool))
	}
	if pending.ArgsDigest != digest {
		if err := g.persist.DeleteConfirm(ctx, confirmToken); err != nil {
			return wrap("consumeConfirm", err)
		}

		return blocked(ErrConfirmArgs, "The arguments changed since the preview was generated, so the token was discarded. Request a new preview and confirm that one.")
	}
	if err := g.persist.DeleteConfirm(ctx, confirmToken); err != nil {
		return wrap("consumeConfirm", err)
	}

	return nil
}

func digestArgs(payload any) (string, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", wrap("digestArgs", err)
	}
	sum := sha256.Sum256(raw)

	return hex.EncodeToString(sum[:])[:16], nil
}

func randomToken() (string, error) {
	var b [9]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", wrap("randomToken", err)
	}

	return strings.TrimRight(hex.EncodeToString(b[:]), "="), nil
}
