package session

import (
	"context"
	nhttp "net/http"
	"testing"

	"github.com/truewebber/gopkg/log"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	esihttp "github.com/truewebber/eve-online-mcp/internal/adapter/esi/http"
	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	ssohttp "github.com/truewebber/eve-online-mcp/internal/adapter/sso/http"
	authcodepgx "github.com/truewebber/eve-online-mcp/internal/domain/authcode/pgx"
	characterpgx "github.com/truewebber/eve-online-mcp/internal/domain/character/pgx"
	confirmpgx "github.com/truewebber/eve-online-mcp/internal/domain/confirm/pgx"
	loginstatepgx "github.com/truewebber/eve-online-mcp/internal/domain/loginstate/pgx"
	mutationpgx "github.com/truewebber/eve-online-mcp/internal/domain/mutation/pgx"
	oauthclientpgx "github.com/truewebber/eve-online-mcp/internal/domain/oauthclient/pgx"
	sessionpgx "github.com/truewebber/eve-online-mcp/internal/domain/session/pgx"
	"github.com/truewebber/eve-online-mcp/internal/observe"
	"github.com/truewebber/eve-online-mcp/internal/postgres"

	"github.com/jackc/pgx/v5/pgxpool"
)

func pgxOptions(pool *pgxpool.Pool, esiC esi.Client, ssoC sso.Client, logger log.Logger) Options {
	return Options{
		Characters: characterpgx.New(pool),
		Sessions:   sessionpgx.New(pool),
		Clients:    oauthclientpgx.New(pool),
		Logins:     loginstatepgx.New(pool),
		Codes:      authcodepgx.New(pool),
		Confirms:   confirmpgx.New(pool),
		Mutations:  mutationpgx.New(pool),
		ESI:        esiC,
		SSO:        ssoC,
		WithinTx: func(ctx context.Context, fn func(context.Context) error) error {
			return postgres.WithinTx(ctx, pool, fn)
		},
		Logger: logger,
	}
}

func testESIClient(t *testing.T, logger log.Logger) esi.Client {
	t.Helper()
	c, err := esihttp.New(esi.Options{
		BaseURL:    esi.DefaultBaseURL,
		CompatDate: "2026-08-18",
		UserAgent:  "eve-mcp-test",
		Observe:    observe.New(),
	}, nhttp.DefaultClient, logger)
	if err != nil {
		t.Fatal(err)
	}

	return c
}

func testSSOClient(t *testing.T, logger log.Logger) sso.Client {
	t.Helper()
	c, err := ssohttp.New(sso.Options{
		ClientID:    "test-eve-client",
		CallbackURL: "http://127.0.0.1/auth/callback",
		UserAgent:   "eve-mcp-test",
	}, nhttp.DefaultClient, logger)
	if err != nil {
		t.Fatal(err)
	}

	return c
}
