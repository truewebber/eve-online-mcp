package tests

import (
	"context"
	"flag"
	"testing"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi/http/esitest"
)

var update = flag.Bool("update", false, "record fixtures from live ESI") //nolint:gochecknoglobals // golden-file -update flag

func TestFixtures(t *testing.T) { //nolint:paralleltest // -update writes testdata and must not race
	if !*update {
		t.Skip("re-record with go test ./tests -run TestFixtures -update")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	if err := esitest.Update(ctx); err != nil {
		t.Fatal(err)
	}
}
