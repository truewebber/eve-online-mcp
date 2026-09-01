package http

import (
	"context"
	"errors"
	nhttp "net/http"
	"net/url"
	"sync"
	"time"

	"github.com/truewebber/gopkg/log"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
)

const (
	errorFloor  = 15
	maxCacheTTL = 86400.0

	errorLimitBudget   = 100
	maxResponseBody    = 16 << 20
	errorSnippet       = 300
	defaultRetryAfter  = 10 * time.Second
	statusErrorLimited = 420
	roundHalf          = 0.5
	backoffCap         = 8 * time.Second
	throttleRetryCap   = 2 * time.Second
	jitterFloor        = 0.5
	maxConcurrency     = 8
)

var (
	errNoTokenSource     = errors.New("authenticated ESI call without a token source")
	errBaseURLRequired   = errors.New("esi: base URL is required")
	errUserAgentRequired = errors.New("esi: user agent is required")
	errCompatRequired    = errors.New("esi: compatibility date is required")
	errHTTPRequired      = errors.New("esi: http client is required")
	errLoggerRequired    = errors.New("esi: logger is required")
	errObserveRequired   = errors.New("esi: observer is required")
	errClientRequired    = errors.New("esi: client is required")
)

type Client struct {
	opts         esi.Options
	base         url.URL
	http         *nhttp.Client
	auth         esi.TokenSource
	cache        *responseCache
	sem          chan struct{}
	errorRemain  int
	errorResetAt time.Time
	errorMu      sync.Mutex
	bucket       *userBucket
	budget       *errorBudget
	logger       log.Logger
	observe      esi.Observer
}

func New(opts esi.Options, httpClient *nhttp.Client, logger log.Logger) (*Client, error) {
	base, err := parseBase(opts.BaseURL)
	if err != nil {
		return nil, err
	}
	if opts.UserAgent == "" {
		return nil, errUserAgentRequired
	}
	if opts.CompatDate == "" {
		return nil, errCompatRequired
	}
	if httpClient == nil {
		return nil, errHTTPRequired
	}
	if logger == nil {
		return nil, errLoggerRequired
	}
	if opts.Observe == nil {
		return nil, errObserveRequired
	}

	return &Client{
		opts:        opts,
		base:        base,
		http:        ownHTTP(httpClient),
		cache:       newResponseCache(),
		sem:         make(chan struct{}, maxConcurrency),
		errorRemain: errorLimitBudget,
		bucket:      newUserBucket(),
		budget:      newErrorBudget(),
		logger:      logger,
		observe:     opts.Observe,
	}, nil
}

func ownHTTP(c *nhttp.Client) *nhttp.Client {
	cp := *c

	return &cp
}

func (c *Client) ForUser(auth esi.TokenSource) esi.Client {
	return &Client{
		opts:        c.opts,
		base:        c.base,
		http:        c.http,
		auth:        auth,
		cache:       c.cache,
		sem:         make(chan struct{}, maxConcurrency),
		errorRemain: errorLimitBudget,
		bucket:      newUserBucket(),
		budget:      newErrorBudget(),
		logger:      c.logger,
		observe:     c.observe,
	}
}

func (c *Client) Get(ctx context.Context, path string, characterID *int, params map[string]any, cacheTTL *float64) (esi.Result, error) {
	if params == nil {
		params = map[string]any{}
	}

	return c.cachedGet(ctx, path, characterID, params, cacheTTL)
}

func (c *Client) Post(ctx context.Context, path string, characterID *int, params map[string]any, jsonBody any) (any, error) {
	return c.write(ctx, writeCall{method: nhttp.MethodPost, path: path, characterID: characterID, params: params, jsonBody: jsonBody})
}

func (c *Client) Put(ctx context.Context, path string, characterID *int, params map[string]any, jsonBody any) (any, error) {
	return c.write(ctx, writeCall{method: nhttp.MethodPut, path: path, characterID: characterID, params: params, jsonBody: jsonBody})
}

func (c *Client) Delete(ctx context.Context, path string, characterID *int, params map[string]any, jsonBody any) (any, error) {
	return c.write(ctx, writeCall{method: nhttp.MethodDelete, path: path, characterID: characterID, params: params, jsonBody: jsonBody})
}
