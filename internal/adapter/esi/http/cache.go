package http

import (
	"container/list"
	"encoding/json"
	"sync"
	"time"
)

const (
	maxCacheEntries = 2000
	maxCacheBytes   = 256 << 20
	maxCachedBody   = 8 << 20
)

type cachedResponse struct {
	Body      []byte
	ETag      string
	ExpiresAt time.Time
	StoredAt  time.Time
	Pages     *int
}

func (c *cachedResponse) Fresh() bool {
	return time.Now().Before(c.ExpiresAt)
}

func (c *cachedResponse) AgeSeconds() float64 {
	age := time.Since(c.StoredAt).Seconds()
	if age < 0 {
		return 0
	}

	return age
}

func (c *cachedResponse) ExpiresUnix() float64 {
	return float64(c.ExpiresAt.Unix())
}

func (c *cachedResponse) Data() any {
	if c == nil || len(c.Body) == 0 {
		return nil
	}
	var v any
	if json.Unmarshal(c.Body, &v) != nil {
		return nil
	}

	return v
}

type cacheItem struct {
	key string
	val cachedResponse
}

type responseCache struct {
	mu    sync.Mutex
	ll    *list.List
	items map[string]*list.Element
	bytes int
}

func newResponseCache() *responseCache {
	return &responseCache{ll: list.New(), items: map[string]*list.Element{}}
}

func (c *responseCache) get(key string) *cachedResponse {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return nil
	}
	item, ok := el.Value.(*cacheItem)
	if !ok {
		return nil
	}
	c.ll.MoveToFront(el)
	val := item.val

	return &val
}

func (c *responseCache) put(key string, val cachedResponse) {
	if len(val.Body) > maxCachedBody {
		return
	}
	if val.StoredAt.IsZero() {
		val.StoredAt = time.Now().UTC()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		item, ok := el.Value.(*cacheItem)
		if !ok {
			return
		}
		c.bytes -= len(item.val.Body)
		item.val = val
		c.bytes += len(val.Body)
		c.ll.MoveToFront(el)
	} else {
		c.items[key] = c.ll.PushFront(&cacheItem{key: key, val: val})
		c.bytes += len(val.Body)
	}
	c.evictLocked()
}

func (c *responseCache) touch(key string, expiresAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return
	}
	item, ok := el.Value.(*cacheItem)
	if !ok {
		return
	}
	item.val.ExpiresAt = expiresAt
	item.val.StoredAt = time.Now().UTC()
	c.ll.MoveToFront(el)
}

func (c *responseCache) evictLocked() {
	for (c.ll.Len() > maxCacheEntries || c.bytes > maxCacheBytes) && c.ll.Back() != nil {
		el := c.ll.Back()
		item, ok := el.Value.(*cacheItem)
		if !ok {
			c.ll.Remove(el)

			continue
		}
		c.ll.Remove(el)
		delete(c.items, item.key)
		c.bytes -= len(item.val.Body)
	}
}

func (c *responseCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.ll.Len()
}

func (c *responseCache) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.bytes
}
