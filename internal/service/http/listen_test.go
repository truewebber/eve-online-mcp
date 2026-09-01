package httpsvc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

var errReadyDown = errors.New("postgres: ping")

func TestReadyzDownHealthzUp(t *testing.T) {
	t.Parallel()
	mux := internalMux(func(context.Context) error { return errReadyDown }, nil)
	ready := httptest.NewRecorder()
	mux.ServeHTTP(ready, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz %d", ready.Code)
	}
	live := httptest.NewRecorder()
	mux.ServeHTTP(live, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil))
	if live.Code != http.StatusOK {
		t.Fatalf("healthz %d", live.Code)
	}
}

func TestReadyzOK(t *testing.T) {
	t.Parallel()
	mux := internalMux(func(context.Context) error { return nil }, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("readyz %d", rec.Code)
	}
}

func TestInternalMetrics(t *testing.T) {
	t.Parallel()
	mux := internalMux(nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("ok")); err != nil {
			t.Fatal(err)
		}
	}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics %d", rec.Code)
	}
	missing := httptest.NewRecorder()
	internalMux(nil, nil).ServeHTTP(missing, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("nil metrics %d", missing.Code)
	}
}

func TestRouteTemplate(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/oauth/authorize", nil)
	req.Pattern = "GET /oauth/authorize"
	if got := routeTemplate(req); got != "/oauth/authorize" {
		t.Fatalf("pattern %s", got)
	}
	req = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.Pattern = "GET /{$}"
	if got := routeTemplate(req); got != "/" {
		t.Fatalf("index %s", got)
	}
	req = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/nope", nil)
	req.Pattern = "/"
	if got := routeTemplate(req); got != otherRoute {
		t.Fatalf("unknown %s", got)
	}
}
