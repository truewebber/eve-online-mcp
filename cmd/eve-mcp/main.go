package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"eve-mcp/internal/app"
	"eve-mcp/internal/config"
	"eve-mcp/internal/server"
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

	configPath := flag.String("config", "", "path to config.toml (default: user config dir)")
	listen := flag.String("listen", "", "override listen address, e.g. 127.0.0.1:8765 or 0.0.0.0:8765")
	publicURL := flag.String("public-url", "", "public base URL when exposing over the network")
	flag.Parse()

	settings, tokenGenerated, err := config.Load(*configPath, *listen, *publicURL)
	if err != nil {
		log.Fatal(err)
	}
	if tokenGenerated {
		log.Printf("generated mcp_token and wrote it to %s — add it as Authorization: Bearer … on remote clients", settings.ConfigPath)
	}

	a, err := app.Open(settings)
	if err != nil {
		log.Fatal(err)
	}
	defer a.Close()

	if err := server.ListenAndServe(settings, a); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}

const usage = `eve-mcp — one-binary MCP server for a player's EVE Online account

Usage:
  eve-mcp [flags]          run the server
  eve-mcp install          install a user service (launchd on macOS)
  eve-mcp uninstall        stop and remove the user service

Flags:
  -config string       path to config.toml (default: user config dir)
  -listen string       listen address, e.g. 127.0.0.1:8765 or 0.0.0.0:8765
  -public-url string   public base URL when exposing over the network

Clients (Cursor, Claude Code) connect to http://127.0.0.1:8765/mcp
Config and tokens live in the OS user config directory, not next to the binary.
`
