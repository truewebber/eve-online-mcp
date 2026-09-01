package eve

import (
	"errors"
	"testing"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
)

var errDialTCP = errors.New("dial tcp")

func TestESIWriteOutcomeStatus(t *testing.T) {
	t.Parallel()
	status, msg := esiWriteOutcome(esi.Error{Msg: "ESI 520 on /mail", Status: 520})
	if status != 520 || msg == "" {
		t.Fatalf("esi error %d %q", status, msg)
	}
	status, msg = esiWriteOutcome(esi.RateLimitedError{Msg: "ESI 420", Status: 420})
	if status != 420 || msg == "" {
		t.Fatalf("rate %d %q", status, msg)
	}
	status, msg = esiWriteOutcome(errDialTCP)
	if status != 0 || msg != "dial tcp" {
		t.Fatalf("plain %d %q", status, msg)
	}
}
