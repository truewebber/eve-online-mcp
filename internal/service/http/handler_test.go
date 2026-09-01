package httpsvc

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCallbackCCPErrorIsStatic(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/auth/callback?error=access_denied&error_description=secret-ccp-string", nil)
	(&API{}).GetAuthCallback(rec, req, GetAuthCallbackParams{})
	body := rec.Body.String()
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
	if strings.Contains(body, "secret-ccp-string") || strings.Contains(body, "access_denied") {
		t.Fatalf("ccp string rendered: %s", body)
	}
}
