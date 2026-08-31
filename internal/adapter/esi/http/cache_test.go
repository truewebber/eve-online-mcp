package http

import (
	"fmt"
	"testing"
	"time"
)

func TestEntryCeilingEvictsOldest(t *testing.T) {
	t.Parallel()
	c := newResponseCache()
	first := "k0"
	c.put(first, cachedResponse{Body: []byte("0"), ExpiresAt: time.Now().Add(time.Hour)})
	for i := 1; i <= maxCacheEntries; i++ {
		c.put(fmt.Sprintf("k%d", i), cachedResponse{
			Body: []byte("x"), ExpiresAt: time.Now().Add(time.Hour),
		})
	}
	if c.len() > maxCacheEntries {
		t.Fatalf("len %d", c.len())
	}
	if c.get(first) != nil {
		t.Fatal("oldest entry should be evicted")
	}
}

func TestByteCeilingEvictsBeforeEntryCeiling(t *testing.T) {
	t.Parallel()
	c := newResponseCache()
	big := make([]byte, maxCachedBody)
	n := maxCacheBytes/maxCachedBody + 1
	c.put("first", cachedResponse{Body: big, ExpiresAt: time.Now().Add(time.Hour)})
	for i := 1; i < n; i++ {
		c.put(fmt.Sprintf("k%d", i), cachedResponse{Body: big, ExpiresAt: time.Now().Add(time.Hour)})
	}
	if c.get("first") != nil {
		t.Fatal("first large body should be evicted by the byte ceiling")
	}
	if c.len() > maxCacheEntries {
		t.Fatalf("len %d", c.len())
	}
	if c.size() > maxCacheBytes {
		t.Fatalf("bytes %d", c.size())
	}
}
