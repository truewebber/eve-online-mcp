package eve

import (
	"encoding/json"
	nhttp "net/http"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	esihttp "github.com/truewebber/eve-online-mcp/internal/adapter/esi/http"
	"github.com/truewebber/eve-online-mcp/internal/adapter/esi/http/esitest"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"
)

func TestServerStatusAgainstFixtures(t *testing.T) {
	t.Parallel()
	tr, err := esitest.Load()
	if err != nil {
		t.Fatal(err)
	}
	client := esihttp.New(esi.Options{
		BaseURL:    esi.DefaultBaseURL,
		CompatDate: esitest.CompatDate,
		UserAgent:  "eve-mcp-test",
	}, &nhttp.Client{Transport: tr}, mocks.QuietLogger(gomock.NewController(t)))
	got, err := eveServerStatus(t.Context(), &session.Session{ESI: client}, empty{})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("json %s: %v", raw, err)
	}
	for _, key := range []string{"players", "server_version", "data_age"} {
		if _, ok := body[key]; !ok {
			t.Fatalf("missing %s: %s", key, raw)
		}
	}
}
