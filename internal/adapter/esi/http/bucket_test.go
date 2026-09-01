package http

import (
	"encoding/json"
	"errors"
	nhttp "net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
)

const (
	testCompatDate = "2026-08-18"
	testUserAgent  = "eve-mcp-test"
)

func TestUserBucketExhausted(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		b := newUserBucket()
		now := time.Now()
		for i := range int(UserBucketCapacity) {
			err := b.take()
			if err != nil {
				t.Fatalf("take %d: %v", i, err)
			}
		}
		err := b.take()
		var limited esi.UserLimitedError
		if !errors.As(err, &limited) {
			t.Fatalf("401st want UserLimitedError, got %v", err)
		}
		if limited.RetrySec < 1 {
			t.Fatalf("retry_sec %d", limited.RetrySec)
		}
		if !limited.RetryAt.After(now) {
			t.Fatalf("retry_at %s", limited.RetryAt)
		}
	})
}

func TestUserBucketRefill(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		b := newUserBucket()
		for range int(UserBucketCapacity) {
			err := b.take()
			if err != nil {
				t.Fatal(err)
			}
		}
		time.Sleep(time.Second)
		err := b.take()
		if err != nil {
			t.Fatalf("first after refill: %v", err)
		}
		err = b.take()
		if err != nil {
			t.Fatalf("second after refill: %v", err)
		}
		err = b.take()
		if err == nil {
			t.Fatal("third after 1s refill should fail")
		}
	})
}

func TestFreshCacheHitDoesNotTakeToken(t *testing.T) {
	t.Parallel()
	var hits int
	srv := httptest.NewServer(nhttp.HandlerFunc(func(w nhttp.ResponseWriter, _ *nhttp.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "max-age=3600")
		if _, err := w.Write([]byte(`{"players":1}`)); err != nil {
			t.Fatal(err)
		}
	}))
	t.Cleanup(srv.Close)
	c := mustClient(t, testOptions(srv.URL), srv.Client())
	if _, err := c.Get(t.Context(), "/status", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	afterWarm := c.bucket.remaining()
	res, err := c.Get(t.Context(), "/status", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.FromCache {
		t.Fatalf("want cache hit, got %+v", res)
	}
	if hits != 1 {
		t.Fatalf("ESI hits %d", hits)
	}
	if got := c.bucket.remaining(); got < afterWarm {
		t.Fatalf("cache hit spent a token: %v → %v", afterWarm, got)
	}
}

func TestNetworkGetTakesToken(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(nhttp.HandlerFunc(func(w nhttp.ResponseWriter, _ *nhttp.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"ok":true}`)); err != nil {
			t.Fatal(err)
		}
	}))
	t.Cleanup(srv.Close)
	c := mustClient(t, testOptions(srv.URL), srv.Client())
	if _, err := c.Get(t.Context(), "/status", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := c.bucket.remaining(); got >= UserBucketCapacity {
		t.Fatalf("tokens %v, network GET must consume a token", got)
	}
	c2 := mustClient(t, testOptions(srv.URL), srv.Client())
	for c2.bucket.remaining() >= 1 {
		err := c2.bucket.take()
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := c2.Get(t.Context(), "/other", nil, nil, nil)
	if !errors.As(err, new(esi.UserLimitedError)) {
		t.Fatalf("empty bucket Get: %v", err)
	}
}

func TestNotModifiedRefundsToken(t *testing.T) {
	t.Parallel()
	var hits int
	srv := httptest.NewServer(nhttp.HandlerFunc(func(w nhttp.ResponseWriter, r *nhttp.Request) {
		hits++
		if r.Header.Get("If-None-Match") == `"abc"` {
			w.WriteHeader(nhttp.StatusNotModified)

			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `"abc"`)
		w.Header().Set("Cache-Control", "max-age=0")
		if _, err := w.Write([]byte(`{"players":1}`)); err != nil {
			t.Fatal(err)
		}
	}))
	t.Cleanup(srv.Close)
	c := mustClient(t, testOptions(srv.URL), srv.Client())
	if _, err := c.Get(t.Context(), "/status", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	afterWarm := c.bucket.remaining()
	res, err := c.Get(t.Context(), "/status", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.FromCache {
		t.Fatalf("want 304 from cache, got %+v", res)
	}
	if hits != 2 {
		t.Fatalf("hits %d", hits)
	}
	if got := c.bucket.remaining(); got < afterWarm {
		t.Fatalf("304 spent a token: %v → %v", afterWarm, got)
	}
}

func TestCacheSharedAcrossForUser(t *testing.T) {
	t.Parallel()
	var hits int
	srv := httptest.NewServer(nhttp.HandlerFunc(func(w nhttp.ResponseWriter, _ *nhttp.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "max-age=3600")
		if _, err := w.Write([]byte(`{"players":1}`)); err != nil {
			t.Fatal(err)
		}
	}))
	t.Cleanup(srv.Close)
	base := mustClient(t, testOptions(srv.URL), srv.Client())
	a := base.ForUser(nil)
	b := base.ForUser(nil)
	if _, err := a.Get(t.Context(), "/status", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	res, err := b.Get(t.Context(), "/status", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.FromCache {
		t.Fatalf("second character should hit shared cache, got %+v", res)
	}
	if hits != 1 {
		t.Fatalf("ESI hits %d", hits)
	}
}

func TestOversizedBodyIsServedAndNotStored(t *testing.T) {
	t.Parallel()
	var hits int
	body := make([]byte, maxCachedBody+1)
	for i := range body {
		body[i] = 'a'
	}
	payload, err := json.Marshal(string(body))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(nhttp.HandlerFunc(func(w nhttp.ResponseWriter, _ *nhttp.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "max-age=3600")
		if _, err := w.Write(payload); err != nil {
			t.Fatal(err)
		}
	}))
	t.Cleanup(srv.Close)
	c := mustClient(t, testOptions(srv.URL), srv.Client())
	res, err := c.Get(t.Context(), "/status", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.FromCache || res.Data == nil {
		t.Fatalf("want served body, got %+v", res)
	}
	if _, err := c.Get(t.Context(), "/status", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if hits != 2 {
		t.Fatalf("oversized body must not be cached, hits %d", hits)
	}
	if c.cache.len() != 0 {
		t.Fatalf("cache entries %d", c.cache.len())
	}
}
