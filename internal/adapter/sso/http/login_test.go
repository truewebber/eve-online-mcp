package http

import (
	"net/url"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
)

const testClientID = "cid"

func TestPrepareLoginAssemblesAuthorizeURL(t *testing.T) {
	t.Parallel()
	c := New(sso.Options{
		ClientID:    testClientID,
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
	if q.Get(formClientID) != testClientID {
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

func TestPrepareLoginEncodesCallback(t *testing.T) {
	t.Parallel()
	c := New(sso.Options{
		ClientID:    testClientID,
		CallbackURL: "http://127.0.0.1/cb?x=a&b=c d",
	}, nil, mocks.QuietLogger(gomock.NewController(t)))
	prep, err := c.PrepareLogin(nil)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(prep.URL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "https" || u.Host != ssoHost || u.Path != pathAuthorize {
		t.Fatalf("url %q", prep.URL)
	}
	if u.Query().Get("redirect_uri") != "http://127.0.0.1/cb?x=a&b=c d" {
		t.Fatalf("redirect_uri %q", u.Query().Get("redirect_uri"))
	}
}
