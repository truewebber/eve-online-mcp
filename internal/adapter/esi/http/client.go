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
)

var errNoTokenSource = errors.New("authenticated ESI call without a token source")

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

func New(opts esi.Options, httpClient *nhttp.Client, logger log.Logger) *Client {
	if opts.BaseURL == "" {
		opts.BaseURL = esi.DefaultBaseURL
	}
	if opts.MaxConcurrency < 1 {
		opts.MaxConcurrency = 8
	}

	return &Client{
		opts:        opts,
		base:        esiBase(opts.BaseURL),
		http:        ownHTTP(httpClient),
		cache:       newResponseCache(),
		sem:         make(chan struct{}, opts.MaxConcurrency),
		errorRemain: errorLimitBudget,
		bucket:      newUserBucket(),
		budget:      newErrorBudget(),
		logger:      logger,
		observe:     opts.Observe,
	}
}

func ownHTTP(c *nhttp.Client) *nhttp.Client {
	if c == nil {
		return &nhttp.Client{}
	}
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
		sem:         make(chan struct{}, c.opts.MaxConcurrency),
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
