package http

import (
	nhttp "net/http"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
)

func testSSOOptions() sso.Options {
	return sso.Options{
		ClientID:    testClientID,
		CallbackURL: testCallbackURL,
		UserAgent:   testUserAgent,
	}
}

func mustSSO(t *testing.T, opts sso.Options, hc *nhttp.Client) *Client {
	t.Helper()
	c, err := New(opts, hc, mocks.QuietLogger(gomock.NewController(t)))
	if err != nil {
		t.Fatal(err)
	}

	return c
}
