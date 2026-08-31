package write

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func testGuard(t *testing.T) (*Guard, *memPersist) {
	t.Helper()
	mem := newMemPersist()
	g := NewGuard(mem, "user-1")

	return g, mem
}

func TestAuthorizePreviewAndConfirm(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g, _ := testGuard(t)
	args := map[string]any{"destination": "Jita"}
	preview := map[string]any{"will_set": "Jita"}
	scopes := Capabilities()["waypoint"].Scopes

	out, err := g.Authorize(ctx, "eve_ui_set_waypoint", "waypoint", args, preview, "", scopes)
	if err != nil {
		t.Fatal(err)
	}
	if out.Required["status"] != "confirmation_required" || out.Required["will_do"] == nil || out.Required["confirm_token"] == "" {
		t.Fatalf("preview %+v", out)
	}
	token, ok := out.Required["confirm_token"].(string)
	if !ok || token == "" {
		t.Fatalf("preview %+v", out)
	}

	done, err := g.Authorize(ctx, "eve_ui_set_waypoint", "waypoint", args, preview, token, scopes)
	if err != nil || done.Required != nil {
		t.Fatalf("confirm %v %v", done, err)
	}
	_, err = g.Authorize(ctx, "eve_ui_set_waypoint", "waypoint", args, preview, token, scopes)
	if !errors.As(err, new(BlockedError)) {
		t.Fatalf("replay want BlockedError, got %v", err)
	}
}

func TestConfirmToolMismatchKeepsToken(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g, _ := testGuard(t)
	args := map[string]any{"destination": "Jita"}
	scopes := append([]string{}, Capabilities()["waypoint"].Scopes...)
	scopes = append(scopes, Capabilities()["mail_send"].Scopes...)
	out, err := g.Authorize(ctx, "eve_ui_set_waypoint", "waypoint", args, nil, "", Capabilities()["waypoint"].Scopes)
	if err != nil {
		t.Fatal(err)
	}
	token, ok := out.Required["confirm_token"].(string)
	if !ok || token == "" {
		t.Fatalf("preview %+v", out)
	}
	_, err = g.Authorize(ctx, "eve_mail_send", "mail_send", args, nil, token, scopes)
	var blocked BlockedError
	if !errors.As(err, &blocked) || !strings.Contains(blocked.Msg, "eve_ui_set_waypoint") {
		t.Fatalf("mismatch %v", err)
	}
	done, err := g.Authorize(ctx, "eve_ui_set_waypoint", "waypoint", args, nil, token, Capabilities()["waypoint"].Scopes)
	if err != nil || done.Required != nil {
		t.Fatalf("token should still work: %v %v", done, err)
	}
}

func TestConfirmDigestMismatchDiscards(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g, _ := testGuard(t)
	scopes := Capabilities()["waypoint"].Scopes
	out, err := g.Authorize(ctx, "eve_ui_set_waypoint", "waypoint", map[string]any{"d": "Jita"}, nil, "", scopes)
	if err != nil {
		t.Fatal(err)
	}
	token, ok := out.Required["confirm_token"].(string)
	if !ok || token == "" {
		t.Fatalf("preview %+v", out)
	}
	_, err = g.Authorize(ctx, "eve_ui_set_waypoint", "waypoint", map[string]any{"d": "Amarr"}, nil, token, scopes)
	var blocked BlockedError
	if !errors.As(err, &blocked) || !strings.Contains(blocked.Msg, "arguments changed") {
		t.Fatalf("digest %v", err)
	}
	_, err = g.Authorize(ctx, "eve_ui_set_waypoint", "waypoint", map[string]any{"d": "Jita"}, nil, token, scopes)
	if !errors.As(err, &blocked) {
		t.Fatalf("discarded token still worked: %v", err)
	}
}

func TestConfirmExpiry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g, mem := testGuard(t)
	args := map[string]any{"d": "Jita"}
	digest, err := digestArgs(args)
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.PutConfirm(ctx, Confirm{
		Token: "stale", UserID: "user-1", Tool: "eve_ui_set_waypoint",
		ArgsDigest: digest, CreatedAt: time.Now().Add(-10 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	_, err = g.Authorize(ctx, "eve_ui_set_waypoint", "waypoint", args, nil, "stale", Capabilities()["waypoint"].Scopes)
	var blocked BlockedError
	if !errors.As(err, &blocked) || !strings.Contains(blocked.Msg, "expired") {
		t.Fatalf("expiry %v", err)
	}
}

func TestSixthMailIsBlocked(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g, _ := testGuard(t)
	scopes := Capabilities()["mail_send"].Scopes
	for range 5 {
		g.Record(ctx, "eve_mail_send", "mail_send", nil, "ok")
	}
	_, err := g.Authorize(ctx, "eve_mail_send", "mail_send", nil, nil, "", scopes)
	var blocked BlockedError
	if !errors.As(err, &blocked) || !strings.Contains(blocked.Msg, "Mail budget exhausted") {
		t.Fatalf("sixth mail %v", err)
	}
	if !strings.Contains(blocked.Msg, "rolling hour") {
		t.Fatalf("want actionable message, got %q", blocked.Msg)
	}
}

func TestCheckCapabilityUnknownOnly(t *testing.T) {
	t.Parallel()
	g, _ := testGuard(t)
	if err := g.CheckCapability("waypoint"); err != nil {
		t.Fatalf("known capability: %v", err)
	}
	err := g.CheckCapability("teleport")
	var blocked BlockedError
	if !errors.As(err, &blocked) || !strings.Contains(blocked.Msg, "Unknown") {
		t.Fatalf("unknown %v", err)
	}
}

func TestStatusHasNoAuditOrBudget(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g, _ := testGuard(t)
	st := g.Status(ctx)
	for _, key := range []string{"write_mode", "disabled_capabilities", "write_budget_per_hour", "writes_last_hour"} {
		if _, ok := st[key]; ok {
			t.Fatalf("status still has %s: %+v", key, st)
		}
	}
	if st["mail_cap_per_hour"] != MailCap {
		t.Fatalf("mail cap %+v", st["mail_cap_per_hour"])
	}
	caps, ok := st["capabilities"].([]string)
	if !ok {
		t.Fatalf("capabilities %+v", st["capabilities"])
	}
	if len(caps) != len(Capabilities()) {
		t.Fatalf("capabilities %v", caps)
	}
}

func TestRequestedScopesIncludesCorpAndWrites(t *testing.T) {
	t.Parallel()
	scopes := RequestedScopes()
	want := map[string]struct{}{}
	for _, s := range ReadScopes() {
		want[s] = struct{}{}
	}
	for _, s := range CorpReadScopes() {
		want[s] = struct{}{}
	}
	for _, cap := range Capabilities() {
		for _, s := range cap.Scopes {
			want[s] = struct{}{}
		}
	}
	if len(scopes) != len(want) {
		t.Fatalf("got %d scopes, want %d", len(scopes), len(want))
	}
	for _, s := range scopes {
		if _, ok := want[s]; !ok {
			t.Fatalf("unexpected scope %s", s)
		}
	}
}
