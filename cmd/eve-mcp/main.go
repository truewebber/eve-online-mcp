package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"eve-mcp/internal/adapter/esi"
	"eve-mcp/internal/adapter/sso"
	aduser "eve-mcp/internal/adapter/user"
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

	allow, err := writeAllow(cfg)
	if err != nil {
		log.Fatal(err)
	}
	writeOpts := write.Options{
		Mode:               cfg.WriteMode,
		Allow:              allow,
		WriteBudgetPerHour: cfg.WriteBudgetHour,
		MailBudgetPerHour:  cfg.MailBudgetHour,
		ConfirmTTLSeconds:  cfg.ConfirmTTL,
		AuditFile:          filepath.Join(cfg.DataDir, "audit.jsonl"),
	}
	runtime, err := session.Open(session.Options{
		DataDir:    cfg.DataDir,
		UserAgent:  cfg.UserAgent,
		CorpScopes: cfg.CorpScopes,
		WriteMode:  cfg.WriteMode,
		ESI: esi.Options{
			UserAgent:  cfg.UserAgent,
			CompatDate: cfg.CompatDate,
		},
		SSO: sso.Options{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			CallbackURL:  cfg.CallbackURL,
			UserAgent:    cfg.UserAgent,
			Scopes:       writeOpts.RequestedScopes(cfg.CorpScopes),
		},
		Write: writeOpts,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer runtime.Close()

	users, err := aduser.Open(cfg.DataDir)
	if err != nil {
		log.Fatal(err)
	}
	host := oauth.Host{
		DataDir:     cfg.DataDir,
		Listen:      cfg.Listen,
		PublicURL:   cfg.PublicURL,
		MCPPath:     "/mcp",
		CallbackURL: cfg.CallbackURL,
		WriteMode:   cfg.WriteMode,
	}
	oauthServer, err := oauth.Open(host, runtime, users)
	if err != nil {
		log.Fatal(err)
	}

	h := httpsvc.New(oauthServer, host)
	if err := httpsvc.ListenAndServe(h, runtime, httpsvc.ListenOptions{
		Listen:         cfg.Listen,
		InternalListen: cfg.InternalListen,
		MCPPath:        host.MCPPath,
		Version:        version,
		CorpScopes:     cfg.CorpScopes,
	}); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}

func writeAllow(cfg config) (map[string]struct{}, error) {
	list := cfg.WriteAllowList
	if len(list) == 1 && list[0] == "all" {
		out := map[string]struct{}{}
		for name := range write.Capabilities {
			out[name] = struct{}{}
		}
		return out, nil
	}
	out := map[string]struct{}{}
	if len(list) == 1 && (list[0] == "none" || list[0] == "") {
		return out, nil
	}
	for _, name := range list {
		if _, ok := write.Capabilities[name]; !ok {
			return nil, fmt.Errorf("unknown WRITE_ALLOW entry %q", name)
		}
		out[name] = struct{}{}
	}
	return out, nil
}

const usage = `eve-mcp — MCP server that exposes EVE Online accounts to LLM clients

Usage:
  eve-mcp                  run the server (config from env / ./.env)
  eve-mcp install          install a user service (launchd on macOS)
  eve-mcp uninstall        stop and remove the user service

Required env: CLIENT_ID — the EVE application from developers.eveonline.com.
See .env.example for the full list. Clients connect to http://127.0.0.1:8765/mcp
and sign in with their EVE account in the browser.
`
