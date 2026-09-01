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
	errMailCap           = errors.New("write: mail cap")
)

// MailCapRejections is the SPEC §11 counter for the mail-cap refusal.
var MailCapRejections atomic.Int64 //nolint:gochecknoglobals // SPEC §11: increment next to the refusal.

type BlockedError struct{ Msg string }

func (e BlockedError) Error() string { return e.Msg }

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

func NewGuard(persist Persist, characterID, sessionID int64, logger log.Logger) *Guard {
	return &Guard{persist: persist, characterID: characterID, sessionID: sessionID, logger: logger}
}

func (g *Guard) CheckCapability(capability string) error {
	if _, ok := Capabilities()[capability]; !ok {
		return BlockedError{Msg: fmt.Sprintf("Unknown write capability %q.", capability)}
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
		return BlockedError{Msg: fmt.Sprintf("This character was not authorized with %s. Re-authenticate the MCP server (Authentication required) and approve the full scope set.", strings.Join(missing, ", "))}
	}

	return nil
}

func (g *Guard) Authorize(ctx context.Context, tool, capability string, args map[string]any, preview map[string]any, confirmToken string, granted []string) (Decision, error) {
	if relErr := g.releaseMailHold(errMailHoldAbandoned); relErr != nil {
		g.logMailHold(relErr)
	}
	err := g.CheckCapability(capability)
	if err != nil {
		return Decision{}, err
	}
	err = g.CheckScope(capability, granted)
	if err != nil {
		return Decision{}, err
	}
	if capability == CapMailSend {
		if confirmToken != "" {
			err = g.holdMailCap(ctx)
		} else {
			err = g.checkMailCap(ctx)
		}
		if err != nil {
			return Decision{}, err
		}
	}
	digest, err := digestArgs(args)
	if err != nil {
		if relErr := g.releaseMailHold(err); relErr != nil {
			g.logMailHold(relErr)
		}

		return Decision{}, err
	}
	if confirmToken != "" {
		err := g.consumeConfirm(ctx, tool, digest, confirmToken)
		if err != nil {
			if relErr := g.releaseMailHold(err); relErr != nil {
				g.logMailHold(relErr)
			}

			return Decision{}, err
		}

		return Decision{}, nil
	}
	token, err := randomToken()
	if err != nil {
		return Decision{}, err
	}
	if g.persist != nil {
		err := g.persist.PutConfirm(ctx, Confirm{
			Token: token, SessionID: g.sessionID, Tool: tool,
			ArgsDigest: digest, CreatedAt: time.Now().UTC(),
		})
		if err != nil {
			return Decision{}, wrap("Authorize", err)
		}
	}
	ttlSec := int(ConfirmTTL / time.Second)

	return Decision{Required: map[string]any{
		"status": "confirmation_required", "tool": tool, "capability": capability,
		"will_do": preview, "confirm_token": token,
		"expires_in_seconds": ttlSec,
		"next_step":          fmt.Sprintf("Show 'will_do' to the user and get their explicit go-ahead, then call %s again with identical arguments plus confirm_token='%s'.", tool, token),
	}}, nil
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
	if g.persist != nil {
		if n, err := g.persist.CountMailCap(ctx, g.characterID); err == nil {
			mails = n
		}
		if n, err := g.persist.CountConfirm(ctx, g.sessionID); err == nil {
			pending = n
		}
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

func (g *Guard) appendRecord(ctx context.Context, rec Record) error {
	if g.persist == nil {
		return nil
	}
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
	if g.persist == nil {
		return nil
	}
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
	if g.persist == nil {
		return nil
	}
	hold, err := g.persist.HoldMailCap(ctx, g.characterID)
	if err != nil {
		return wrap("holdMailCap", err)
	}
	if hold.Count >= MailCap {
		if relErr := hold.Release(errMailCap); relErr != nil {
			g.logMailHold(relErr)
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

func (g *Guard) logMailHold(err error) {
	if g.logger == nil {
		return
	}
	g.logger.Error("write: mail-cap hold", "err", err)
}

func mailCapBlocked() BlockedError {
	return BlockedError{Msg: fmt.Sprintf("Mail budget exhausted: %d mails in the last hour. Wait until an earlier send drops out of the rolling hour, then try again.", MailCap)}
}

func (g *Guard) consumeConfirm(ctx context.Context, tool, digest, confirmToken string) error {
	if g.persist == nil {
		return BlockedError{Msg: errConfirmUnknown}
	}
	pending, err := g.persist.GetConfirm(ctx, confirmToken)
	if errors.Is(err, ErrConfirmNotFound) || pending == nil {
		return BlockedError{Msg: errConfirmUnknown}
	}
	if err != nil {
		return wrap("consumeConfirm", err)
	}
	if pending.SessionID != g.sessionID {
		return BlockedError{Msg: errConfirmUnknown}
	}
	if time.Since(pending.CreatedAt) > ConfirmTTL {
		if err := g.persist.DeleteConfirm(ctx, confirmToken); err != nil {
			return wrap("consumeConfirm", err)
		}

		return BlockedError{Msg: errConfirmUnknown}
	}
	if pending.Tool != tool {
		return BlockedError{Msg: fmt.Sprintf("confirm_token was issued for %q, not %q.", pending.Tool, tool)}
	}
	if pending.ArgsDigest != digest {
		if err := g.persist.DeleteConfirm(ctx, confirmToken); err != nil {
			return wrap("consumeConfirm", err)
		}

		return BlockedError{Msg: "The arguments changed since the preview was generated, so the token was discarded. Request a new preview and confirm that one."}
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
