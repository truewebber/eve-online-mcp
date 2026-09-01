package eve

import (
	"testing"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/j"
)

func TestOldestAgeIsTheMaximum(t *testing.T) {
	t.Parallel()
	if got := oldestAge([]float64{5, 3600, 12}); got != 3600 {
		t.Fatalf("oldestAge %v", got)
	}
	if got := staleNote(5, 3600, 12); got != (esi.Result{AgeSeconds: 3600}).StaleNote() {
		t.Fatalf("staleNote %q", got)
	}
}

func TestOverviewDataAgeIsOldestSuccessfulFetch(t *testing.T) {
	t.Parallel()
	got := staleOf(
		overviewBox{r: esi.Result{AgeSeconds: 5}},
		overviewBox{err: errInner},
		overviewBox{r: esi.Result{AgeSeconds: 3600}},
	)
	if got != (esi.Result{AgeSeconds: 3600}).StaleNote() {
		t.Fatalf("%q", got)
	}
	if staleOf(overviewBox{err: errInner}) != "" {
		t.Fatal("failed fetches must not invent an age")
	}
}

func TestOverviewCarriesDataAge(t *testing.T) {
	t.Parallel()
	body := asMap(t, mustCall(t, func() (any, error) {
		return eveCharacterOverview(t.Context(), fixtureSession(t), empty{})
	}))
	if j.Str(body[fDataAge]) == "" {
		t.Fatalf("missing data_age: %+v", body)
	}
}

func TestClonesDataAgeIsOldest(t *testing.T) {
	t.Parallel()
	if got := staleNote(12, 3600); got != (esi.Result{AgeSeconds: 3600}).StaleNote() {
		t.Fatalf("%q", got)
	}
}

func mustCall(t *testing.T, fn func() (any, error)) any {
	t.Helper()
	got, err := fn()
	if err != nil {
		t.Fatal(err)
	}

	return got
}
