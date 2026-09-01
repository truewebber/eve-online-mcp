package httpsvc

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/truewebber/gopkg/log"

	svcmcp "github.com/truewebber/eve-online-mcp/internal/service/mcp"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	httpServers       = 2
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 15 * time.Second
	writeTimeout      = 2 * time.Minute
	idleTimeout       = 2 * time.Minute
	readyTimeout      = 2 * time.Second
	readyOK           = `{"status":"ok"}`
	readyDown         = `{"status":"unavailable"}`
)

type ListenOptions struct {
	Listen            string
	InternalListen    string
	MCPPath           string
	Version           string
	TrustConnectingIP bool
	Ready             func(context.Context) error
	Logger            log.Logger
	Metrics           http.Handler
	Observe           Observer
}

// The internal listener (healthz, readyz, metrics) must never be exposed.
func ListenAndServe(h *API, opts ListenOptions) error {
	if err := checkListen(opts); err != nil {
		return err
	}
	errs := make(chan error, httpServers)
	go func() { errs <- serve(opts.InternalListen, internalMux(opts.Ready, opts.Metrics)) }()

	base := h.Host.BaseURL()
	path := opts.MCPPath
	opts.Logger.Info("writes: confirm, mail cap 5/hour")
	opts.Logger.Info("MCP endpoint (OAuth — clients show Authentication required)", "base", base, "path", path)
	opts.Logger.Info("EVE callback must be exactly this URL", "url", h.Host.CallbackURL)
	opts.Logger.Info("internal endpoint (healthz, readyz, metrics)", "addr", opts.InternalListen)

	go func() { errs <- serve(opts.Listen, publicHandler(h, opts)) }()

	return <-errs
}

func publicHandler(h *API, opts ListenOptions) http.Handler {
	public := http.NewServeMux()
	mountPublic(h, public)
	limited := newLimiter(opts.Logger).wrap(public, opts.TrustConnectingIP)
	root := http.NewServeMux()
	mountMCP(root, h, opts)
	root.Handle("/", limited)

	return observePublic(root, opts.Observe)
}

func mountPublic(h *API, mux *http.ServeMux) {
	h.Mount(mux)
	prm := h.OAuth.ProtectedResourceHandler()
	mux.Handle("/.well-known/oauth-protected-resource/", prm)
	mux.Handle("/.well-known/oauth-protected-resource/mcp", prm)
	mux.HandleFunc("/.well-known/oauth-authorization-server/", h.OAuth.ServeASMeta)
}

func mountMCP(mux *http.ServeMux, h *API, opts ListenOptions) {
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
	mux.Handle(path, protected)
	if !strings.HasSuffix(path, "/") {
		mux.Handle(path+"/", protected)
	}
}

func checkListen(opts ListenOptions) error {
	switch {
	case opts.Listen == "" || opts.InternalListen == "" || opts.MCPPath == "":
		return errListenRequired
	case opts.Logger == nil:
		return errLoggerRequired
	case opts.Metrics == nil:
		return errMetricsRequired
	case opts.Observe == nil:
		return errObserveRequired
	case opts.Ready == nil:
		return errReadyRequired
	default:
		return nil
	}
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

func internalMux(ready func(context.Context) error, metrics http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", serveHealthz)
	mux.HandleFunc("/readyz", serveReadyz(ready))
	mux.Handle("/metrics", metrics)

	return mux
}

func serveHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, readyOK)
}

func serveReadyz(ready func(context.Context) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
		defer cancel()
		if err := ready(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, readyDown)

			return
		}
		writeJSON(w, http.StatusOK, readyOK)
	}
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write([]byte(body)); err != nil {
		return
	}
}
