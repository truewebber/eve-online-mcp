package httpsvc

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestShortGrantNamesMissingScopes(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	shortGrant(rec, []string{"esi-mail.send_mail.v1"})
	body := rec.Body.String()
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
	if !strings.Contains(body, "esi-mail.send_mail.v1") {
		t.Fatalf("missing identifier: %s", body)
	}
	if !strings.Contains(body, "developers.eveonline.com") {
		t.Fatalf("fix place: %s", body)
	}
	for _, leak := range []string{"oauth:", "wrap(", "sso:", "invalid_grant"} {
		if strings.Contains(body, leak) {
			t.Fatalf("leaked %q: %s", leak, body)
		}
	}
}
