package write

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func testGuard(t *testing.T, mode string) (*Guard, *memPersist) {
	t.Helper()
	mem := newMemPersist()
	g := NewGuard(Options{
		Mode:               mode,
		Allow:              map[string]struct{}{"waypoint": {}, "mail_send": {}},
		WriteBudgetPerHour: 40,
		MailBudgetPerHour:  5,
		ConfirmTTLSeconds:  300,
	}, mem, "user-1")
	return g, mem
}

func TestAuthorizePreviewAndConfirm(t *testing.T) {
	ctx := context.Background()
	g, _ := testGuard(t, "confirm")
	args := map[string]any{"destination": "Jita"}
	preview := map[string]any{"will_set": "Jita"}
	scopes := Capabilities["waypoint"].Scopes

	out, err := g.Authorize(ctx, "eve_ui_set_waypoint", "waypoint", args, preview, "", scopes)
	if err != nil {
		t.Fatal(err)
	}
	if out["status"] != "confirmation_required" || out["will_do"] == nil || out["confirm_token"] == "" {
		t.Fatalf("preview %+v", out)
	}
	token, _ := out["confirm_token"].(string)

	done, err := g.Authorize(ctx, "eve_ui_set_waypoint", "waypoint", args, preview, token, scopes)
	if err != nil || done != nil {
		t.Fatalf("confirm %v %v", done, err)
	}
	_, err = g.Authorize(ctx, "eve_ui_set_waypoint", "waypoint", args, preview, token, scopes)
	var blocked Blocked
	if !errors.As(err, &blocked) {
		t.Fatalf("replay want Blocked, got %v", err)
	}
}

func TestConfirmToolMismatchKeepsToken(t *testing.T) {
	ctx := context.Background()
	g, _ := testGuard(t, "confirm")
	args := map[string]any{"destination": "Jita"}
	scopes := append([]string{}, Capabilities["waypoint"].Scopes...)
	scopes = append(scopes, Capabilities["mail_send"].Scopes...)
	out, err := g.Authorize(ctx, "eve_ui_set_waypoint", "waypoint", args, nil, "", Capabilities["waypoint"].Scopes)
	if err != nil {
		t.Fatal(err)
	}
	token, _ := out["confirm_token"].(string)
	_, err = g.Authorize(ctx, "eve_mail_send", "mail_send", args, nil, token, scopes)
	var blocked Blocked
	if !errors.As(err, &blocked) || !strings.Contains(blocked.Msg, "eve_ui_set_waypoint") {
		t.Fatalf("mismatch %v", err)
	}
	done, err := g.Authorize(ctx, "eve_ui_set_waypoint", "waypoint", args, nil, token, Capabilities["waypoint"].Scopes)
	if err != nil || done != nil {
		t.Fatalf("token should still work: %v %v", done, err)
	}
}

func TestConfirmDigestMismatchDiscards(t *testing.T) {
	ctx := context.Background()
	g, _ := testGuard(t, "confirm")
	scopes := Capabilities["waypoint"].Scopes
	out, err := g.Authorize(ctx, "eve_ui_set_waypoint", "waypoint", map[string]any{"d": "Jita"}, nil, "", scopes)
	if err != nil {
		t.Fatal(err)
	}
	token, _ := out["confirm_token"].(string)
	_, err = g.Authorize(ctx, "eve_ui_set_waypoint", "waypoint", map[string]any{"d": "Amarr"}, nil, token, scopes)
	var blocked Blocked
	if !errors.As(err, &blocked) || !strings.Contains(blocked.Msg, "arguments changed") {
		t.Fatalf("digest %v", err)
	}
	_, err = g.Authorize(ctx, "eve_ui_set_waypoint", "waypoint", map[string]any{"d": "Jita"}, nil, token, scopes)
	if !errors.As(err, &blocked) {
		t.Fatalf("discarded token still worked: %v", err)
	}
}

func TestConfirmExpiry(t *testing.T) {
	ctx := context.Background()
	g, mem := testGuard(t, "confirm")
	args := map[string]any{"d": "Jita"}
	if err := mem.PutConfirm(ctx, Confirm{
		Token: "stale", UserID: "user-1", Tool: "eve_ui_set_waypoint",
		ArgsDigest: digestArgs(args), CreatedAt: time.Now().Add(-10 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	_, err := g.Authorize(ctx, "eve_ui_set_waypoint", "waypoint", args, nil, "stale", Capabilities["waypoint"].Scopes)
	var blocked Blocked
	if !errors.As(err, &blocked) || !strings.Contains(blocked.Msg, "expired") {
		t.Fatalf("expiry %v", err)
	}
}

func TestSixthMailIsBlocked(t *testing.T) {
	ctx := context.Background()
	g, _ := testGuard(t, "on")
	scopes := Capabilities["mail_send"].Scopes
	for i := 0; i < 5; i++ {
		if _, err := g.Authorize(ctx, "eve_mail_send", "mail_send", nil, nil, "", scopes); err != nil {
			t.Fatal(err)
		}
		g.Record(ctx, "eve_mail_send", "mail_send", nil, "ok")
	}
	_, err := g.Authorize(ctx, "eve_mail_send", "mail_send", nil, nil, "", scopes)
	var blocked Blocked
	if !errors.As(err, &blocked) || !strings.Contains(blocked.Msg, "Mail budget exhausted") {
		t.Fatalf("sixth mail %v", err)
	}
	if !strings.Contains(blocked.Msg, "rolling hour") {
		t.Fatalf("want actionable message, got %q", blocked.Msg)
	}
}
