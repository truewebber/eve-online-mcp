package http

import (
	"sync"
	"time"
)

type accessMem struct {
	AccessToken     string
	AccessExpiresAt time.Time
}

// Access tokens live 20 minutes and are re-derivable from the session
// refresh token (DB.md). Keyed by refresh token, never by character id.
type accessCache struct {
	mu sync.Mutex
	by map[string]accessMem
}

func newAccessCache() *accessCache {
	return &accessCache{by: map[string]accessMem{}}
}

func (s *accessCache) get(refreshToken string) (accessMem, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mem, ok := s.by[refreshToken]

	return mem, ok
}

func (s *accessCache) put(refreshToken string, mem accessMem) {
	if refreshToken == "" || mem.AccessToken == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.by[refreshToken] = mem
}

func (s *accessCache) drop(refreshToken string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.by, refreshToken)
}

func (s *accessCache) live(refreshToken string, margin time.Duration) (accessMem, bool) {
	mem, ok := s.get(refreshToken)
	if !ok || mem.AccessToken == "" {
		return accessMem{}, false
	}
	if time.Now().After(mem.AccessExpiresAt.Add(-margin)) {
		return accessMem{}, false
	}

	return mem, true
}
