package esi

import (
	"net/http"
	"testing"
	"time"
)

func TestLimitErrorParsesHeaders(t *testing.T) {
	t.Parallel()
	resp := &http.Response{
		StatusCode: 420,
		Header:     http.Header{},
	}
	resp.Header.Set("Retry-After", "8")
	resp.Header.Set("X-Esi-Error-Limit-Remain", "0")
	resp.Header.Set("X-Esi-Error-Limit-Reset", "12")

	err := limitError(resp, "/status")
	if err.Status != 420 {
		t.Fatalf("status %d", err.Status)
	}
	if err.Remain == nil || *err.Remain != 0 {
		t.Fatalf("remain %+v", err.Remain)
	}
	if err.ResetSec == nil || *err.ResetSec != 12 {
		t.Fatalf("reset %+v", err.ResetSec)
	}
	if err.RetrySec < 12 {
		t.Fatalf("retry_sec should follow the longer reset, got %d", err.RetrySec)
	}
	if time.Until(err.RetryAt) < 10*time.Second {
		t.Fatalf("retry_at too soon: %s", err.RetryAt)
	}
	if err.Error() == "" {
		t.Fatal("empty message")
	}
}

func TestRetryAfterHTTPDate(t *testing.T) {
	t.Parallel()
	when := time.Now().UTC().Add(25 * time.Second).Format(http.TimeFormat)
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Retry-After", when)
	d := retryAfter(resp)
	if d < 20*time.Second || d > 30*time.Second {
		t.Fatalf("retry-after date parsed as %s", d)
	}
}
