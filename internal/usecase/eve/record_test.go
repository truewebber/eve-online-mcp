package eve

import (
	"errors"
	"testing"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
)

var errDialTCP = errors.New("dial tcp")

func TestESIWriteOutcomeStatus(t *testing.T) {
	t.Parallel()
	out := esiWriteOutcome(esi.Error{Msg: "ESI 520 on /mail", Status: 520})
	if out.status != 520 || out.msg == "" {
		t.Fatalf("esi error %d %q", out.status, out.msg)
	}
	out = esiWriteOutcome(esi.RateLimitedError{Msg: "ESI 420", Status: 420})
	if out.status != 420 || out.msg == "" {
		t.Fatalf("rate %d %q", out.status, out.msg)
	}
	out = esiWriteOutcome(errDialTCP)
	if out.status != 0 || out.msg != "dial tcp" {
		t.Fatalf("plain %d %q", out.status, out.msg)
	}
}
