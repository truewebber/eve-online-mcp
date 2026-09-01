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

func authz(tool, capability string, args, preview map[string]any, token string, scopes []string) write.Authz {
	return write.Authz{Tool: tool, Capability: capability, Args: args, Preview: preview, Token: token, Scopes: scopes}
}

type confirmBox struct {
	tokens map[string]write.Confirm
	rows   []write.Mutation
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
	persist.EXPECT().CountMailCap(gomock.Any(), gomock.Any()).DoAndReturn(box.countMail).AnyTimes()
	persist.EXPECT().AppendMutation(gomock.Any(), gomock.Any()).DoAndReturn(box.append).AnyTimes()
	persist.EXPECT().HoldMailCap(gomock.Any(), gomock.Any()).DoAndReturn(box.hold).AnyTimes()
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

func (b *confirmBox) countMail(_ context.Context, characterID int64) (int, error) {
	n := 0
	for _, row := range b.rows {
		if row.CharacterID == characterID && row.Tool == write.ToolMailSend && row.Outcome == write.OutcomeOK {
			n++
		}
	}

	return n, nil
}

func (b *confirmBox) append(_ context.Context, m write.Mutation) error {
	b.rows = append(b.rows, m)

	return nil
}

func (b *confirmBox) hold(ctx context.Context, characterID int64) (*write.MailCapHold, error) {
	n, err := b.countMail(ctx, characterID)

	return write.NewMailCapHold(n, func(fn func(context.Context) error) error {
		return fn(ctx)
	}, func(error) error { return nil }), err
}

func recordMail(t *testing.T, g *write.Guard, args map[string]any) {
	t.Helper()
	if err := g.Record(context.Background(), write.Record{
		Tool: write.ToolMailSend, Capability: write.CapMailSend, Args: args, Outcome: write.OutcomeOK,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAuthorizePreviewAndConfirm(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g, _ := testGuard(t)
	args := map[string]any{"destination": testDestination}
	preview := map[string]any{"will_set": testDestination}
	scopes := write.Capabilities()["waypoint"].Scopes

	out, err := g.Authorize(ctx, authz("eve_ui_set_waypoint", "waypoint", args, preview, "", scopes))
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

	done, err := g.Authorize(ctx, authz("eve_ui_set_waypoint", "waypoint", args, preview, token, scopes))
	if err != nil || done.Required != nil {
		t.Fatalf("confirm %v %v", done, err)
	}
	_, err = g.Authorize(ctx, authz("eve_ui_set_waypoint", "waypoint", args, preview, token, scopes))
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
	out, err := g.Authorize(ctx, authz("eve_ui_set_waypoint", "waypoint", args, nil, "", write.Capabilities()["waypoint"].Scopes))
	if err != nil {
		t.Fatal(err)
	}
	token, ok := out.Required["confirm_token"].(string)
	if !ok || token == "" {
		t.Fatalf("preview %+v", out)
	}
	_, err = g.Authorize(ctx, authz("eve_mail_send", write.CapMailSend, args, nil, token, scopes))
	var blocked write.BlockedError
	if !errors.As(err, &blocked) || !strings.Contains(blocked.Msg, "eve_ui_set_waypoint") {
		t.Fatalf("mismatch %v", err)
	}
	done, err := g.Authorize(ctx, authz("eve_ui_set_waypoint", "waypoint", args, nil, token, write.Capabilities()["waypoint"].Scopes))
	if err != nil || done.Required != nil {
		t.Fatalf("token should still work: %v %v", done, err)
	}
}

func TestConfirmDigestMismatchDiscards(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g, _ := testGuard(t)
	scopes := write.Capabilities()["waypoint"].Scopes
	out, err := g.Authorize(ctx, authz("eve_ui_set_waypoint", "waypoint", map[string]any{"d": testDestination}, nil, "", scopes))
	if err != nil {
		t.Fatal(err)
	}
	token, ok := out.Required["confirm_token"].(string)
	if !ok || token == "" {
		t.Fatalf("preview %+v", out)
	}
	_, err = g.Authorize(ctx, authz("eve_ui_set_waypoint", "waypoint", map[string]any{"d": "Amarr"}, nil, token, scopes))
	var blocked write.BlockedError
	if !errors.As(err, &blocked) || !strings.Contains(blocked.Msg, "arguments changed") {
		t.Fatalf("digest %v", err)
	}
	_, err = g.Authorize(ctx, authz("eve_ui_set_waypoint", "waypoint", map[string]any{"d": testDestination}, nil, token, scopes))
	if !errors.As(err, &blocked) {
		t.Fatalf("discarded token still worked: %v", err)
	}
}

func TestConfirmExpiry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g, box := testGuard(t)
	args := map[string]any{"d": testDestination}
	out, err := g.Authorize(ctx, authz("eve_ui_set_waypoint", "waypoint", args, nil, "", write.Capabilities()["waypoint"].Scopes))
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
	_, err = g.Authorize(ctx, authz("eve_ui_set_waypoint", "waypoint", args, nil, token, write.Capabilities()["waypoint"].Scopes))
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
	before := write.MailCapRejections.Load()
	for range 5 {
		recordMail(t, g, nil)
	}
	_, err := g.Authorize(ctx, authz("eve_mail_send", write.CapMailSend, nil, nil, "", scopes))
	var blocked write.BlockedError
	if !errors.As(err, &blocked) || !strings.Contains(blocked.Msg, "Mail budget exhausted") {
		t.Fatalf("sixth mail %v", err)
	}
	if !strings.Contains(blocked.Msg, "rolling hour") {
		t.Fatalf("want actionable message, got %q", blocked.Msg)
	}
	if write.MailCapRejections.Load() != before+1 {
		t.Fatalf("rejections %d want %d", write.MailCapRejections.Load(), before+1)
	}
}

func TestPreviewDoesNotRecord(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g, box := testGuard(t)
	scopes := write.Capabilities()["waypoint"].Scopes
	_, err := g.Authorize(ctx, authz("eve_ui_set_waypoint", "waypoint", map[string]any{"d": testDestination}, nil, "", scopes))
	if err != nil {
		t.Fatal(err)
	}
	if len(box.rows) != 0 {
		t.Fatalf("preview recorded %+v", box.rows)
	}
}

func TestRecordFailedESI(t *testing.T) {
	t.Parallel()
	g, box := testGuard(t)
	if err := g.Record(context.Background(), write.Record{
		Tool: write.ToolMailSend, Capability: write.CapMailSend,
		Args:    map[string]any{"subject": "Fleet tonight", "body": "secret body", "recipients": []any{1, 2}},
		Outcome: write.OutcomeError, ESIStatus: 520, Error: "ESI 520 on /mail",
	}); err != nil {
		t.Fatal(err)
	}
	if len(box.rows) != 1 {
		t.Fatalf("rows %d", len(box.rows))
	}
	row := box.rows[0]
	if row.Outcome != write.OutcomeError || row.ESIStatus != 520 {
		t.Fatalf("row %+v", row)
	}
	if strings.Contains(row.Summary, "secret body") || strings.Contains(row.Error, "secret body") {
		t.Fatalf("body leaked %+v", row)
	}
	n, err := box.countMail(context.Background(), 1)
	if err != nil || n != 0 {
		t.Fatalf("error counted toward cap %d %v", n, err)
	}
}

func TestRecordOmitsMailBody(t *testing.T) {
	t.Parallel()
	g, box := testGuard(t)
	recordMail(t, g, map[string]any{
		"subject": "Fleet tonight", "body": "do not store this body",
		"recipients": []any{map[string]any{"id": 1}, map[string]any{"id": 2}},
	})
	if len(box.rows) != 1 {
		t.Fatalf("rows %d", len(box.rows))
	}
	row := box.rows[0]
	if !strings.Contains(row.Summary, "mail to 2 recipients") || !strings.Contains(row.Summary, "Fleet tonight") {
		t.Fatalf("summary %q", row.Summary)
	}
	if strings.Contains(row.Summary, "do not store this body") {
		t.Fatalf("body in summary %q", row.Summary)
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

func TestMissingScopesSortedDifference(t *testing.T) {
	t.Parallel()
	if got := write.MissingScopes(write.RequestedScopes()); len(got) != 0 {
		t.Fatalf("full grant %v", got)
	}
	got := write.MissingScopes(nil)
	if len(got) != len(write.RequestedScopes()) {
		t.Fatalf("empty grant %d want %d", len(got), len(write.RequestedScopes()))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] >= got[i] {
			t.Fatalf("unsorted %v", got)
		}
	}
	short := append([]string{}, write.RequestedScopes()...)
	drop := short[len(short)-1]
	short = short[:len(short)-1]
	got = write.MissingScopes(short)
	if len(got) != 1 || got[0] != drop {
		t.Fatalf("one missing %v want %s", got, drop)
	}
}
