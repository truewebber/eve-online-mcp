package http

import (
	"net/url"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
)

func TestPrepareLoginAssemblesAuthorizeURL(t *testing.T) {
	t.Parallel()
	c := New(sso.Options{
		ClientID:    "cid",
		CallbackURL: "http://127.0.0.1/auth/callback",
		Scopes:      []string{"esi-wallet.read_character_wallet.v1"},
	}, nil, mocks.QuietLogger(gomock.NewController(t)))
	prep, err := c.PrepareLogin(nil)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(prep.URL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "https" || u.Host != "login.eveonline.com" || u.Path != "/v2/oauth/authorize" {
		t.Fatalf("url %q", prep.URL)
	}
	q := u.Query()
	if q.Get(formClientID) != "cid" {
		t.Fatalf("client_id %q", q.Get(formClientID))
	}
	if q.Get("redirect_uri") != "http://127.0.0.1/auth/callback" {
		t.Fatalf("redirect_uri %q", q.Get("redirect_uri"))
	}
	if q.Get("response_type") != "code" {
		t.Fatalf("response_type %q", q.Get("response_type"))
	}
	if q.Get("state") == "" || q.Get("code_challenge") == "" {
		t.Fatalf("missing pkce fields in %q", u.RawQuery)
	}
}
