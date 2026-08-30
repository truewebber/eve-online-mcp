package write

import (
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

type pendingWrite struct {
	token      string
	tool       string
	capability string
	argsDigest string
	preview    map[string]any
	createdAt  time.Time
}

type Guard struct {
	opts         Options
	pending      map[string]pendingWrite
	recentWrites []time.Time
	recentMail   []time.Time
	mu           sync.Mutex
}

func NewGuard(opts Options) *Guard {
	return &Guard{opts: opts, pending: map[string]pendingWrite{}}
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

func (g *Guard) checkBudget(capability string) error {
	now := time.Now()
	g.recentWrites = trim(g.recentWrites, now, time.Hour)
	if len(g.recentWrites) >= g.opts.WriteBudgetPerHour {
		return Blocked{Msg: fmt.Sprintf("Write budget exhausted: %d writes in the last hour. This is a safety cap, not an ESI limit — wait, or raise write_budget_per_hour.", g.opts.WriteBudgetPerHour)}
	}
	if capability == "mail_send" {
		g.recentMail = trim(g.recentMail, now, time.Hour)
		if len(g.recentMail) >= g.opts.MailBudgetPerHour {
			return Blocked{Msg: fmt.Sprintf("Mail budget exhausted: %d mails in the last hour.", g.opts.MailBudgetPerHour)}
		}
	}
	return nil
}

func (g *Guard) Authorize(tool, capability string, args map[string]any, preview map[string]any, confirmToken string, granted []string) (map[string]any, error) {
	if err := g.CheckCapability(capability); err != nil {
		return nil, err
	}
	if err := g.CheckScope(capability, granted); err != nil {
		return nil, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.checkBudget(capability); err != nil {
		return nil, err
	}
	g.expirePending()
	digest := digestArgs(args)
	if g.opts.Mode == "on" {
		return nil, nil
	}
	if confirmToken != "" {
		pending, ok := g.pending[confirmToken]
		if !ok {
			return nil, Blocked{Msg: "That confirm_token is unknown or has expired. Call the tool again without a token to get a fresh preview."}
		}
		if pending.tool != tool {
			return nil, Blocked{Msg: fmt.Sprintf("confirm_token was issued for %q, not %q.", pending.tool, tool)}
		}
		if pending.argsDigest != digest {
			delete(g.pending, confirmToken)
			return nil, Blocked{Msg: "The arguments changed since the preview was generated, so the token was discarded. Request a new preview and confirm that one."}
		}
		delete(g.pending, confirmToken)
		return nil, nil
	}
	token := randomToken()
	g.pending[token] = pendingWrite{
		token: token, tool: tool, capability: capability,
		argsDigest: digest, preview: preview, createdAt: time.Now(),
	}
	g.auditLocked(map[string]any{"event": "preview", "tool": tool, "capability": capability, "preview": preview})
	return map[string]any{
		"status": "confirmation_required", "tool": tool, "capability": capability,
		"will_do": preview, "confirm_token": token,
		"expires_in_seconds": g.opts.ConfirmTTLSeconds,
		"next_step":          fmt.Sprintf("Show 'will_do' to the user and get their explicit go-ahead, then call %s again with identical arguments plus confirm_token='%s'.", tool, token),
	}, nil
}

func (g *Guard) Record(tool, capability string, args map[string]any, result any) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	g.recentWrites = append(g.recentWrites, now)
	if capability == "mail_send" {
		g.recentMail = append(g.recentMail, now)
	}
	g.auditLocked(map[string]any{"event": "write", "tool": tool, "capability": capability, "args": args, "result": truncate(result)})
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

func (g *Guard) expirePending() {
	cutoff := time.Now().Add(-time.Duration(g.opts.ConfirmTTLSeconds) * time.Second)
	for k, p := range g.pending {
		if p.createdAt.Before(cutoff) {
			delete(g.pending, k)
		}
	}
}

func (g *Guard) Status() map[string]any {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	g.recentWrites = trim(g.recentWrites, now, time.Hour)
	g.recentMail = trim(g.recentMail, now, time.Hour)
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
	return map[string]any{
		"write_mode":            g.opts.Mode,
		"enabled_capabilities":  enabled,
		"disabled_capabilities": disabled,
		"capability_reference":  ref,
		"writes_last_hour":      len(g.recentWrites),
		"write_budget_per_hour": g.opts.WriteBudgetPerHour,
		"mails_last_hour":       len(g.recentMail),
		"mail_budget_per_hour":  g.opts.MailBudgetPerHour,
		"pending_confirmations": len(g.pending),
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
