package httpsvc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/gopkg/log"

	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	"github.com/truewebber/eve-online-mcp/internal/domain/character"
	"github.com/truewebber/eve-online-mcp/internal/domain/loginstate"
	"github.com/truewebber/eve-online-mcp/internal/domain/write"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
	"github.com/truewebber/eve-online-mcp/internal/usecase/oauth"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"
)

const testHMACKey = "0123456789abcdef0123456789abcdef"

var (
	errDialRefused = errors.New("dial tcp 127.0.0.1:5432: connect: connection refused")
	errUnexpected  = errors.New("pgx: unexpected")
	errCCPBody     = errors.New("ccp-alert-payload")
)

func TestCallbackCCPErrorIsStatic(t *testing.T) {
	t.Parallel()
	payload := "<script>alert(1)</script>"
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/auth/callback?error=access_denied&error_description="+payload, nil)
	(&API{Logger: mocks.QuietLogger(gomock.NewController(t))}).GetAuthCallback(rec, req, GetAuthCallbackParams{})
	assertStaticPage(t, rec, pageRefused)
	if strings.Contains(rec.Body.String(), payload) || strings.Contains(rec.Body.String(), "access_denied") {
		t.Fatalf("ccp string rendered: %s", rec.Body.String())
	}
	for k, vs := range rec.Result().Header {
		for _, v := range vs {
			if strings.Contains(v, payload) {
				t.Fatalf("ccp string in header %s: %s", k, v)
			}
		}
	}
}

func TestCallbackMissingCodeIsBadCallback(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/callback?state=st", nil)
	(&API{Logger: mocks.QuietLogger(gomock.NewController(t))}).GetAuthCallback(rec, req, GetAuthCallbackParams{})
	assertStaticPage(t, rec, pageBadCallback)
}

func TestCallbackUnknownLogin(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	logins := mocks.NewMockLoginstateRepository(ctrl)
	logins.EXPECT().Take(gomock.Any(), "st").Return(nil, loginstate.ErrNotFound)
	h := callbackAPI(t, mocks.QuietLogger(ctrl), logins, mocks.NewMockSSOClient(ctrl), mocks.NewMockCharacterRepository(ctrl), mocks.NewMockAuthcodeRepository(ctrl))
	rec := callCallback(t, h)
	assertStaticPage(t, rec, pageUnknownLogin)
}

func TestCallbackDatabaseFailureIsGenericAndLogged(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	logger := mocks.NewMockLogger(ctrl)
	logger.EXPECT().Error("http: callback", "err", gomock.Any(), "title", pageUnavailable.title).
		Do(func(_ string, args ...any) {
			if !strings.Contains(fmt.Sprint(args...), "connection refused") {
				t.Fatalf("real error missing: %v", args)
			}
		})
	logins := mocks.NewMockLoginstateRepository(ctrl)
	logins.EXPECT().Take(gomock.Any(), "st").Return(nil, errDialRefused)
	h := callbackAPI(t, logger, logins, mocks.NewMockSSOClient(ctrl), mocks.NewMockCharacterRepository(ctrl), mocks.NewMockAuthcodeRepository(ctrl))
	rec := callCallback(t, h)
	assertStaticPage(t, rec, pageUnavailable)
	if strings.Contains(rec.Body.String(), "connection refused") || strings.Contains(rec.Body.String(), errDialRefused.Error()) {
		t.Fatalf("inner error rendered: %s", rec.Body.String())
	}
}

func TestCallbackClientMismatch(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	logins := mocks.NewMockLoginstateRepository(ctrl)
	logins.EXPECT().Take(gomock.Any(), "st").Return(&loginstate.Login{PKCEVerifier: "v"}, nil)
	ssoC := mocks.NewMockSSOClient(ctrl)
	ssoC.EXPECT().ExchangeCode(gomock.Any(), "x", "v").Return(nil, sso.ErrInvalidGrant)
	h := callbackAPI(t, mocks.QuietLogger(ctrl), logins, ssoC, mocks.NewMockCharacterRepository(ctrl), mocks.NewMockAuthcodeRepository(ctrl))
	rec := callCallback(t, h)
	assertStaticPage(t, rec, pageMismatch)
	if strings.Contains(rec.Body.String(), sso.ErrInvalidGrant.Error()) {
		t.Fatalf("inner error rendered: %s", rec.Body.String())
	}
}

func TestCallbackLoginRefused(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	logins := mocks.NewMockLoginstateRepository(ctrl)
	logins.EXPECT().Take(gomock.Any(), "st").Return(&loginstate.Login{PKCEVerifier: "v"}, nil)
	ssoC := mocks.NewMockSSOClient(ctrl)
	ssoC.EXPECT().ExchangeCode(gomock.Any(), "x", "v").Return(nil, sso.Err(errCCPBody.Error()))
	h := callbackAPI(t, mocks.QuietLogger(ctrl), logins, ssoC, mocks.NewMockCharacterRepository(ctrl), mocks.NewMockAuthcodeRepository(ctrl))
	rec := callCallback(t, h)
	assertStaticPage(t, rec, pageRefused)
	if strings.Contains(rec.Body.String(), errCCPBody.Error()) {
		t.Fatalf("ccp string rendered: %s", rec.Body.String())
	}
}

func TestCallbackShortGrant(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	logins := mocks.NewMockLoginstateRepository(ctrl)
	logins.EXPECT().Take(gomock.Any(), "st").Return(&loginstate.Login{PKCEVerifier: "v"}, nil)
	ssoC := mocks.NewMockSSOClient(ctrl)
	ssoC.EXPECT().ExchangeCode(gomock.Any(), "x", "v").Return(&sso.CharacterToken{
		CharacterID: 1, CharacterName: "Pilot",
	}, nil)
	h := callbackAPI(t, mocks.QuietLogger(ctrl), logins, ssoC, mocks.NewMockCharacterRepository(ctrl), mocks.NewMockAuthcodeRepository(ctrl))
	rec := callCallback(t, h)
	assertStaticPage(t, rec, pageShortGrant)
	if !strings.Contains(rec.Body.String(), "esi-") {
		t.Fatalf("missing identifiers: %s", rec.Body.String())
	}
}

func TestCallbackGenericOnBadRedirect(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	logins := mocks.NewMockLoginstateRepository(ctrl)
	logins.EXPECT().Take(gomock.Any(), "st").Return(&loginstate.Login{
		PKCEVerifier: "v", RedirectURI: "://bad",
	}, nil)
	ssoC := mocks.NewMockSSOClient(ctrl)
	ssoC.EXPECT().ExchangeCode(gomock.Any(), "x", "v").Return(&sso.CharacterToken{
		CharacterID: 1, CharacterName: "Pilot", OwnerHash: "h",
		Scopes: write.RequestedScopes(),
	}, nil)
	chars := mocks.NewMockCharacterRepository(ctrl)
	chars.EXPECT().Get(gomock.Any(), int64(1)).Return(nil, character.ErrNotFound)
	chars.EXPECT().Upsert(gomock.Any(), gomock.Any()).Return(nil)
	codes := mocks.NewMockAuthcodeRepository(ctrl)
	codes.EXPECT().Put(gomock.Any(), gomock.Any()).Return(nil)
	h := callbackAPI(t, mocks.QuietLogger(ctrl), logins, ssoC, chars, codes)
	rec := callCallback(t, h)
	assertStaticPage(t, rec, pageGeneric)
}

func TestLookupCoversCatalog(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err   error
		entry pageErr
	}{
		{oauth.ErrUnknownLogin, pageUnknownLogin},
		{oauth.ErrClientMismatch, pageMismatch},
		{oauth.ErrLoginRefused, pageRefused},
		{oauth.ErrUnavailable, pageUnavailable},
		{errUnexpected, pageGeneric},
	}
	for _, tc := range cases {
		if got := lookup(tc.err); got != tc.entry {
			t.Fatalf("%v: %+v want %+v", tc.err, got, tc.entry)
		}
	}
}

func assertStaticPage(t *testing.T, rec *httptest.ResponseRecorder, entry pageErr) {
	t.Helper()
	if rec.Code != entry.status {
		t.Fatalf("status %d want %d body %s", rec.Code, entry.status, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, entry.sentence) {
		t.Fatalf("sentence missing: %s", body)
	}
	if loc := rec.Header().Get("Location"); loc != "" && strings.Contains(loc, "error") {
		t.Fatalf("location %s", loc)
	}
}

func callCallback(t *testing.T, h *API) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/callback?code=x&state=st", nil)
	h.GetAuthCallback(rec, req, GetAuthCallbackParams{})

	return rec
}

func callbackAPI(
	t *testing.T,
	logger log.Logger,
	logins *mocks.MockLoginstateRepository,
	ssoClient sso.Client,
	chars *mocks.MockCharacterRepository,
	codes *mocks.MockAuthcodeRepository,
) *API {
	t.Helper()
	ctrl := gomock.NewController(t)
	esiC := mocks.NewMockESIClient(ctrl)
	esiC.EXPECT().ForUser(gomock.Any()).Return(esiC).AnyTimes()
	host := oauth.Host{
		PublicURL:   "http://127.0.0.1:8765",
		MCPPath:     "/mcp",
		CallbackURL: "http://127.0.0.1:8765/auth/callback",
	}
	runtime, err := session.Open(session.Options{
		Mutations:  mocks.NewMockMutationRepository(ctrl),
		ESI:        esiC,
		SSO:        ssoClient,
		Logins:     logins,
		Characters: chars,
		Codes:      codes,
		Sessions:   mocks.NewMockSessionRepository(ctrl),
		Clients:    mocks.NewMockOauthclientRepository(ctrl),
		Confirms:   mocks.NewMockConfirmRepository(ctrl),
		WithinTx:   func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) },
		Logger:     mocks.QuietLogger(ctrl),
	})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := oauth.Open(host, runtime, oauth.Options{HMACKey: []byte(testHMACKey)}, mocks.QuietLogger(ctrl))
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(srv, host, logger)
	if err != nil {
		t.Fatal(err)
	}

	return h
}

func TestIndexLinksToGitHub(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	h := callbackAPI(t, mocks.QuietLogger(ctrl), mocks.NewMockLoginstateRepository(ctrl),
		mocks.NewMockSSOClient(ctrl), mocks.NewMockCharacterRepository(ctrl), mocks.NewMockAuthcodeRepository(ctrl))
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	h.GetIndex(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, githubRepoURL) {
		t.Fatalf("missing github link: %s", body)
	}
	if strings.Contains(body, "EVE callback") {
		t.Fatalf("operator callback leaked onto the index: %s", body)
	}
}
