package esi

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	ErrAllowanceSpent = errors.New("esi: request allowance spent")
	ErrBudgetSpent    = errors.New("esi: error budget spent")
)

const DefaultBaseURL = "https://esi.evetech.net"

const (
	staleAfterMinute = 60.0
	staleAfterHour   = 3600.0
)

type Observer interface {
	Request(method string, status int, path string, d time.Duration)
}

type Options struct {
	BaseURL    string
	UserAgent  string
	CompatDate string
	Observe    Observer
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

// UserLimitedError is this character's ESI request allowance or error
// budget. Reason is ErrAllowanceSpent or ErrBudgetSpent; do not retry
// before RetryAt.
type UserLimitedError struct {
	Msg      string
	RetryAt  time.Time
	RetrySec int
	Reason   error
}

func (e UserLimitedError) Error() string { return e.Msg }

func (e UserLimitedError) Unwrap() error { return e.Reason }

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

func (r Result) PageCount() int {
	if r.Pages != nil && *r.Pages > 0 {
		return *r.Pages
	}

	return 1
}

//go:generate go tool go.uber.org/mock/mockgen -destination=../../mocks/esi.go -package=mocks -mock_names=Client=MockESIClient,TokenSource=MockESITokenSource,Observer=MockESIObserver github.com/truewebber/eve-online-mcp/internal/adapter/esi Client,TokenSource,Observer
type TokenSource interface {
	AccessToken(ctx context.Context, characterID int) (string, error)
}

type CursorQuery struct {
	CharacterID *int
	Params      map[string]any
	CursorParam string
	CursorKey   string
	BatchSize   int
	MaxPages    int
}

type Client interface {
	Get(ctx context.Context, path Route, characterID *int, params map[string]any, cacheTTL *float64) (Result, error)
	GetAllPages(ctx context.Context, path Route, characterID *int, params map[string]any, maxPages int) (Result, error)
	GetCursorPages(ctx context.Context, path Route, q CursorQuery) (Result, error)
	Post(ctx context.Context, path Route, characterID *int, params map[string]any, jsonBody any) (any, error)
	Put(ctx context.Context, path Route, characterID *int, params map[string]any, jsonBody any) (any, error)
	Delete(ctx context.Context, path Route, characterID *int, params map[string]any, jsonBody any) (any, error)
	ForUser(auth TokenSource) Client
}
