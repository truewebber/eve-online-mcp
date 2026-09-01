package httpsvc

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/mocks"
)

func TestLimiterRejectsSixtyFirst(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		l := newLimiter(mocks.QuietLogger(gomock.NewController(t)))
		h := l.wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}), false)
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		req.RemoteAddr = "203.0.113.10:9"
		for range publicRateLimit {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("allowed status %d", rec.Code)
			}
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("61st status %d", rec.Code)
		}
		if rec.Header().Get("Retry-After") == "" {
			t.Fatal("missing Retry-After")
		}
		time.Sleep(publicRateWindow)
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("after window %d", rec.Code)
		}
	})
}
