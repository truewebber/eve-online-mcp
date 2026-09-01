package tests

import (
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
	"github.com/truewebber/eve-online-mcp/internal/domain/write"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
	"github.com/truewebber/eve-online-mcp/internal/postgres"
	"github.com/truewebber/eve-online-mcp/internal/postgres/pgtest"
	httpsvc "github.com/truewebber/eve-online-mcp/internal/service/http"
	svcmcp "github.com/truewebber/eve-online-mcp/internal/service/mcp"
	"github.com/truewebber/eve-online-mcp/internal/usecase/oauth"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	testHMACKey     = "0123456789abcdef0123456789abcdef"
	testAccessToken = "fixture-access"
	testVersion     = "test"
	testUserAgent   = "eve-mcp-tests"
	fixtureName     = "Fixture Pilot"
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
	runtime, oauthServer := wire(t, db, host, httpClient, logger)
	characterID := seedCharacter(t, runtime, oauthServer)
	token, err := oauthServer.IssueAccess(characterID)
	if err != nil {
		t.Fatal(err)
	}
	hs.Config.Handler = mcpMux(oauthServer, host)
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

func wire(
	t *testing.T,
	db *postgres.DB,
	host oauth.Host,
	httpClient *nhttp.Client,
	logger log.Logger,
) (*session.Session, *oauth.Server) {
	t.Helper()
	pool := db.Pool()
	opts := session.Options{
		UserAgent:  testUserAgent,
		Characters: characterpgx.New(pool, logger),
		Clients:    oauthclientpgx.New(pool),
		Logins:     loginstatepgx.New(pool),
		Codes:      authcodepgx.New(pool),
		Confirms:   confirmpgx.New(pool),
		Mutations:  mutationpgx.New(pool),
		HTTP:       httpClient,
		Logger:     logger,
	}
	opts.ESI = esihttp.New(esi.Options{
		UserAgent:  testUserAgent,
		CompatDate: esitest.CompatDate,
	}, httpClient, logger)
	opts.SSO = ssohttp.New(sso.Options{
		ClientID:    "test-eve-client",
		CallbackURL: host.CallbackURL,
		UserAgent:   testUserAgent,
		Scopes:      write.RequestedScopes(),
	}, httpClient, logger)
	runtime, err := session.Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	oauthServer, err := oauth.Open(host, runtime, oauth.Options{HMACKey: []byte(testHMACKey)}, logger)
	if err != nil {
		t.Fatal(err)
	}

	return runtime, oauthServer
}

func seedCharacter(t *testing.T, runtime *session.Session, oauthServer *oauth.Server) int {
	t.Helper()
	ctx := t.Context()
	id := esitest.FixtureCharacterID
	if err := runtime.Characters.Upsert(ctx, character.Character{
		ID:           int64(id),
		Name:         fixtureName,
		RefreshToken: "fixture-refresh",
		Scopes:       write.RequestedScopes(),
	}); err != nil {
		t.Fatal(err)
	}
	sess := oauthServer.SessionFor(id)
	if err := sess.SSO.Upsert(ctx, &sso.CharacterToken{
		CharacterID:     id,
		CharacterName:   fixtureName,
		RefreshToken:    "fixture-refresh",
		Scopes:          write.RequestedScopes(),
		AccessToken:     testAccessToken,
		AccessExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	return id
}

func mcpMux(oauthServer *oauth.Server, host oauth.Host) nhttp.Handler {
	h := httpsvc.New(oauthServer, host)
	mux := nhttp.NewServeMux()
	httpsvc.HandlerFromMux(h, mux)
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
