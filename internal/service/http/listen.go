package httpsvc

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	svcmcp "github.com/truewebber/eve-online-mcp/internal/service/mcp"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	httpServers       = 2
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 15 * time.Second
	writeTimeout      = 2 * time.Minute
	idleTimeout       = 2 * time.Minute
)

type ListenOptions struct {
	Listen         string
	InternalListen string
	MCPPath        string
	Version        string
}

// The internal listener (healthz, later metrics) must never be exposed.
func ListenAndServe(h *API, opts ListenOptions) error {
	mux := http.NewServeMux()
	HandlerFromMux(h, mux)

	prm := h.OAuth.ProtectedResourceHandler()
	mux.Handle("/.well-known/oauth-protected-resource/", prm)
	mux.Handle("/.well-known/oauth-protected-resource/mcp", prm)
	mux.HandleFunc("/.well-known/oauth-authorization-server/", h.OAuth.ServeASMeta)

	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name: "eve-online", Title: "EVE Online", Version: opts.Version,
	}, &mcp.ServerOptions{Instructions: svcmcp.Instructions()})
	svcmcp.Register(mcpServer)
	stream := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return mcpServer
	}, &mcp.StreamableHTTPOptions{
		Stateless:                  true,
		DisableLocalhostProtection: true,
	})
	protected := h.OAuth.ProtectMCP(stream)
	path := opts.MCPPath
	if path == "" {
		path = "/mcp"
	}
	mux.Handle(path, protected)
	if !strings.HasSuffix(path, "/") {
		mux.Handle(path+"/", protected)
	}

	errs := make(chan error, httpServers)
	go func() { errs <- serve(opts.InternalListen, internalMux()) }()

	base := h.Host.BaseURL()
	log.Printf("writes: confirm, mail cap 5/hour")
	log.Printf("MCP endpoint: %s%s (OAuth — clients show Authentication required)", base, path)
	log.Printf("EVE callback must be exactly: %s", h.Host.CallbackURL)
	log.Printf("internal endpoint (healthz): http://%s", opts.InternalListen)

	go func() { errs <- serve(opts.Listen, mux) }()

	return <-errs
}

func serve(addr string, h http.Handler) error {
	s := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	if err := s.ListenAndServe(); err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	return nil
}

// Not on the public mux — k8s probes only.
func internalMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"status":"ok"}`)); err != nil {
			return
		}
	})

	return mux
}
