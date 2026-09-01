package main

import (
	"context"
	"fmt"
	"os"

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
	"github.com/truewebber/eve-online-mcp/internal/domain/write"
	"github.com/truewebber/eve-online-mcp/internal/postgres"
	httpsvc "github.com/truewebber/eve-online-mcp/internal/service/http"
	"github.com/truewebber/eve-online-mcp/internal/usecase/oauth"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"
	"github.com/truewebber/eve-online-mcp/internal/usecase/sweep"
)

func main() {
	os.Exit(run())
}

func run() int {
	logger := log.NewLogger()
	defer func() { _ = logger.Close() }()

	if err := start(logger); err != nil {
		logger.Error("fatal", "err", err)

		return 1
	}

	return 0
}

func start(logger log.Logger) error {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "help", "-h", "--help":
			logger.Info(usage)

			return nil
		}
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	db, err := postgres.Open(context.Background(), cfg.DatabaseURL, logger)
	if err != nil {
		return fmt.Errorf("open postgres: %w", err)
	}
	defer db.Close()
	pool := db.Pool()
	chars := characterpgx.New(pool, logger)
	opts := session.Options{
		UserAgent:  cfg.UserAgent,
		Characters: chars,
		Sessions:   sessionpgx.New(pool, logger),
		Clients:    oauthclientpgx.New(pool),
		Logins:     loginstatepgx.New(pool),
		Codes:      authcodepgx.New(pool),
		Confirms:   confirmpgx.New(pool),
		Mutations:  mutationpgx.New(pool),
		WithinTx: func(ctx context.Context, fn func(context.Context) error) error {
			return postgres.WithinTx(ctx, pool, fn)
		},
		Logger: logger,
	}
	opts.HTTP = session.NewHTTPClient(opts)
	opts.ESI = esihttp.New(esi.Options{
		UserAgent:  cfg.UserAgent,
		CompatDate: defaultCompatDate,
	}, opts.HTTP, logger)
	opts.SSO = ssohttp.New(sso.Options{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		CallbackURL:  cfg.CallbackURL,
		UserAgent:    cfg.UserAgent,
		Scopes:       write.RequestedScopes(),
	}, opts.HTTP, logger)
	runtime, err := session.Open(opts)
	if err != nil {
		return fmt.Errorf("open session: %w", err)
	}
	host := oauth.Host{
		Listen:      cfg.Listen,
		PublicURL:   cfg.PublicURL,
		MCPPath:     "/mcp",
		CallbackURL: cfg.CallbackURL,
	}
	oauthServer, err := oauth.Open(host, runtime, oauth.Options{HMACKey: cfg.hmacKey}, logger)
	if err != nil {
		return fmt.Errorf("open oauth: %w", err)
	}
	go sweep.New(sweep.Options{
		Lock:      sweep.NewPoolLock(pool),
		Logins:    opts.Logins,
		Codes:     opts.Codes,
		Confirms:  opts.Confirms,
		Sessions:  opts.Sessions,
		Mutations: opts.Mutations,
		Clients:   opts.Clients,
		SSO:       opts.SSO,
		Logger:    logger,
	}).Start(context.Background())

	h := httpsvc.New(oauthServer, host)
	if err := httpsvc.ListenAndServe(h, httpsvc.ListenOptions{
		Listen:         cfg.Listen,
		InternalListen: cfg.InternalListen,
		MCPPath:        host.MCPPath,
		Version:        version,
		Logger:         logger,
	}); err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	return nil
}

const usage = `eve-mcp — MCP server that exposes EVE Online accounts to LLM clients

Usage:
  eve-mcp                  run the server (config from env / ./.env)

Required env: CLIENT_ID — the EVE application from developers.eveonline.com.
DATABASE_URL — Postgres DSN (make postgres). HMAC_KEY — MCP JWT signing
key, min 32 bytes (openssl rand -hex 32). See .env.example.
See .env.example for the full list. Clients connect to http://127.0.0.1:8765/mcp
and sign in with their EVE account in the browser.
`
