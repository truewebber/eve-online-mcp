package tests

import (
	"context"
	"fmt"
	nhttp "net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/gopkg/log"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	esihttp "github.com/truewebber/eve-online-mcp/internal/adapter/esi/http"
	"github.com/truewebber/eve-online-mcp/internal/adapter/esi/http/esitest"
	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	ssohttp "github.com/truewebber/eve-online-mcp/internal/adapter/sso/http"
	authcodepgx "github.com/truewebber/eve-online-mcp/internal/domain/authcode/pgx"
	"github.com/truewebber/eve-online-mcp/internal/domain/character"
	characterpgx "github.com/truewebber/eve-online-mcp/internal/domain/character/pgx"
	confirmpgx "github.com/truewebber/eve-online-mcp/internal/domain/confirm/pgx"
	loginstatepgx "github.com/truewebber/eve-online-mcp/internal/domain/loginstate/pgx"
	mutationpgx "github.com/truewebber/eve-online-mcp/internal/domain/mutation/pgx"
	oauthclientpgx "github.com/truewebber/eve-online-mcp/internal/domain/oauthclient/pgx"
	dbsession "github.com/truewebber/eve-online-mcp/internal/domain/session"
	sessionpgx "github.com/truewebber/eve-online-mcp/internal/domain/session/pgx"
	"github.com/truewebber/eve-online-mcp/internal/domain/write"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
	"github.com/truewebber/eve-online-mcp/internal/observe"
	"github.com/truewebber/eve-online-mcp/internal/postgres"
	"github.com/truewebber/eve-online-mcp/internal/postgres/pgtest"
	httpsvc "github.com/truewebber/eve-online-mcp/internal/service/http"
	svcmcp "github.com/truewebber/eve-online-mcp/internal/service/mcp"
	"github.com/truewebber/eve-online-mcp/internal/usecase/oauth"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	testHMACKey      = "0123456789abcdef0123456789abcdef"
	testAccessToken  = "fixture-access"
	testRefreshToken = "fixture-refresh"
	testVersion      = "test"
	testUserAgent    = "eve-mcp-tests"
	fixtureName      = "Fixture Pilot"
)

type env struct {
	server *httptest.Server
	client *mcp.ClientSession
	token  string
}

func openEnv(t *testing.T) *env {
	t.Helper()
	logger := mocks.QuietLogger(gomock.NewController(t))
	db := openThrowaway(t, logger)
	tr, err := esitest.Load()
	if err != nil {
		t.Fatal(err)
	}
	httpClient := &nhttp.Client{Transport: &esitest.Fallback{Inner: tr}}
	hs := httptest.NewUnstartedServer(nhttp.NotFoundHandler())
	baseURL := url.URL{Scheme: "http", Host: hs.Listener.Addr().String()}
	host := oauth.Host{
		PublicURL:   baseURL.String(),
		MCPPath:     "/mcp",
		CallbackURL: baseURL.JoinPath("auth", "callback").String(),
	}
	runtime, oauthServer := wire(t, wireIn{db: db, host: host, httpClient: httpClient, logger: logger})
	characterID, sessionID := seedCharacter(t, runtime)
	token, err := oauthServer.IssueAccess(characterID, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	hs.Config.Handler = mcpMux(t, oauthServer, host, logger)
	hs.Start()
	t.Cleanup(hs.Close)
	sess := connect(t, hs.URL+"/mcp", token)

	return &env{server: hs, client: sess, token: token}
}

func openThrowaway(t *testing.T, logger log.Logger) *postgres.DB {
	t.Helper()
	dsn := pgtest.EmptyDatabase(t)
	ctx := t.Context()
	if err := pgtest.Apply(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	db, err := postgres.Open(ctx, dsn, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	return db
}

type wireIn struct {
	db         *postgres.DB
	host       oauth.Host
	httpClient *nhttp.Client
	logger     log.Logger
}

func wire(t *testing.T, in wireIn) (*session.Session, *oauth.Server) {
	t.Helper()
	pool := in.db.Pool()
	esiClient, err := esihttp.New(esi.Options{
		BaseURL:    esi.DefaultBaseURL,
		UserAgent:  testUserAgent,
		CompatDate: esitest.CompatDate,
		Observe:    observe.New(),
	}, in.httpClient, in.logger)
	if err != nil {
		t.Fatal(err)
	}
	ssoClient, err := ssohttp.New(sso.Options{
		ClientID:    "test-eve-client",
		CallbackURL: in.host.CallbackURL,
		UserAgent:   testUserAgent,
		Scopes:      write.RequestedScopes(),
	}, in.httpClient, in.logger)
	if err != nil {
		t.Fatal(err)
	}
	opts := session.Options{
		Characters: characterpgx.New(pool),
		Sessions:   sessionpgx.New(pool),
		Clients:    oauthclientpgx.New(pool),
		Logins:     loginstatepgx.New(pool),
		Codes:      authcodepgx.New(pool),
		Confirms:   confirmpgx.New(pool),
		Mutations:  mutationpgx.New(pool),
		WithinTx: func(ctx context.Context, fn func(context.Context) error) error {
			return postgres.WithinTx(ctx, pool, fn)
		},
		ESI:    esiClient,
		Logger: in.logger,
	}
	ssoMock := mocks.NewMockSSOClient(gomock.NewController(t))
	ssoMock.EXPECT().PrepareLogin(gomock.Any()).DoAndReturn(ssoClient.PrepareLogin).AnyTimes()
	ssoMock.EXPECT().AccessToken(gomock.Any(), gomock.Any()).Return(&sso.CharacterToken{
		CharacterID:     esitest.FixtureCharacterID,
		CharacterName:   fixtureName,
		RefreshToken:    testRefreshToken,
		Scopes:          write.RequestedScopes(),
		AccessToken:     testAccessToken,
		AccessExpiresAt: time.Now().Add(time.Hour),
	}, nil).AnyTimes()
	ssoMock.EXPECT().Revoke(gomock.Any(), gomock.Any()).AnyTimes()
	ssoMock.EXPECT().ExchangeCode(gomock.Any(), gomock.Any(), gomock.Any()).Return(&sso.CharacterToken{
		CharacterID: esitest.FixtureCharacterID, CharacterName: fixtureName,
		RefreshToken: testRefreshToken, Scopes: write.RequestedScopes(),
	}, nil).AnyTimes()
	opts.SSO = ssoMock
	runtime, err := session.Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	oauthServer, err := oauth.Open(in.host, runtime, oauth.Options{HMACKey: []byte(testHMACKey)}, in.logger)
	if err != nil {
		t.Fatal(err)
	}

	return runtime, oauthServer
}

func seedCharacter(t *testing.T, runtime *session.Session) (int, int64) {
	t.Helper()
	ctx := t.Context()
	id := esitest.FixtureCharacterID
	if err := runtime.Characters.Upsert(ctx, character.Character{
		ID:   int64(id),
		Name: fixtureName,
	}); err != nil {
		t.Fatal(err)
	}
	row, err := runtime.Sessions.Create(ctx, dbsession.Session{
		CharacterID:  int64(id),
		RefreshToken: testRefreshToken,
		Scopes:       write.RequestedScopes(),
		MCPClientID:  "test",
	})
	if err != nil {
		t.Fatal(err)
	}

	return id, row.ID
}

func mcpMux(t *testing.T, oauthServer *oauth.Server, host oauth.Host, logger log.Logger) nhttp.Handler {
	t.Helper()
	h, err := httpsvc.New(oauthServer, host, logger)
	if err != nil {
		t.Fatal(err)
	}
	mux := nhttp.NewServeMux()
	h.Mount(mux)
	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name: "eve-online", Title: "EVE Online", Version: testVersion,
	}, &mcp.ServerOptions{Instructions: svcmcp.Instructions()})
	svcmcp.Register(mcpServer)
	stream := mcp.NewStreamableHTTPHandler(func(*nhttp.Request) *mcp.Server {
		return mcpServer
	}, &mcp.StreamableHTTPOptions{
		Stateless:                  true,
		DisableLocalhostProtection: true,
	})
	protected := oauthServer.ProtectMCP(stream)
	mux.Handle("/mcp", protected)
	mux.Handle("/mcp/", protected)

	return mux
}

func connect(t *testing.T, endpoint, token string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: testUserAgent, Version: testVersion}, nil)
	sess, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:             endpoint,
		HTTPClient:           &nhttp.Client{Transport: bearerRoundTrip{token: token}},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })

	return sess
}

type bearerRoundTrip struct {
	token string
	base  nhttp.RoundTripper
}

func (b bearerRoundTrip) RoundTrip(req *nhttp.Request) (*nhttp.Response, error) {
	next := b.base
	if next == nil {
		next = nhttp.DefaultTransport
	}
	clone := req.Clone(req.Context())
	if b.token != "" {
		clone.Header.Set("Authorization", "Bearer "+b.token)
	}

	resp, err := next.RoundTrip(clone)
	if err != nil {
		return nil, wrap("round trip", err)
	}

	return resp, nil
}

func wrap(op string, err error) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("tests: %s: %w", op, err)
}

func toolText(result *mcp.CallToolResult) string {
	if result == nil {
		return ""
	}
	var b strings.Builder
	for _, c := range result.Content {
		if text, ok := c.(*mcp.TextContent); ok {
			b.WriteString(text.Text)
		}
	}

	return b.String()
}
