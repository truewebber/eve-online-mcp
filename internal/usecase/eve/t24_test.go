package eve

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/domain/confirm"
	"github.com/truewebber/eve-online-mcp/internal/domain/mutation"
	"github.com/truewebber/eve-online-mcp/internal/j"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
)

const testRecipient = "Tritanium"

func TestCalendarListCursorRoundTrip(t *testing.T) {
	t.Parallel()
	a := fixtureSession(t)
	first, err := eveCalendarList(t.Context(), a, calendarListIn{Limit: calendarESIPage})
	if err != nil {
		t.Fatal(err)
	}
	body := asMap(t, first)
	events := j.Maps(body[fEvents])
	if len(events) != calendarESIPage {
		t.Fatalf("first page %d events", len(events))
	}
	cursor := j.Int(body[fNextCursor])
	if cursor != 9050 {
		t.Fatalf("next_cursor %v", body[fNextCursor])
	}
	second, err := eveCalendarList(t.Context(), a, calendarListIn{FromEvent: cursor})
	if err != nil {
		t.Fatal(err)
	}
	older := j.Maps(asMap(t, second)[fEvents])
	if len(older) != 1 || j.Int(older[0][fEventID]) != 8001 {
		t.Fatalf("second page %+v", older)
	}
}

func TestCalendarListAttendeesOneGetPerEvent(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	client := mocks.NewMockESIClient(ctrl)
	var paths []string
	client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, path string, _ *int, _ map[string]any, _ *float64) (esi.Result, error) {
			paths = append(paths, path)
			if strings.HasSuffix(path, "/calendar") {
				return esi.Result{Data: []any{
					map[string]any{fEventID: 1, fTitle: "A", fEventDate: "2026-09-01T00:00:00Z", "event_response": vAccepted, "importance": 0},
					map[string]any{fEventID: 2, fTitle: "B", fEventDate: "2026-09-02T00:00:00Z", "event_response": vNotResponded, "importance": 0},
				}}, nil
			}

			return esi.Result{Data: []any{}}, nil
		},
	).AnyTimes()
	a := toolSession(t, client, false)
	if _, err := eveCalendarList(t.Context(), a, calendarListIn{}); err != nil {
		t.Fatal(err)
	}
	if attendeeGets(paths) != 0 {
		t.Fatalf("unasked attendees: %v", paths)
	}
	paths = nil
	attendees := true
	if _, err := eveCalendarList(t.Context(), a, calendarListIn{Attendees: &attendees}); err != nil {
		t.Fatal(err)
	}
	if attendeeGets(paths) != 2 {
		t.Fatalf("attendee gets %d paths %v", attendeeGets(paths), paths)
	}
}

func TestMailComposePostsNewmailNotMail(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	client := mocks.NewMockESIClient(ctrl)
	var newmail any
	client.EXPECT().Post(gomock.Any(), "/universe/ids", gomock.Any(), gomock.Any(), gomock.Any()).Return(map[string]any{
		fCharacters: []any{map[string]any{"id": 243070982, fName: testRecipient}},
	}, nil).AnyTimes()
	client.EXPECT().Post(gomock.Any(), "/ui/openwindow/newmail", gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ string, _ *int, _ map[string]any, body any) (any, error) {
			newmail = body

			return true, nil
		},
	)
	a := toolSession(t, client, false)
	confs, ok := a.Confirms.(*mocks.MockConfirmRepository)
	if !ok {
		t.Fatal("confirms")
	}
	var stored confirm.Confirm
	confs.EXPECT().Put(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, c confirm.Confirm) error {
		stored = c

		return nil
	})
	preview, err := eveMailCompose(t.Context(), a, mailComposeIn{
		To: []string{testRecipient}, Subject: "hi", Body: "there",
	})
	if err != nil {
		t.Fatal(err)
	}
	token := j.Str(asMap(t, preview)["confirm_token"])
	if token == "" {
		t.Fatalf("no token: %+v", preview)
	}
	confs.EXPECT().Get(gomock.Any(), token).Return(&stored, nil)
	confs.EXPECT().Delete(gomock.Any(), token).Return(nil)
	muts, ok := a.Mutations.(*mocks.MockMutationRepository)
	if !ok {
		t.Fatal("mutations")
	}
	muts.EXPECT().Append(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, m mutation.Mutation) error {
		if m.Tool != "eve_mail_compose" || m.Capability != "openwindow" {
			t.Fatalf("mutation %+v", m)
		}

		return nil
	})
	got, err := eveMailCompose(t.Context(), a, mailComposeIn{
		To: []string{testRecipient}, Subject: "hi", Body: "there", ConfirmToken: token,
	})
	if err != nil {
		t.Fatal(err)
	}
	out := asMap(t, got)
	if out[fStatus] != vDone {
		t.Fatalf("%+v", out)
	}
	body := j.Map(newmail)
	ids, ok := body[fRecipients].([]int)
	if !ok || len(ids) != 1 || ids[0] != 243070982 {
		t.Fatalf("newmail body %+v", newmail)
	}
}

func TestMailSendCSPAExceedsMintsNoToken(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	client := mocks.NewMockESIClient(ctrl)
	client.EXPECT().Post(gomock.Any(), "/universe/ids", gomock.Any(), gomock.Any(), gomock.Any()).Return(map[string]any{
		fCharacters: []any{map[string]any{"id": 243070982, fName: testRecipient}},
	}, nil)
	client.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, path string, _ *int, _ map[string]any, _ any) (any, error) {
			if strings.HasSuffix(path, "/cspa") {
				return 10000.0, nil
			}
			t.Fatalf("unexpected post %s", path)

			return 0, errWanted
		},
	)
	a := toolSession(t, client, false)
	_, err := eveMailSend(t.Context(), a, mailSendIn{To: []string{testRecipient}, Subject: "x", Body: "y"})
	var exceeds CSPAExceedsError
	if !errors.As(err, &exceeds) || exceeds.Cost != 10000 {
		t.Fatalf("%v", err)
	}
}

func TestMailSendCSPAUnpricedMintsNoToken(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	client := mocks.NewMockESIClient(ctrl)
	client.EXPECT().Post(gomock.Any(), "/universe/ids", gomock.Any(), gomock.Any(), gomock.Any()).Return(map[string]any{
		fCharacters: []any{map[string]any{"id": 243070982, fName: testRecipient}},
	}, nil)
	client.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, path string, _ *int, _ map[string]any, _ any) (any, error) {
			if strings.HasSuffix(path, "/cspa") {
				return []any{}, nil
			}
			t.Fatalf("unexpected post %s", path)

			return 0, errWanted
		},
	)
	a := toolSession(t, client, false)
	_, err := eveMailSend(t.Context(), a, mailSendIn{To: []string{testRecipient}, Subject: "x", Body: "y", ApprovedCost: 1})
	if !errors.Is(err, ErrCSPAUnpriced) {
		t.Fatalf("%v", err)
	}
}

func TestOpenWindowNewmailIsError(t *testing.T) {
	t.Parallel()
	_, err := eveUIOpenWindow(t.Context(), nil, openWindowIn{Window: "newmail", Target: "1"})
	var v ValidationError
	if !errors.As(err, &v) {
		t.Fatalf("%v", err)
	}
	if v.Field != fWindow || !strings.Contains(v.Invariant, windowMarket) || !strings.Contains(v.Invariant, windowInfo) || !strings.Contains(v.Invariant, windowContract) {
		t.Fatalf("%+v", v)
	}
	if strings.Contains(v.Invariant, "newmail") {
		t.Fatalf("named the refused value: %+v", v)
	}
}

func TestEnumsRefuseUnknown(t *testing.T) {
	t.Parallel()
	cases := []struct {
		field string
		err   error
	}{
		{"kind", mustErr(pickEnum(fKind, "ledger", fJournal, fJournal, fTransactions, vBoth))},
		{"response_format", rejectUnknownFormat("pretty")},
		{fResponse, mustErr(requireEnum(fResponse, "maybe", vAccepted, vDeclined, vTentative))},
		{fieldPreference, mustErr(requireEnum(fieldPreference, "fastest", vShorter, "safer", "less_secure"))},
	}
	for _, tc := range cases {
		var v ValidationError
		if !errors.As(tc.err, &v) || v.Field != tc.field {
			t.Fatalf("%s: %v", tc.field, tc.err)
		}
		if !strings.Contains(v.Invariant, "must be one of") {
			t.Fatalf("%s invariant %q", tc.field, v.Invariant)
		}
	}
}

func TestCSPAChargeReadsDocumentedNumber(t *testing.T) {
	t.Parallel()
	got, ok := cspaCharge(2950.0)
	if !ok || got != 2950 {
		t.Fatalf("%v %v", got, ok)
	}
	if _, ok := cspaCharge(nil); ok {
		t.Fatal("nil priced")
	}
	if _, ok := cspaCharge([]any{}); ok {
		t.Fatal("empty array priced")
	}
	if _, ok := cspaCharge(map[string]any{"cost": 1}); ok {
		t.Fatal("object priced")
	}
}

func attendeeGets(paths []string) int {
	n := 0
	for _, p := range paths {
		if strings.HasSuffix(p, "/attendees") {
			n++
		}
	}

	return n
}

func asMap(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}

	return out
}

var errWanted = errors.New("want error")

func mustErr(_ any, err error) error {
	if err != nil {
		return err
	}

	return errWanted
}
