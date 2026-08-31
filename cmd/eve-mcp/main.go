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
	"github.com/truewebber/eve-online-mcp/internal/adapter/store"
	authcodepgx "github.com/truewebber/eve-online-mcp/internal/domain/authcode/pgx"
	characterpgx "github.com/truewebber/eve-online-mcp/internal/domain/character/pgx"
	confirmpgx "github.com/truewebber/eve-online-mcp/internal/domain/confirm/pgx"
	loginstatepgx "github.com/truewebber/eve-online-mcp/internal/domain/loginstate/pgx"
	oauthclientpgx "github.com/truewebber/eve-online-mcp/internal/domain/oauthclient/pgx"
	"github.com/truewebber/eve-online-mcp/internal/domain/write"
	httpsvc "github.com/truewebber/eve-online-mcp/internal/service/http"
	"github.com/truewebber/eve-online-mcp/internal/usecase/oauth"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"
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

	db, err := store.Open(context.Background(), cfg.DatabaseURL, logger)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	pool := db.Pool()
	chars := characterpgx.New(pool, logger)
	opts := session.Options{
		UserAgent:  cfg.UserAgent,
		Store:      db,
		Characters: chars,
		Clients:    oauthclientpgx.New(pool),
		Logins:     loginstatepgx.New(pool),
		Codes:      authcodepgx.New(pool),
		Confirms:   confirmpgx.New(pool),
		Logger:     logger,
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
	defer runtime.Close()

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
