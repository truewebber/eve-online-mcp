package main

import (
	"context"
	"fmt"
	"net/http"
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
	"github.com/truewebber/eve-online-mcp/internal/observe"
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
	if helpRequested() {
		logger.Info(usage)

		return nil
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
	obs := observe.New()
	runtime, err := openRuntime(cfg, db, logger, obs)
	if err != nil {
		return err
	}
	host := oauthHost(cfg)
	oauthServer, err := oauth.Open(host, runtime.session, oauth.Options{
		HMACKey:           cfg.hmacKey,
		ExtraRedirects:    cfg.ExtraRedirects,
		TrustConnectingIP: cfg.TrustConnectingIP,
	}, logger)
	if err != nil {
		return fmt.Errorf("open oauth: %w", err)
	}
	go sweep.New(runtime.sweep).Start(context.Background())

	return listen(serveIn{
		cfg: cfg, db: db, host: host, oauth: oauthServer, logger: logger,
		metrics: obs.Handler(), observe: obs,
	})
}

func helpRequested() bool {
	if len(os.Args) < minCLIArgs {
		return false
	}
	switch os.Args[1] {
	case "help", "-h", "--help":
		return true
	default:
		return false
	}
}

type runtime struct {
	session *session.Session
	sweep   sweep.Options
}

func openRuntime(cfg config, db *postgres.DB, logger log.Logger, obs *observe.Registry) (runtime, error) {
	opts := sessionOptions(cfg, db, logger, obs)
	opened, err := session.Open(opts)
	if err != nil {
		return runtime{}, fmt.Errorf("open session: %w", err)
	}

	return runtime{session: opened, sweep: sweep.Options{
		Lock:      sweep.NewPoolLock(db.Pool()),
		Logins:    opts.Logins,
		Codes:     opts.Codes,
		Confirms:  opts.Confirms,
		Sessions:  opts.Sessions,
		Mutations: opts.Mutations,
		Clients:   opts.Clients,
		SSO:       opts.SSO,
		Logger:    logger,
	}}, nil
}

func sessionOptions(cfg config, db *postgres.DB, logger log.Logger, obs *observe.Registry) session.Options {
	pool := db.Pool()
	opts := session.Options{
		UserAgent:  cfg.UserAgent,
		Characters: characterpgx.New(pool, logger),
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
		Observe:    obs,
	}, opts.HTTP, logger)
	opts.SSO = ssohttp.New(sso.Options{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		CallbackURL:  cfg.CallbackURL,
		UserAgent:    cfg.UserAgent,
		Scopes:       write.RequestedScopes(),
	}, opts.HTTP, logger)

	return opts
}

func oauthHost(cfg config) oauth.Host {
	return oauth.Host{
		Listen:      cfg.Listen,
		PublicURL:   cfg.PublicURL,
		MCPPath:     "/mcp",
		CallbackURL: cfg.CallbackURL,
	}
}

type serveIn struct {
	cfg     config
	db      *postgres.DB
	host    oauth.Host
	oauth   *oauth.Server
	logger  log.Logger
	metrics http.Handler
	observe httpsvc.Observer
}

func listen(in serveIn) error {
	h := httpsvc.New(in.oauth, in.host, in.logger)
	if err := httpsvc.ListenAndServe(h, httpsvc.ListenOptions{
		Listen:            in.cfg.Listen,
		InternalListen:    in.cfg.InternalListen,
		MCPPath:           in.host.MCPPath,
		Version:           version,
		TrustConnectingIP: in.cfg.TrustConnectingIP,
		Ready:             in.db.Ping,
		Logger:            in.logger,
		Metrics:           in.metrics,
		Observe:           in.observe,
	}); err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	return nil
}

const (
	minCLIArgs = 2
	usage      = `eve-mcp — MCP server that exposes EVE Online accounts to LLM clients

Usage:
  eve-mcp                  run the server (config from env / ./.env)

Required env: CLIENT_ID — the EVE application from developers.eveonline.com.
DATABASE_URL — Postgres DSN (make postgres). HMAC_KEY — MCP JWT signing
key, min 32 bytes (openssl rand -hex 32). See .env.example.
LISTEN_HOST_PORT / INTERNAL_LISTEN_HOST_PORT default to loopback.
PUBLIC_URL is required when the public bind is not loopback.
See .env.example for the full list. Clients connect to http://127.0.0.1:8765/mcp
and sign in with their EVE account in the browser.
`
)
