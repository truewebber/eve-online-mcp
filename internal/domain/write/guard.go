package write

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
)

type Blocked struct{ Msg string }

func (e Blocked) Error() string { return e.Msg }

type Guard struct {
	persist Persist
	userID  string
}

func NewGuard(persist Persist, userID string) *Guard {
	return &Guard{persist: persist, userID: userID}
}

func (g *Guard) CheckCapability(capability string) error {
	if _, ok := Capabilities[capability]; !ok {
		return Blocked{Msg: fmt.Sprintf("Unknown write capability %q.", capability)}
	}
	return nil
}

func (g *Guard) CheckScope(capability string, granted []string) error {
	cap := Capabilities[capability]
	have := map[string]struct{}{}
	for _, s := range granted {
		have[s] = struct{}{}
	}
	var missing []string
	for _, s := range cap.Scopes {
		if _, ok := have[s]; !ok {
			missing = append(missing, s)
		}
	}
	if len(missing) > 0 {
		return Blocked{Msg: fmt.Sprintf("This character was not authorized with %s. Re-run the login for this character with eve_auth_login_url.", strings.Join(missing, ", "))}
	}
	return nil
}

func (g *Guard) checkMailCap(ctx context.Context) error {
	if g.persist == nil {
		return nil
	}
	n, err := g.persist.CountMailSince(ctx, g.userID, time.Now().Add(-time.Hour))
	if err != nil {
		return err
	}
	if n >= MailCap {
		return Blocked{Msg: fmt.Sprintf("Mail budget exhausted: %d mails in the last hour. Wait until an earlier send drops out of the rolling hour, then try again.", MailCap)}
	}
	return nil
}

func (g *Guard) Authorize(ctx context.Context, tool, capability string, args map[string]any, preview map[string]any, confirmToken string, granted []string) (map[string]any, error) {
	if err := g.CheckCapability(capability); err != nil {
		return nil, err
	}
	if err := g.CheckScope(capability, granted); err != nil {
		return nil, err
	}
	if capability == "mail_send" {
		if err := g.checkMailCap(ctx); err != nil {
			return nil, err
		}
	}
	digest := digestArgs(args)
	if confirmToken != "" {
		if err := g.consumeConfirm(ctx, tool, digest, confirmToken); err != nil {
			return nil, err
		}
		return nil, nil
	}
	token := randomToken()
	if g.persist != nil {
		if err := g.persist.PutConfirm(ctx, Confirm{
			Token: token, UserID: g.userID, Tool: tool,
			ArgsDigest: digest, CreatedAt: time.Now().UTC(),
		}); err != nil {
			return nil, err
		}
	}
	ttlSec := int(ConfirmTTL / time.Second)
	return map[string]any{
		"status": "confirmation_required", "tool": tool, "capability": capability,
		"will_do": preview, "confirm_token": token,
		"expires_in_seconds": ttlSec,
		"next_step":          fmt.Sprintf("Show 'will_do' to the user and get their explicit go-ahead, then call %s again with identical arguments plus confirm_token='%s'.", tool, token),
	}, nil
}

func (g *Guard) consumeConfirm(ctx context.Context, tool, digest, confirmToken string) error {
	if g.persist == nil {
		return Blocked{Msg: "That confirm_token is unknown or has expired. Call the tool again without a token to get a fresh preview."}
	}
	pending, ok, err := g.persist.GetConfirm(ctx, confirmToken)
	if err != nil {
		return err
	}
	if !ok || pending.UserID != g.userID {
		return Blocked{Msg: "That confirm_token is unknown or has expired. Call the tool again without a token to get a fresh preview."}
	}
	if time.Since(pending.CreatedAt) > ConfirmTTL {
		_ = g.persist.DeleteConfirm(ctx, confirmToken)
		return Blocked{Msg: "That confirm_token is unknown or has expired. Call the tool again without a token to get a fresh preview."}
	}
	if pending.Tool != tool {
		return Blocked{Msg: fmt.Sprintf("confirm_token was issued for %q, not %q.", pending.Tool, tool)}
	}
	if pending.ArgsDigest != digest {
		_ = g.persist.DeleteConfirm(ctx, confirmToken)
		return Blocked{Msg: "The arguments changed since the preview was generated, so the token was discarded. Request a new preview and confirm that one."}
	}
	_ = g.persist.DeleteConfirm(ctx, confirmToken)
	return nil
}

func (g *Guard) Record(ctx context.Context, tool, capability string, args map[string]any, result any) {
	if capability == "mail_send" && g.persist != nil {
		if err := g.persist.InsertMail(ctx, g.userID, time.Now().UTC()); err != nil {
			log.Printf("could not record mail_log: %v", err)
		}
	}
}

func (g *Guard) Status(ctx context.Context) map[string]any {
	now := time.Now()
	ref := map[string]string{}
	for name, cap := range Capabilities {
		ref[name] = cap.Summary
	}
	mails, pending := 0, 0
	if g.persist != nil {
		if n, err := g.persist.CountMailSince(ctx, g.userID, now.Add(-time.Hour)); err == nil {
			mails = n
		}
		if n, err := g.persist.CountConfirm(ctx, g.userID); err == nil {
			pending = n
		}
	}
	remaining := MailCap - mails
	if remaining < 0 {
		remaining = 0
	}
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

func digestArgs(payload any) string {
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])[:16]
}

func randomToken() string {
	var b [9]byte
	_, _ = rand.Read(b[:])
	return strings.TrimRight(hex.EncodeToString(b[:]), "=")
}
