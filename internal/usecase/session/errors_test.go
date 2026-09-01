package session

import (
	"testing"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
)

func TestMapErrorUserRateLimited(t *testing.T) {
	t.Parallel()
	retryAt := time.Date(2026, 8, 30, 12, 0, 1, 0, time.UTC)
	out := MapError(esi.UserLimitedError{
		Msg: "spent", RetryAt: retryAt, RetrySec: 1,
	})
	if out["kind"] != "UserRateLimited" {
		t.Fatalf("kind %v", out["kind"])
	}
	if out["retry_after_seconds"] != 1 {
		t.Fatalf("retry_after %v", out["retry_after_seconds"])
	}
	if out["retry_at"] != "2026-08-30T12:00:01Z" {
		t.Fatalf("retry_at %v", out["retry_at"])
	}
	if _, ok := out["error_limit_remain"]; ok {
		t.Fatal("UserRateLimited must not carry CCP error-limit fields")
	}
}

func TestMapErrorErrorBudgetIsUserRateLimited(t *testing.T) {
	t.Parallel()
	out := MapError(esi.UserLimitedError{
		Msg: "This character's ESI error budget is spent", RetrySec: 12,
		RetryAt: time.Date(2026, 9, 1, 1, 0, 0, 0, time.UTC),
	})
	if out["kind"] != "UserRateLimited" {
		t.Fatalf("kind %v", out["kind"])
	}
	if _, ok := out["error"]; ok {
		t.Fatalf("sentence belongs at the transport: %+v", out)
	}
	if out["retry_after_seconds"] != 12 {
		t.Fatalf("%+v", out)
	}
}

func TestMapErrorEsiRateLimitedUnchanged(t *testing.T) {
	t.Parallel()
	out := MapError(esi.RateLimitedError{Msg: "ccp", Status: 420, RetrySec: 8, RetryAt: time.Unix(0, 0).UTC()})
	if out["kind"] != "EsiRateLimited" {
		t.Fatalf("kind %v", out["kind"])
	}
}
