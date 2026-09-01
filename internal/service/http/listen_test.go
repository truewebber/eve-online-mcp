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
	mux := internalMux(func(context.Context) error { return errReadyDown })
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
	mux := internalMux(func(context.Context) error { return nil })
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("readyz %d", rec.Code)
	}
}
