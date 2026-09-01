package eve

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	"github.com/truewebber/eve-online-mcp/internal/domain/write"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var errInner = errors.New("dial tcp 10.0.0.1:5432: pgx-detail")

const misspelledName = "Riftr"

func TestHandleKeepsKindAndHidesInner(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err  error
		kind string
		has  string
	}{
		{session.ErrNoSession, "AuthError", sentenceAuth},
		{sso.ErrInvalidGrant, "AuthError", sentenceGrant},
		{session.ErrNPCCorp, kindError, "eve_corp_overview"},
		{write.ErrMailCap, "WriteBlocked", sentenceMail},
		{esi.UserLimitedError{RetrySec: 3, RetryAt: time.Unix(0, 0).UTC(), Reason: esi.ErrAllowanceSpent}, "UserRateLimited", sentenceAllowance},
		{esi.UserLimitedError{RetrySec: 3, RetryAt: time.Unix(0, 0).UTC(), Reason: esi.ErrBudgetSpent}, "UserRateLimited", sentenceBudget},
		{esi.RateLimitedError{Msg: "ccp-detail", Status: 420, RetrySec: 8, RetryAt: time.Unix(0, 0).UTC()}, "EsiRateLimited", sentenceESILimit},
		{esi.Error{Msg: "Network error: " + errInner.Error(), Status: 502}, "EsiError", sentenceESI},
		{UnresolvedError{Names: []string{misspelledName}}, kindError, sentenceUnresolved},
		{ValidationError{Field: "character_id", Invariant: "must be a positive integer"}, kindError, "character_id must be a positive integer"},
		{ErrCSPAUnpriced, kindError, "CSPA charge could not be priced"},
		{CSPAExceedsError{Cost: 10, Approved: 0}, kindError, "exceeds approved_cost"},
		{errInner, kindError, sentenceGeneric},
	}
	for _, tc := range cases {
		res, _, err := Handle(tc.err)
		if err != nil {
			t.Fatalf("%v: %v", tc.err, err)
		}
		raw := toolText(res)
		var out map[string]any
		if json.Unmarshal([]byte(raw), &out) != nil {
			t.Fatalf("json %s", raw)
		}
		if out["kind"] != tc.kind {
			t.Fatalf("%v kind %v want %s", tc.err, out["kind"], tc.kind)
		}
		msg, ok := out["error"].(string)
		if !ok || !strings.Contains(msg, tc.has) {
			t.Fatalf("%v sentence %q", tc.err, msg)
		}
		if strings.Contains(raw, errInner.Error()) || strings.Contains(raw, "pgx-detail") || strings.Contains(raw, "ccp-detail") {
			t.Fatalf("inner leaked: %s", raw)
		}
	}
}

func TestHandleCSPAExceedsExtras(t *testing.T) {
	t.Parallel()
	res, _, err := Handle(CSPAExceedsError{Cost: 2950, Approved: 0})
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if json.Unmarshal([]byte(toolText(res)), &out) != nil {
		t.Fatal(toolText(res))
	}
	if out["priced_cspa_cost_isk"] != float64(2950) || out["approved_cost"] != float64(0) || out["shortfall_isk"] != float64(2950) {
		t.Fatalf("%+v", out)
	}
}

func TestHandleUnresolvedNamesAreData(t *testing.T) {
	t.Parallel()
	res, _, err := Handle(UnresolvedError{Names: []string{misspelledName}})
	if err != nil {
		t.Fatal(err)
	}
	raw := toolText(res)
	var out map[string]any
	if json.Unmarshal([]byte(raw), &out) != nil {
		t.Fatalf("json %s", raw)
	}
	names, ok := out["names"].([]any)
	if !ok || len(names) != 1 || names[0] != misspelledName {
		t.Fatalf("names %+v", out["names"])
	}
	msg, ok := out["error"].(string)
	if !ok || strings.Contains(msg, misspelledName) {
		t.Fatalf("name spliced into sentence: %q", msg)
	}
}

func TestHandleUserRateLimitedExtras(t *testing.T) {
	t.Parallel()
	retryAt := time.Date(2026, 9, 1, 1, 0, 0, 0, time.UTC)
	res, _, err := Handle(esi.UserLimitedError{
		Msg: "inner spent", RetrySec: 12, RetryAt: retryAt, Reason: esi.ErrBudgetSpent,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := toolText(res)
	var out map[string]any
	if json.Unmarshal([]byte(raw), &out) != nil {
		t.Fatalf("json %s", raw)
	}
	if out["retry_after_seconds"] != float64(12) || out["retry_at"] != "2026-09-01T01:00:00Z" {
		t.Fatalf("%+v", out)
	}
	if strings.Contains(raw, "inner spent") {
		t.Fatalf("inner leaked: %s", raw)
	}
}

func toolText(result *mcp.CallToolResult) string {
	if result == nil {
		return ""
	}
	var b strings.Builder
	for _, c := range result.Content {
		if text, ok := c.(*mcp.TextContent); ok {
			b.WriteString(text.Text)
		}
	}

	return b.String()
}
