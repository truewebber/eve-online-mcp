package http

import (
	nhttp "net/http"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
	"github.com/truewebber/eve-online-mcp/internal/observe"
)

func testOptions(baseURL string) esi.Options {
	return esi.Options{
		BaseURL:    baseURL,
		CompatDate: testCompatDate,
		UserAgent:  testUserAgent,
		Observe:    observe.New(),
	}
}

func mustClient(t *testing.T, opts esi.Options, hc *nhttp.Client) *Client {
	t.Helper()
	c, err := New(opts, hc, mocks.QuietLogger(gomock.NewController(t)))
	if err != nil {
		t.Fatal(err)
	}

	return c
}
