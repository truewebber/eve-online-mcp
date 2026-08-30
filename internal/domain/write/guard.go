package write

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type Blocked struct{ Msg string }

func (e Blocked) Error() string { return e.Msg }

type Guard struct {
	opts         Options
	persist      Persist
	userID       string
	recentWrites []time.Time
	mu           sync.Mutex
}

func NewGuard(opts Options, persist Persist, userID string) *Guard {
	if opts.ConfirmTTLSeconds <= 0 {
		opts.ConfirmTTLSeconds = 300
	}
	if opts.MailBudgetPerHour <= 0 {
		opts.MailBudgetPerHour = 5
	}
	return &Guard{opts: opts, persist: persist, userID: userID}
}

func (g *Guard) CheckCapability(capability string) error {
	cap, ok := Capabilities[capability]
	if !ok {
		return Blocked{Msg: fmt.Sprintf("Unknown write capability %q.", capability)}
	}
	if g.opts.Mode == "off" {
		return Blocked{Msg: "Writes are disabled on this server (write_mode=off). Set write_mode=confirm in the config and restart."}
	}
	if !g.opts.CapabilityEnabled(capability) {
		enabled := g.opts.AllowedNames()
		sort.Strings(enabled)
		if len(enabled) == 0 {
			enabled = []string{"<none>"}
		}
		return Blocked{Msg: fmt.Sprintf("The %q capability is not enabled on this server (%s). Enabled capabilities: %s. Add it to write_allow and re-authorize the character to change this.", capability, cap.Summary, strings.Join(enabled, ", "))}
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
		return Blocked{Msg: fmt.Sprintf("This character was not authorized with %s. Log the character in again after enabling the capability.", strings.Join(missing, ", "))}
	}
	return nil
}

func (g *Guard) checkWriteBudget() error {
	now := time.Now()
	g.recentWrites = trim(g.recentWrites, now, time.Hour)
	if g.opts.WriteBudgetPerHour > 0 && len(g.recentWrites) >= g.opts.WriteBudgetPerHour {
		return Blocked{Msg: fmt.Sprintf("Write budget exhausted: %d writes in the last hour. This is a safety cap, not an ESI limit — wait, or raise write_budget_per_hour.", g.opts.WriteBudgetPerHour)}
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
	if n >= g.opts.MailBudgetPerHour {
		return Blocked{Msg: fmt.Sprintf("Mail budget exhausted: %d mails in the last hour. Wait until an earlier send drops out of the rolling hour, then try again.", g.opts.MailBudgetPerHour)}
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
	g.mu.Lock()
	err := g.checkWriteBudget()
	g.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if g.opts.Mode == "on" {
		return nil, nil
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
	g.Audit(map[string]any{"event": "preview", "tool": tool, "capability": capability, "preview": preview})
	return map[string]any{
		"status": "confirmation_required", "tool": tool, "capability": capability,
		"will_do": preview, "confirm_token": token,
		"expires_in_seconds": g.opts.ConfirmTTLSeconds,
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
	ttl := time.Duration(g.opts.ConfirmTTLSeconds) * time.Second
	if time.Since(pending.CreatedAt) > ttl {
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
	g.mu.Lock()
	g.recentWrites = append(g.recentWrites, time.Now())
	g.mu.Unlock()
	if capability == "mail_send" && g.persist != nil {
		if err := g.persist.InsertMail(ctx, g.userID, time.Now().UTC()); err != nil {
			log.Printf("could not record mail_log: %v", err)
		}
	}
	g.Audit(map[string]any{"event": "write", "tool": tool, "capability": capability, "args": args, "result": truncate(result)})
}

func (g *Guard) Audit(entry map[string]any) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.auditLocked(entry)
}

func (g *Guard) auditLocked(entry map[string]any) {
	record := map[string]any{"ts": time.Now().UTC().Format("2006-01-02T15:04:05Z")}
	for k, v := range entry {
		record[k] = v
	}
	raw, _ := json.Marshal(record)
	if g.opts.AuditFile == "" {
		return
	}
	f, err := os.OpenFile(g.opts.AuditFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		log.Printf("could not write audit log: %v", err)
		return
	}
	_, _ = f.Write(append(raw, '\n'))
	_ = f.Close()
}

func (g *Guard) Status(ctx context.Context) map[string]any {
	g.mu.Lock()
	now := time.Now()
	g.recentWrites = trim(g.recentWrites, now, time.Hour)
	writes := len(g.recentWrites)
	g.mu.Unlock()
	enabled := g.opts.AllowedNames()
	sort.Strings(enabled)
	disabled := []string{}
	for name := range Capabilities {
		if _, ok := g.opts.Allow[name]; !ok {
			disabled = append(disabled, name)
		}
	}
	sort.Strings(disabled)
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
	return map[string]any{
		"write_mode":            g.opts.Mode,
		"enabled_capabilities":  enabled,
		"disabled_capabilities": disabled,
		"capability_reference":  ref,
		"writes_last_hour":      writes,
		"write_budget_per_hour": g.opts.WriteBudgetPerHour,
		"mails_last_hour":       mails,
		"mail_budget_per_hour":  g.opts.MailBudgetPerHour,
		"pending_confirmations": pending,
		"audit_log":             g.opts.AuditFile,
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

func trim(bucket []time.Time, now time.Time, window time.Duration) []time.Time {
	cutoff := now.Add(-window)
	i := 0
	for i < len(bucket) && bucket[i].Before(cutoff) {
		i++
	}
	return bucket[i:]
}

func truncate(value any) any {
	raw, _ := json.Marshal(value)
	if len(raw) <= 500 {
		return value
	}
	return string(raw[:500]) + "…"
}
