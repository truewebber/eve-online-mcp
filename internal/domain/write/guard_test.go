package write_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/domain/write"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
)

const testDestination = "Jita"

type confirmBox struct {
	tokens map[string]write.Confirm
	mail   []mailAt
}

type mailAt struct {
	characterID int64
	at          time.Time
}

func testGuard(t *testing.T) (*write.Guard, *confirmBox) {
	t.Helper()
	ctrl := gomock.NewController(t)
	box := &confirmBox{tokens: map[string]write.Confirm{}}
	persist := mocks.NewMockWritePersist(ctrl)
	persist.EXPECT().PutConfirm(gomock.Any(), gomock.Any()).DoAndReturn(box.put).AnyTimes()
	persist.EXPECT().GetConfirm(gomock.Any(), gomock.Any()).DoAndReturn(box.get).AnyTimes()
	persist.EXPECT().DeleteConfirm(gomock.Any(), gomock.Any()).DoAndReturn(box.drop).AnyTimes()
	persist.EXPECT().CountConfirm(gomock.Any(), gomock.Any()).DoAndReturn(box.countConfirm).AnyTimes()
	persist.EXPECT().CountMailSince(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(box.countMail).AnyTimes()
	persist.EXPECT().InsertMail(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(box.insertMail).AnyTimes()
	g := write.NewGuard(persist, 1, 1, mocks.QuietLogger(ctrl))

	return g, box
}

func (b *confirmBox) put(_ context.Context, c write.Confirm) error {
	b.tokens[c.Token] = c

	return nil
}

func (b *confirmBox) get(_ context.Context, token string) (*write.Confirm, error) {
	c, ok := b.tokens[token]
	if !ok {
		return nil, write.ErrConfirmNotFound
	}
	cp := c

	return &cp, nil
}

func (b *confirmBox) drop(_ context.Context, token string) error {
	delete(b.tokens, token)

	return nil
}

func (b *confirmBox) countConfirm(_ context.Context, sessionID int64) (int, error) {
	n := 0
	for _, c := range b.tokens {
		if c.SessionID == sessionID {
			n++
		}
	}

	return n, nil
}

func (b *confirmBox) countMail(_ context.Context, characterID int64, since time.Time) (int, error) {
	n := 0
	for _, row := range b.mail {
		if row.characterID == characterID && !row.at.Before(since) {
			n++
		}
	}

	return n, nil
}

func (b *confirmBox) insertMail(_ context.Context, characterID int64, at time.Time) error {
	b.mail = append(b.mail, mailAt{characterID: characterID, at: at})

	return nil
}

func TestAuthorizePreviewAndConfirm(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g, _ := testGuard(t)
	args := map[string]any{"destination": testDestination}
	preview := map[string]any{"will_set": testDestination}
	scopes := write.Capabilities()["waypoint"].Scopes

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
	if !errors.As(err, new(write.BlockedError)) {
		t.Fatalf("replay want BlockedError, got %v", err)
	}
}

func TestConfirmToolMismatchKeepsToken(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g, _ := testGuard(t)
	args := map[string]any{"destination": testDestination}
	scopes := append([]string{}, write.Capabilities()["waypoint"].Scopes...)
	scopes = append(scopes, write.Capabilities()[write.CapMailSend].Scopes...)
	out, err := g.Authorize(ctx, "eve_ui_set_waypoint", "waypoint", args, nil, "", write.Capabilities()["waypoint"].Scopes)
	if err != nil {
		t.Fatal(err)
	}
	token, ok := out.Required["confirm_token"].(string)
	if !ok || token == "" {
		t.Fatalf("preview %+v", out)
	}
	_, err = g.Authorize(ctx, "eve_mail_send", write.CapMailSend, args, nil, token, scopes)
	var blocked write.BlockedError
	if !errors.As(err, &blocked) || !strings.Contains(blocked.Msg, "eve_ui_set_waypoint") {
		t.Fatalf("mismatch %v", err)
	}
	done, err := g.Authorize(ctx, "eve_ui_set_waypoint", "waypoint", args, nil, token, write.Capabilities()["waypoint"].Scopes)
	if err != nil || done.Required != nil {
		t.Fatalf("token should still work: %v %v", done, err)
	}
}

func TestConfirmDigestMismatchDiscards(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g, _ := testGuard(t)
	scopes := write.Capabilities()["waypoint"].Scopes
	out, err := g.Authorize(ctx, "eve_ui_set_waypoint", "waypoint", map[string]any{"d": testDestination}, nil, "", scopes)
	if err != nil {
		t.Fatal(err)
	}
	token, ok := out.Required["confirm_token"].(string)
	if !ok || token == "" {
		t.Fatalf("preview %+v", out)
	}
	_, err = g.Authorize(ctx, "eve_ui_set_waypoint", "waypoint", map[string]any{"d": "Amarr"}, nil, token, scopes)
	var blocked write.BlockedError
	if !errors.As(err, &blocked) || !strings.Contains(blocked.Msg, "arguments changed") {
		t.Fatalf("digest %v", err)
	}
	_, err = g.Authorize(ctx, "eve_ui_set_waypoint", "waypoint", map[string]any{"d": testDestination}, nil, token, scopes)
	if !errors.As(err, &blocked) {
		t.Fatalf("discarded token still worked: %v", err)
	}
}

func TestConfirmExpiry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g, box := testGuard(t)
	args := map[string]any{"d": testDestination}
	out, err := g.Authorize(ctx, "eve_ui_set_waypoint", "waypoint", args, nil, "", write.Capabilities()["waypoint"].Scopes)
	if err != nil {
		t.Fatal(err)
	}
	token, ok := out.Required["confirm_token"].(string)
	if !ok || token == "" {
		t.Fatalf("preview %+v", out)
	}
	stored := box.tokens[token]
	stored.CreatedAt = time.Now().Add(-10 * time.Minute)
	box.tokens[token] = stored
	_, err = g.Authorize(ctx, "eve_ui_set_waypoint", "waypoint", args, nil, token, write.Capabilities()["waypoint"].Scopes)
	var blocked write.BlockedError
	if !errors.As(err, &blocked) || !strings.Contains(blocked.Msg, "expired") {
		t.Fatalf("expiry %v", err)
	}
}

func TestSixthMailIsBlocked(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g, _ := testGuard(t)
	scopes := write.Capabilities()[write.CapMailSend].Scopes
	for range 5 {
		g.Record(ctx, "eve_mail_send", write.CapMailSend, nil, "ok")
	}
	_, err := g.Authorize(ctx, "eve_mail_send", write.CapMailSend, nil, nil, "", scopes)
	var blocked write.BlockedError
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
	var blocked write.BlockedError
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
	if st["mail_cap_per_hour"] != write.MailCap {
		t.Fatalf("mail cap %+v", st["mail_cap_per_hour"])
	}
	caps, ok := st["capabilities"].([]string)
	if !ok {
		t.Fatalf("capabilities %+v", st["capabilities"])
	}
	if len(caps) != len(write.Capabilities()) {
		t.Fatalf("capabilities %v", caps)
	}
}

func TestRequestedScopesIncludesCorpAndWrites(t *testing.T) {
	t.Parallel()
	scopes := write.RequestedScopes()
	want := map[string]struct{}{}
	for _, s := range write.ReadScopes() {
		want[s] = struct{}{}
	}
	for _, s := range write.CorpReadScopes() {
		want[s] = struct{}{}
	}
	for _, cap := range write.Capabilities() {
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
