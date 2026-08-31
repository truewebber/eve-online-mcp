package main

import (
	"context"
	"fmt"
	"os"

	"github.com/truewebber/gopkg/log"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	"github.com/truewebber/eve-online-mcp/internal/adapter/store"
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
	runtime, err := session.Open(session.Options{
		UserAgent: cfg.UserAgent,
		Store:     db,
		Logger:    logger,
		ESI: esi.Options{
			UserAgent:  cfg.UserAgent,
			CompatDate: defaultCompatDate,
		},
		SSO: sso.Options{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			CallbackURL:  cfg.CallbackURL,
			UserAgent:    cfg.UserAgent,
			Scopes:       write.RequestedScopes(),
		},
	})
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
	oauthServer, err := oauth.Open(host, runtime, db, logger)
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
DATABASE_URL — Postgres DSN (make postgres). See .env.example.
See .env.example for the full list. Clients connect to http://127.0.0.1:8765/mcp
and sign in with their EVE account in the browser.
`
