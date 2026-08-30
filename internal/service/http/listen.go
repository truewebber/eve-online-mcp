package httpsvc

import (
	"log"
	"net/http"
	"strings"

	svcmcp "github.com/truewebber/eve-online-mcp/internal/service/mcp"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ListenOptions struct {
	Listen         string
	InternalListen string
	MCPPath        string
	Version        string
}

// ListenAndServe runs two HTTP servers: the public one (MCP + OAuth + pages)
// and the internal one (healthz, later metrics) that must never be exposed.
func ListenAndServe(h *API, runtime *session.Session, opts ListenOptions) error {
	mux := http.NewServeMux()
	HandlerFromMux(h, mux)

	prm := h.OAuth.ProtectedResourceHandler()
	mux.Handle("/.well-known/oauth-protected-resource/", prm)
	mux.Handle("/.well-known/oauth-protected-resource/mcp", prm)
	mux.HandleFunc("/.well-known/oauth-authorization-server/", h.OAuth.ServeASMeta)

	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name: "eve-online", Title: "EVE Online", Version: opts.Version,
	}, &mcp.ServerOptions{Instructions: svcmcp.Instructions()})
	svcmcp.Register(mcpServer, runtime)
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

	errs := make(chan error, 2)
	go func() { errs <- http.ListenAndServe(opts.InternalListen, internalMux()) }()

	base := h.Host.BaseURL()
	log.Printf("writes: confirm, mail cap 5/hour")
	log.Printf("MCP endpoint: %s%s (OAuth — clients show Authentication required)", base, path)
	log.Printf("EVE callback must be exactly: %s", h.Host.CallbackURL)
	log.Printf("internal endpoint (healthz): http://%s", opts.InternalListen)

	go func() { errs <- http.ListenAndServe(opts.Listen, mux) }()
	return <-errs
}

// internalMux is the k8s-facing surface: /healthz now, /metrics when
// Prometheus lands. Not part of the OpenAPI contract, never routed publicly.
func internalMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	return mux
}
