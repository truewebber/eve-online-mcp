package oauth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"
)

var (
	errDialDetail = errors.New("dial tcp detail")
	errNoClient   = errors.New("no client")
	errPrepareSSO = errors.New("sso-prepare-failed")
)

func TestClassifyCallbackErrors(t *testing.T) {
	t.Parallel()
	if !errors.Is(unavailable(errDialDetail), ErrUnavailable) {
		t.Fatal("unavailable")
	}
	if !errors.Is(classifySSO(sso.ErrInvalidGrant), ErrClientMismatch) {
		t.Fatal("mismatch")
	}
	if !errors.Is(classifySSO(sso.Err("ccp body")), ErrLoginRefused) {
		t.Fatal("refused")
	}
	if strings.Contains(ErrUnknownLogin.Error(), "start the login") {
		t.Fatalf("sentinel is user-facing: %s", ErrUnknownLogin)
	}
}

func TestAuthorizePrepareLoginDoesNotLeak(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	ssoC := mocks.NewMockSSOClient(ctrl)
	ssoC.EXPECT().PrepareLogin(gomock.Any()).Return(nil, errPrepareSSO)
	s := mockServer(t, ctrl, ssoC)
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/oauth/authorize?client_id=c&redirect_uri=http://localhost:1/cb&code_challenge=x", nil)
	s.ServeAuthorize(rec, req)
	body, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d %s", rec.Code, body)
	}
	if strings.Contains(string(body), errPrepareSSO.Error()) {
		t.Fatalf("leaked: %s", body)
	}
	if !strings.Contains(string(body), `"error":"server_error"`) {
		t.Fatalf("want server_error: %s", body)
	}
}

func TestOAuthJSONErrorsHaveNoDescription(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	s := mockServer(t, ctrl, mocks.NewMockSSOClient(ctrl))
	posts := []struct {
		path string
		body string
	}{
		{"/oauth/token", "grant_type=nope"},
		{"/oauth/register", `{`},
	}
	for _, tc := range posts {
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if tc.path == "/oauth/register" {
			req.Header.Set("Content-Type", "application/json")
			s.ServeRegister(rec, req)
		} else {
			s.ServeToken(rec, req)
		}
		got := rec.Body.String()
		if strings.Contains(got, "error_description") {
			t.Fatalf("%s leaked description: %s", tc.path, got)
		}
		if !strings.Contains(got, `"error"`) {
			t.Fatalf("%s: %s", tc.path, got)
		}
	}
}

func mockServer(t *testing.T, ctrl *gomock.Controller, ssoC sso.Client) *Server {
	t.Helper()
	esiC := mocks.NewMockESIClient(ctrl)
	esiC.EXPECT().ForUser(gomock.Any()).Return(esiC).AnyTimes()
	clients := mocks.NewMockOauthclientRepository(ctrl)
	clients.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, errNoClient).AnyTimes()
	runtime, err := session.Open(session.Options{
		Mutations:  mocks.NewMockMutationRepository(ctrl),
		ESI:        esiC,
		SSO:        ssoC,
		Clients:    clients,
		Characters: mocks.NewMockCharacterRepository(ctrl),
		Sessions:   mocks.NewMockSessionRepository(ctrl),
		Logins:     mocks.NewMockLoginstateRepository(ctrl),
		Codes:      mocks.NewMockAuthcodeRepository(ctrl),
		Confirms:   mocks.NewMockConfirmRepository(ctrl),
		WithinTx:   func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) },
		Logger:     mocks.QuietLogger(ctrl),
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(testHost(), runtime, Options{HMACKey: []byte(testHMACKey)}, mocks.QuietLogger(ctrl))
	if err != nil {
		t.Fatal(err)
	}

	return s
}
