package http

import (
	"errors"
	nhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/mocks"
)

func TestErrorBudgetTwentyThenRefuse(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		b := newErrorBudget()
		before := ErrorBudgetRejections.Load()
		for range errorBudgetCap {
			b.charge()
		}
		err := b.check(errorLimitBudget)
		var limited esi.UserLimitedError
		if !errors.As(err, &limited) {
			t.Fatalf("21st want UserLimitedError, got %v", err)
		}
		if !strings.Contains(limited.Msg, "error budget") {
			t.Fatalf("want error-budget message, got %q", limited.Msg)
		}
		if strings.Contains(limited.Msg, "request allowance") {
			t.Fatalf("looks like allowance: %q", limited.Msg)
		}
		if limited.RetrySec < 1 || !limited.RetryAt.After(time.Now()) {
			t.Fatalf("retry %+v", limited)
		}
		if ErrorBudgetRejections.Load() < before+1 {
			t.Fatalf("rejections %d", ErrorBudgetRejections.Load())
		}
	})
}

func TestErrorBudgetClampsToFifthOfRemainder(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		b := newErrorBudget()
		for range 9 {
			b.charge()
		}
		if err := b.check(50); err != nil {
			t.Fatalf("9 of 10 at remain 50: %v", err)
		}
		b.charge()
		if err := b.check(50); !errors.As(err, new(esi.UserLimitedError)) {
			t.Fatalf("10th charged, 11th at remain 50 want refuse, got %v", err)
		}
	})
}

func TestErrorBudgetWindowRolls(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		b := newErrorBudget()
		for range errorBudgetCap {
			b.charge()
		}
		if err := b.check(errorLimitBudget); err == nil {
			t.Fatal("full window should refuse")
		}
		time.Sleep(errorBudgetWindow)
		if err := b.check(errorLimitBudget); err != nil {
			t.Fatalf("after roll: %v", err)
		}
		b.charge()
		if b.count() != 1 {
			t.Fatalf("count %d", b.count())
		}
	})
}

func TestErrorBudgetIsolatesCharacters(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		a := newErrorBudget()
		b := newErrorBudget()
		for range errorBudgetCap {
			a.charge()
		}
		if err := a.check(errorLimitBudget); err == nil {
			t.Fatal("a should be spent")
		}
		if err := b.check(errorLimitBudget); err != nil {
			t.Fatalf("b must be untouched: %v", err)
		}
		b.charge()
		if a.count() != errorBudgetCap || b.count() != 1 {
			t.Fatalf("counts a=%d b=%d", a.count(), b.count())
		}
	})
}

func TestErrorBudgetClientCharges4xxNot2xx(t *testing.T) {
	t.Parallel()
	var hits int
	srv := httptest.NewServer(nhttp.HandlerFunc(func(w nhttp.ResponseWriter, _ *nhttp.Request) {
		hits++
		w.Header().Set("X-Esi-Error-Limit-Remain", "100")
		w.Header().Set("X-Esi-Error-Limit-Reset", "60")
		if hits == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "max-age=3600")
			if _, err := w.Write([]byte(`{"ok":true}`)); err != nil {
				t.Fatal(err)
			}

			return
		}
		w.WriteHeader(nhttp.StatusForbidden)
		if _, err := w.Write([]byte(`{"error":"no"}`)); err != nil {
			t.Fatal(err)
		}
	}))
	t.Cleanup(srv.Close)
	c := New(esi.Options{BaseURL: srv.URL, CompatDate: testCompatDate}, srv.Client(), mocks.QuietLogger(gomock.NewController(t)))
	if _, err := c.Get(t.Context(), "/ok", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if c.budget.count() != 0 {
		t.Fatalf("200 charged %d", c.budget.count())
	}
	if _, err := c.Get(t.Context(), "/ok", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Fatalf("cache hit left the pod, ESI calls %d", hits)
	}
	if c.budget.count() != 0 {
		t.Fatalf("cache hit charged %d", c.budget.count())
	}
	if _, err := c.Get(t.Context(), "/deny", nil, nil, nil); err == nil {
		t.Fatal("want 403")
	}
	if c.budget.count() != 1 {
		t.Fatalf("403 count %d", c.budget.count())
	}
}

func TestErrorBudgetCharges420And429(t *testing.T) {
	t.Parallel()
	c := New(esi.Options{CompatDate: testCompatDate}, nil, mocks.QuietLogger(gomock.NewController(t)))
	for _, status := range []int{statusErrorLimited, nhttp.StatusTooManyRequests} {
		resp := &nhttp.Response{StatusCode: status, Header: nhttp.Header{}}
		resp.Header.Set("X-Esi-Error-Limit-Remain", "80")
		resp.Header.Set("X-Esi-Error-Limit-Reset", "30")
		c.noteErrorHeaders(resp)
	}
	if c.budget.count() != 2 {
		t.Fatalf("420/429 charged %d", c.budget.count())
	}
}

func TestErrorBudgetClientNotModifiedNotCharged(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(nhttp.HandlerFunc(func(w nhttp.ResponseWriter, r *nhttp.Request) {
		if r.Header.Get("If-None-Match") == `"abc"` {
			w.Header().Set("X-Esi-Error-Limit-Remain", "100")
			w.WriteHeader(nhttp.StatusNotModified)

			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `"abc"`)
		w.Header().Set("Cache-Control", "max-age=0")
		w.Header().Set("X-Esi-Error-Limit-Remain", "100")
		if _, err := w.Write([]byte(`{"ok":true}`)); err != nil {
			t.Fatal(err)
		}
	}))
	t.Cleanup(srv.Close)
	c := New(esi.Options{BaseURL: srv.URL, CompatDate: testCompatDate}, srv.Client(), mocks.QuietLogger(gomock.NewController(t)))
	if _, err := c.Get(t.Context(), "/status", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(t.Context(), "/status", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if c.budget.count() != 0 {
		t.Fatalf("304 charged %d", c.budget.count())
	}
}

func TestErrorBudgetClientRefusesBeforeNetwork(t *testing.T) {
	t.Parallel()
	var hits int
	srv := httptest.NewServer(nhttp.HandlerFunc(func(w nhttp.ResponseWriter, _ *nhttp.Request) {
		hits++
		w.Header().Set("X-Esi-Error-Limit-Remain", "100")
		w.WriteHeader(nhttp.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	c := New(esi.Options{BaseURL: srv.URL, CompatDate: testCompatDate}, srv.Client(), mocks.QuietLogger(gomock.NewController(t)))
	for range errorBudgetCap {
		if _, err := c.Get(t.Context(), "/deny", nil, nil, nil); err == nil {
			t.Fatal("want 403")
		}
	}
	_, err := c.Get(t.Context(), "/deny", nil, nil, nil)
	if !errors.As(err, new(esi.UserLimitedError)) {
		t.Fatalf("over budget: %v", err)
	}
	if hits != errorBudgetCap {
		t.Fatalf("hits %d, refusal must not leave the pod", hits)
	}
}

func TestErrorBudgetClientCharactersIsolated(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(nhttp.HandlerFunc(func(w nhttp.ResponseWriter, _ *nhttp.Request) {
		w.Header().Set("X-Esi-Error-Limit-Remain", "100")
		w.WriteHeader(nhttp.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	base := New(esi.Options{BaseURL: srv.URL, CompatDate: testCompatDate}, srv.Client(), mocks.QuietLogger(gomock.NewController(t)))
	a, ok := base.ForUser(nil).(*Client)
	if !ok {
		t.Fatal("ForUser")
	}
	b, ok := base.ForUser(nil).(*Client)
	if !ok {
		t.Fatal("ForUser")
	}
	for range errorBudgetCap {
		if _, err := a.Get(t.Context(), "/deny", nil, nil, nil); err == nil {
			t.Fatal("want 403")
		}
	}
	if _, err := a.Get(t.Context(), "/deny", nil, nil, nil); !errors.As(err, new(esi.UserLimitedError)) {
		t.Fatalf("a over: %v", err)
	}
	if _, err := b.Get(t.Context(), "/deny", nil, nil, nil); err == nil {
		t.Fatal("b should still reach ESI")
	}
	if b.budget.count() != 1 {
		t.Fatalf("b count %d", b.budget.count())
	}
}

func TestGlobalFailFastStillWorks(t *testing.T) {
	t.Parallel()
	var hits int
	srv := httptest.NewServer(nhttp.HandlerFunc(func(w nhttp.ResponseWriter, _ *nhttp.Request) {
		hits++
		w.WriteHeader(nhttp.StatusOK)
	}))
	t.Cleanup(srv.Close)
	c := New(esi.Options{BaseURL: srv.URL, CompatDate: testCompatDate}, srv.Client(), mocks.QuietLogger(gomock.NewController(t)))
	c.errorRemain = 10
	c.errorResetAt = time.Now().Add(30 * time.Second)
	_, err := c.Get(t.Context(), "/status", nil, nil, nil)
	if !errors.As(err, new(esi.RateLimitedError)) {
		t.Fatalf("fail-fast: %v", err)
	}
	if hits != 0 {
		t.Fatalf("fail-fast left the pod, hits %d", hits)
	}
}

func TestAllowanceRejectionIncrements(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		b := newUserBucket()
		before := AllowanceRejections.Load()
		for range int(UserBucketCapacity) {
			if err := b.take(); err != nil {
				t.Fatal(err)
			}
		}
		if err := b.take(); !errors.As(err, new(esi.UserLimitedError)) {
			t.Fatalf("got %v", err)
		}
		if AllowanceRejections.Load() < before+1 {
			t.Fatalf("rejections %d", AllowanceRejections.Load())
		}
	})
}
