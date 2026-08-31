package esi

import (
	"context"
	"fmt"
	"time"
)

const DefaultBaseURL = "https://esi.evetech.net"

const (
	staleAfterMinute = 60.0
	staleAfterHour   = 3600.0
)

type Options struct {
	BaseURL        string
	UserAgent      string
	CompatDate     string
	MaxConcurrency int
}

type Error struct {
	Msg    string
	Status int
	Body   any
}

func (e Error) Error() string { return e.Msg }

// RateLimitedError is CCP's ESI error-limit (420) or HTTP 429. Do not retry
// before RetryAt — the bucket is shared for this server's public IP.
type RateLimitedError struct {
	Msg      string
	Status   int
	RetryAt  time.Time
	RetrySec int
	Remain   *int
	ResetSec *int
}

func (e RateLimitedError) Error() string { return e.Msg }

// UserLimitedError is this user's ESI request allowance. Do not retry
// before RetryAt — looping would burn the shared CCP error-limit.
type UserLimitedError struct {
	Msg      string
	RetryAt  time.Time
	RetrySec int
}

func (e UserLimitedError) Error() string { return e.Msg }

type Result struct {
	Data       any
	FromCache  bool
	AgeSeconds float64
	ExpiresAt  float64
	Pages      *int
	Truncated  bool
}

func (r Result) StaleNote() string {
	if r.AgeSeconds < staleAfterMinute {
		return fmt.Sprintf("%ds old", int(r.AgeSeconds))
	}
	if r.AgeSeconds < staleAfterHour {
		return fmt.Sprintf("%dm old", int(r.AgeSeconds/staleAfterMinute))
	}

	return fmt.Sprintf("%.1fh old", r.AgeSeconds/staleAfterHour)
}

type TokenSource interface {
	AccessToken(ctx context.Context, characterID int) (string, error)
}

type Client interface {
	Get(ctx context.Context, path string, characterID *int, params map[string]any, cacheTTL *float64) (Result, error)
	GetAllPages(ctx context.Context, path string, characterID *int, params map[string]any, maxPages int) (Result, error)
	GetCursorPages(ctx context.Context, path string, characterID *int, params map[string]any, cursorParam, cursorKey string, batchSize, maxPages int) (Result, error)
	Post(ctx context.Context, path string, characterID *int, params map[string]any, jsonBody any) (any, error)
	Put(ctx context.Context, path string, characterID *int, params map[string]any, jsonBody any) (any, error)
	Delete(ctx context.Context, path string, characterID *int, params map[string]any, jsonBody any) (any, error)
	ForUser(auth TokenSource) Client
}
