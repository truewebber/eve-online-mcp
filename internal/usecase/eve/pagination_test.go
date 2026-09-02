package eve

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/j"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
)

func TestMailListCursorRoundTrip(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	client := mocks.NewMockESIClient(ctrl)
	var lastParams map[string]any
	client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, path esi.Route, _ *int, params map[string]any, _ *float64) (esi.Result, error) {
			if !strings.HasSuffix(path.String(), "/mail") {
				return esi.Result{Data: []any{}}, nil
			}
			lastParams = params
			if j.Int(params[fLastMailID]) == 20 {
				return esi.Result{Data: []any{mailHeader(10, "old", "2026-01-01T00:00:00Z")}}, nil
			}

			return esi.Result{Data: []any{
				mailHeader(30, "new", "2026-03-01T00:00:00Z"),
				mailHeader(20, "mid", "2026-02-01T00:00:00Z"),
				mailHeader(15, "older", "2026-01-15T00:00:00Z"),
			}}, nil
		},
	).AnyTimes()
	client.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errWanted).AnyTimes()
	a := toolSession(t, client, false)
	first, err := eveMailList(t.Context(), a, mailListIn{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	body := asMap(t, first)
	cursor := j.Int(body[fNextCursor])
	if cursor != 20 || !j.Bool(body[fTruncated]) {
		t.Fatalf("first page %+v", body)
	}
	second, err := eveMailList(t.Context(), a, mailListIn{LastMailID: cursor})
	if err != nil {
		t.Fatal(err)
	}
	if j.Int(lastParams[fLastMailID]) != 20 {
		t.Fatalf("ESI cursor %+v", lastParams)
	}
	older := j.Maps(asMap(t, second)["mails"])
	if len(older) != 1 || j.Int(older[0][fMailID]) != 10 {
		t.Fatalf("second page %+v", older)
	}
}

func TestAssetsBlueprintsPageIsOneGet(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	client := mocks.NewMockESIClient(ctrl)
	var gets int
	var asked any
	pages := 3
	client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, path esi.Route, _ *int, params map[string]any, _ *float64) (esi.Result, error) {
			if !strings.Contains(path.String(), "blueprints") {
				return esi.Result{Data: []any{}}, nil
			}
			gets++
			asked = params[fPage]

			return esi.Result{Data: []any{
				map[string]any{fTypeID: 34, "location_id": 60003760, "runs": -1,
					fMaterialEfficiency: 10, fTimeEfficiency: 20, fQuantity: 1},
			}, Pages: &pages}, nil
		},
	).AnyTimes()
	client.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errWanted).AnyTimes()
	a := toolSession(t, client, false)
	got, err := eveAssetsBlueprints(t.Context(), a, assetsBlueprintsIn{Page: 2})
	if err != nil {
		t.Fatal(err)
	}
	if gets != 1 {
		t.Fatalf("ESI gets %d", gets)
	}
	if j.Int(asked) != 2 {
		t.Fatalf("asked page %v", asked)
	}
	body := asMap(t, got)
	if j.Int(body[fPage]) != 2 || j.Int(body[fTotalPages]) != 3 {
		t.Fatalf("%+v", body)
	}
}

func TestAssetsListOffsetWalk(t *testing.T) {
	t.Parallel()
	a := fixtureSession(t)
	first, err := eveAssetsList(t.Context(), a, assetsListIn{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	body := asMap(t, first)
	total := j.Int(body[fTotal])
	if total < 1 {
		t.Fatalf("total %+v", body)
	}
	second, err := eveAssetsList(t.Context(), a, assetsListIn{Limit: 10, Offset: total})
	if err != nil {
		t.Fatal(err)
	}
	again := asMap(t, second)
	if j.Int(again[fTotal]) != total {
		t.Fatalf("total drifted %d → %d", total, j.Int(again[fTotal]))
	}
	if len(j.Maps(again[fLocations])) != 0 {
		t.Fatalf("offset past end %+v", again[fLocations])
	}
}

func TestCalendarListTruncatedIsLabelled(t *testing.T) {
	t.Parallel()
	a := fixtureSession(t)
	got, err := eveCalendarList(t.Context(), a, calendarListIn{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	body := asMap(t, got)
	if !j.Bool(body[fTruncated]) || j.Str(body["how_to_see_more"]) == "" {
		t.Fatalf("unlabelled %+v", body)
	}
	if j.Int(body[fNextCursor]) == 0 {
		t.Fatal("missing next_cursor")
	}
}

func TestClass4ToolHasNoPagingParam(t *testing.T) {
	t.Parallel()
	got := toolMaps(t, listedTools(t))["eve_character_skills"]
	props := asMap(t, asMap(t, got["inputSchema"])["properties"])
	for _, name := range []string{fPage, fOffset, fLastMailID, fFromEvent} {
		if _, ok := props[name]; ok {
			t.Fatalf("class-4 tool has %s", name)
		}
	}
}

func mailHeader(id int, subject, timestamp string) map[string]any {
	return map[string]any{
		fMailID: id, fFrom: 1, fSubject: subject,
		fTimestamp: timestamp, "is_read": false, "labels": []any{},
	}
}
