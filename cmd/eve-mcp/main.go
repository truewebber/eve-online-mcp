package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"eve-mcp/internal/adapter/esi"
	"eve-mcp/internal/adapter/sso"
	"eve-mcp/internal/adapter/store"
	"eve-mcp/internal/domain/write"
	httpsvc "eve-mcp/internal/service/http"
	"eve-mcp/internal/usecase/oauth"
	"eve-mcp/internal/usecase/session"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("eve-mcp ")

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "install":
			if err := installService(); err != nil {
				log.Fatal(err)
			}
			return
		case "uninstall":
			if err := uninstallService(); err != nil {
				log.Fatal(err)
			}
			return
		case "help", "-h", "--help":
			fmt.Print(usage)
			return
		}
	}

	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}

	db, err := store.Open(context.Background(), cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	runtime, err := session.Open(session.Options{
		UserAgent: cfg.UserAgent,
		Store:     db,
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
		log.Fatal(err)
	}
	defer runtime.Close()

	host := oauth.Host{
		Listen:      cfg.Listen,
		PublicURL:   cfg.PublicURL,
		MCPPath:     "/mcp",
		CallbackURL: cfg.CallbackURL,
	}
	oauthServer, err := oauth.Open(host, runtime, db)
	if err != nil {
		log.Fatal(err)
	}

	h := httpsvc.New(oauthServer, host)
	if err := httpsvc.ListenAndServe(h, runtime, httpsvc.ListenOptions{
		Listen:         cfg.Listen,
		InternalListen: cfg.InternalListen,
		MCPPath:        host.MCPPath,
		Version:        version,
	}); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}

const usage = `eve-mcp — MCP server that exposes EVE Online accounts to LLM clients

Usage:
  eve-mcp                  run the server (config from env / ./.env)
  eve-mcp install          install a user service (launchd on macOS)
  eve-mcp uninstall        stop and remove the user service

Required env: CLIENT_ID — the EVE application from developers.eveonline.com.
DATABASE_URL — Postgres DSN (make postgres). See .env.example.
See .env.example for the full list. Clients connect to http://127.0.0.1:8765/mcp
and sign in with their EVE account in the browser.
`
