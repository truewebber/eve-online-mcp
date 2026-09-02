package esitest

import (
	"context"
	"encoding/json"
	"flag"
	"testing"
	"time"

	nhttp "net/http"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	esihttp "github.com/truewebber/eve-online-mcp/internal/adapter/esi/http"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
	"github.com/truewebber/eve-online-mcp/internal/observe"
)

var update = flag.Bool("update", false, "record fixtures from live ESI") //nolint:gochecknoglobals // golden-file -update flag

func TestFixtures(t *testing.T) { //nolint:paralleltest // -update writes testdata and must not race
	if *update {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
		defer cancel()
		if err := Update(ctx); err != nil {
			t.Fatal(err)
		}
	}
	assertCatalog(t)
	assertStatusRoundTrip(t)
}

func assertCatalog(t *testing.T) {
	t.Helper()
	tr, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range Catalog() {
		f, ok := tr.Fixture(fixtureKey(spec))
		if !ok {
			t.Fatalf("missing fixture %s %s", spec.Method, spec.Path)
		}
		if f.Status == 0 {
			t.Fatalf("%s: empty status", f.Key())
		}
		if f.CompatDate != CompatDate {
			t.Fatalf("%s: compat %q", f.Key(), f.CompatDate)
		}
		if len(f.Headers) == 0 {
			t.Fatalf("%s: no headers", f.Key())
		}
		if !json.Valid(f.Body) && len(f.Body) > 0 {
			t.Fatalf("%s: body is not JSON", f.Key())
		}
		if spec.Status == statusErrorLimited {
			if f.Headers["X-Esi-Error-Limit-Remain"] == "" || f.Headers["X-Esi-Error-Limit-Reset"] == "" {
				t.Fatalf("%s: 420 missing error-limit headers", f.Key())
			}
		}
		if spec.Query["page"] != "" && f.Headers["X-Pages"] == "" {
			t.Fatalf("%s: paged fixture missing X-Pages", f.Key())
		}
	}
}

func assertStatusRoundTrip(t *testing.T) {
	t.Helper()
	tr, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	c, err := esihttp.New(esi.Options{
		BaseURL:    esi.DefaultBaseURL,
		CompatDate: CompatDate,
		UserAgent:  userAgent,
		Observe:    observe.New(),
	}, &nhttp.Client{Transport: tr}, mocks.QuietLogger(gomock.NewController(t)))
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Get(t.Context(), esi.Path("/status"), nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(res.Data)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("status body %s: %v", raw, err)
	}
	if _, ok := body["players"]; !ok {
		t.Fatalf("status missing players: %s", raw)
	}
}

func fixtureKey(spec Spec) string {
	f := Fixture{Method: spec.Method, Path: spec.Path, Query: spec.Query}

	return f.Key()
}
