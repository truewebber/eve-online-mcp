package esi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/adapter/store"
)

const testCompatDate = "2026-08-18"

func TestUserBucketExhausted(t *testing.T) {
	t.Parallel()
	b := newUserBucket()
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	b.now = func() time.Time { return now }
	for i := range int(UserBucketCapacity) {
		err := b.take()
		if err != nil {
			t.Fatalf("take %d: %v", i, err)
		}
	}
	err := b.take()
	var limited UserLimitedError
	if !errors.As(err, &limited) {
		t.Fatalf("401st want UserLimitedError, got %v", err)
	}
	if limited.RetrySec < 1 {
		t.Fatalf("retry_sec %d", limited.RetrySec)
	}
	if !limited.RetryAt.After(now) {
		t.Fatalf("retry_at %s", limited.RetryAt)
	}
}

func TestUserBucketRefill(t *testing.T) {
	t.Parallel()
	b := newUserBucket()
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	b.now = func() time.Time { return now }
	for range int(UserBucketCapacity) {
		err := b.take()
		if err != nil {
			t.Fatal(err)
		}
	}
	now = now.Add(time.Second)
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
}

func TestFreshCacheHitDoesNotTakeToken(t *testing.T) {
	t.Parallel()
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits++
		t.Error("cache hit must not call ESI")
	}))
	t.Cleanup(srv.Close)

	c := New(Options{BaseURL: srv.URL, CompatDate: testCompatDate}, srv.Client(), nil, nil)
	now := time.Now()
	c.bucket.now = func() time.Time { return now }
	c.testCache = &memCache{m: map[string]*store.CachedResponse{
		mustCacheKey(t, c, "/status", nil, map[string]any{}): {
			Body:      json.RawMessage(`{"players":1}`),
			ExpiresAt: time.Now().Add(time.Hour),
			StoredAt:  time.Now(),
		},
	}}
	res, err := c.Get(t.Context(), "/status", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.FromCache {
		t.Fatalf("want cache hit, got %+v", res)
	}
	if hits != 0 {
		t.Fatalf("ESI hits %d", hits)
	}
	if got := c.bucket.remaining(); got != UserBucketCapacity {
		t.Fatalf("tokens %v, want %v", got, UserBucketCapacity)
	}
}

func TestNetworkGetTakesToken(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"ok":true}`)); err != nil {
			t.Fatal(err)
		}
	}))
	t.Cleanup(srv.Close)
	c := New(Options{BaseURL: srv.URL, CompatDate: testCompatDate}, srv.Client(), nil, nil)
	now := time.Now()
	c.bucket.now = func() time.Time { return now }
	if _, err := c.Get(t.Context(), "/status", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := c.bucket.remaining(); got != UserBucketCapacity-1 {
		t.Fatalf("tokens %v", got)
	}
	for c.bucket.remaining() >= 1 {
		err := c.bucket.take()
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := c.Get(t.Context(), "/status", nil, nil, nil)
	if !errors.As(err, new(UserLimitedError)) {
		t.Fatalf("empty bucket Get: %v", err)
	}
}

func TestNotModifiedRefundsToken(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") != `"abc"` {
			t.Errorf("If-None-Match %q", r.Header.Get("If-None-Match"))
		}
		w.WriteHeader(http.StatusNotModified)
	}))
	t.Cleanup(srv.Close)
	c := New(Options{BaseURL: srv.URL, CompatDate: testCompatDate}, srv.Client(), nil, nil)
	now := time.Now()
	c.bucket.now = func() time.Time { return now }
	c.testCache = &memCache{m: map[string]*store.CachedResponse{
		mustCacheKey(t, c, "/status", nil, map[string]any{}): {
			Body:      json.RawMessage(`{"players":1}`),
			ETag:      `"abc"`,
			ExpiresAt: time.Now().Add(-time.Minute),
			StoredAt:  time.Now().Add(-time.Hour),
		},
	}}
	res, err := c.Get(t.Context(), "/status", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.FromCache {
		t.Fatalf("want 304 from cache, got %+v", res)
	}
	if got := c.bucket.remaining(); got != UserBucketCapacity {
		t.Fatalf("304 should be free, tokens %v", got)
	}
}

type memCache struct {
	mu sync.Mutex
	m  map[string]*store.CachedResponse
}

func (m *memCache) CacheGet(_ context.Context, key string) (*store.CachedResponse, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.m[key]
	if c == nil {
		return nil, false, nil
	}
	cp := *c

	return &cp, true, nil
}

func (m *memCache) CachePut(_ context.Context, key string, c store.CachedResponse) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.m == nil {
		m.m = map[string]*store.CachedResponse{}
	}
	cp := c
	m.m[key] = &cp

	return nil
}

func (m *memCache) CacheTouch(_ context.Context, key string, expiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c := m.m[key]; c != nil {
		c.ExpiresAt = expiresAt
	}

	return nil
}

func mustCacheKey(t *testing.T, c *Client, path string, characterID *int, params map[string]any) string {
	t.Helper()
	key, err := c.cacheKey(path, characterID, params)
	if err != nil {
		t.Fatal(err)
	}

	return key
}
