package eve

import (
	"context"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
)

func TestKillmailHashIsNotARouteLabel(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	client := mocks.NewMockESIClient(ctrl)
	const hash = "2cc55ac01ceb43dd78fe73bec8c593073781360a"
	client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, path esi.Route, _ *int, _ map[string]any, _ *float64) (esi.Result, error) {
			if path.Pattern() != "/killmails/{id}/{id}" {
				t.Fatalf("pattern %q", path.Pattern())
			}
			if path.String() != "/killmails/138049254/"+hash {
				t.Fatalf("raw %q", path.String())
			}

			return esi.Result{Data: map[string]any{"killmail_id": 138049254}}, nil
		},
	)
	got := fetchKillmailBodies(t.Context(), toolSession(t, client, false), []map[string]any{
		{"killmail_id": 138049254, "killmail_hash": hash},
	})
	if len(got.failed) != 0 || len(got.kills) != 1 {
		t.Fatalf("%+v", got)
	}
}
