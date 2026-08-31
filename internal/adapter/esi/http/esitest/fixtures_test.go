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
)

var update = flag.Bool("update", false, "record fixtures from live ESI") //nolint:gochecknoglobals // golden-file -update flag

func TestFixtures(t *testing.T) { //nolint:paralleltest // -update writes testdata and must not race
	if *update {
		updateFixtures(t)
	}
	assertCatalog(t)
	assertStatusRoundTrip(t)
}

func updateFixtures(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	dir := Testdata()
	token := AccessToken()
	for _, spec := range Catalog() {
		f, err := resolveFixture(ctx, spec, token)
		if err != nil {
			t.Fatalf("%s %s: %v", spec.Method, spec.Path, err)
		}
		if err := Write(dir, f); err != nil {
			t.Fatal(err)
		}
	}
}

func resolveFixture(ctx context.Context, spec Spec, token string) (Fixture, error) {
	if spec.Status == statusErrorLimited || (spec.Auth && token == "") {
		return SchemaFixture(spec)
	}
	if spec.Status == statusForbidden {
		got, err := Record(ctx, spec, "")
		if err != nil || got.Status != statusForbidden {
			return SchemaFixture(spec)
		}

		return got, nil
	}
	if spec.Auth {
		return Record(ctx, spec, token)
	}

	return Record(ctx, spec, "")
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
	c := esihttp.New(esi.Options{
		BaseURL:    esi.DefaultBaseURL,
		CompatDate: CompatDate,
		UserAgent:  userAgent,
	}, &nhttp.Client{Transport: tr}, mocks.QuietLogger(gomock.NewController(t)))
	res, err := c.Get(t.Context(), "/status", nil, nil, nil)
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
