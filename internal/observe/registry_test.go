package observe

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRegistryRED(t *testing.T) {
	t.Parallel()
	r := New()
	r.Request("GET", 200, "/characters/{id}/assets", time.Millisecond)
	r.HTTP("POST", 401, "/mcp", 2*time.Millisecond)
	if got := testutil.ToFloat64(r.esiRequests.WithLabelValues("GET", "200", "/characters/{id}/assets")); got != 1 {
		t.Fatalf("esi %v", got)
	}
	if got := testutil.ToFloat64(r.httpRequests.WithLabelValues("POST", "401", "/mcp")); got != 1 {
		t.Fatalf("http %v", got)
	}
}

func TestHandlerServes(t *testing.T) {
	t.Parallel()
	r := New()
	r.Request("GET", 200, "/status", time.Millisecond)
	rec := httptest.NewRecorder()
	r.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "eve_mcp_esi_requests_total") {
		t.Fatalf("body %s", body)
	}
	if strings.Contains(body, "sessions_active") || strings.Contains(body, "tool_calls") {
		t.Fatalf("leftover series %s", body)
	}
}
