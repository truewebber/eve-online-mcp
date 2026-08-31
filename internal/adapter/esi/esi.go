package esi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/adapter/sso"
	"github.com/truewebber/eve-online-mcp/internal/adapter/store"
	"github.com/truewebber/eve-online-mcp/internal/domain/j"
)

const DefaultBaseURL = "https://esi.evetech.net"

// Options is the ESI client config. Built at the composition root.
type Options struct {
	BaseURL        string
	UserAgent      string
	CompatDate     string
	MaxConcurrency int
}

const (
	errorFloor  = 15
	maxBackoff  = 60 * time.Second
	maxCacheTTL = 86400.0
)

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

type Result struct {
	Data       any
	FromCache  bool
	AgeSeconds float64
	ExpiresAt  float64
	Pages      *int
	Truncated  bool
}

func (r Result) StaleNote() string {
	if r.AgeSeconds < 60 {
		return fmt.Sprintf("%ds old", int(r.AgeSeconds))
	}
	if r.AgeSeconds < 3600 {
		return fmt.Sprintf("%dm old", int(r.AgeSeconds/60))
	}

	return fmt.Sprintf("%.1fh old", r.AgeSeconds/3600)
}

type httpCache interface {
	CacheGet(ctx context.Context, key string) (*store.CachedResponse, bool, error)
	CachePut(ctx context.Context, key string, c store.CachedResponse) error
	CacheTouch(ctx context.Context, key string, expiresAt time.Time) error
}

type Client struct {
	opts         Options
	http         *http.Client
	store        *store.Store
	sso          *sso.Client
	sem          chan struct{}
	errorRemain  int
	errorResetAt time.Time
	errorMu      sync.Mutex
	bucket       *userBucket
	// testCache, if set, replaces Postgres for HTTP cache (unit tests).
	testCache httpCache
}

func New(opts Options, httpClient *http.Client, db *store.Store, ssoClient *sso.Client) *Client {
	if opts.BaseURL == "" {
		opts.BaseURL = DefaultBaseURL
	}
	if opts.MaxConcurrency < 1 {
		opts.MaxConcurrency = 8
	}

	return &Client{
		opts:        opts,
		http:        httpClient,
		store:       db,
		sso:         ssoClient,
		sem:         make(chan struct{}, opts.MaxConcurrency),
		errorRemain: 100,
		bucket:      newUserBucket(),
	}
}

func (c *Client) cache() httpCache {
	if c.testCache != nil {
		return c.testCache
	}
	if c.store != nil {
		return c.store
	}

	return nil
}

func (c *Client) Get(path string, characterID *int, params map[string]any, cacheTTL *float64) (Result, error) {
	if params == nil {
		params = map[string]any{}
	}

	return c.cachedGet(path, characterID, params, cacheTTL)
}

func (c *Client) GetAllPages(path string, characterID *int, params map[string]any, maxPages int) (Result, error) {
	if params == nil {
		params = map[string]any{}
	}
	firstParams := clone(params)
	firstParams["page"] = 1
	first, err := c.cachedGet(path, characterID, firstParams, nil)
	if err != nil {
		return Result{}, err
	}
	total := 1
	if first.Pages != nil {
		total = *first.Pages
	}
	if total <= 1 || !isSlice(first.Data) {
		return first, nil
	}
	capped := min(total, maxPages)
	if capped < total {
		log.Printf("%s has %d pages, fetching first %d", path, total, capped)
	}
	type box struct {
		r   Result
		err error
	}
	ch := make(chan box, capped-1)
	for page := 2; page <= capped; page++ {
		p := clone(params)
		p["page"] = page
		go func() {
			r, err := c.cachedGet(path, characterID, p, nil)
			ch <- box{r, err}
		}()
	}
	data := append([]any{}, j.Slice(first.Data)...)
	oldest := first.AgeSeconds
	allCached := first.FromCache
	for range capped - 1 {
		b := <-ch
		if b.err != nil {
			return Result{}, b.err
		}
		if s := j.Slice(b.r.Data); s != nil {
			data = append(data, s...)
		}
		if b.r.AgeSeconds > oldest {
			oldest = b.r.AgeSeconds
		}
		allCached = allCached && b.r.FromCache
	}

	return Result{
		Data: data, FromCache: allCached, AgeSeconds: oldest,
		ExpiresAt: first.ExpiresAt, Pages: &total, Truncated: capped < total,
	}, nil
}

func (c *Client) GetCursorPages(path string, characterID *int, params map[string]any, cursorParam, cursorKey string, batchSize, maxPages int) (Result, error) {
	if params == nil {
		params = map[string]any{}
	}
	if maxPages < 1 {
		maxPages = 1
	}
	if batchSize < 1 {
		batchSize = 1
	}
	cursor := params[cursorParam]
	base := clone(params)
	var data []any
	seen := map[any]struct{}{}
	oldest := 0.0
	expiresAt := 0.0
	allCached := true
	fetched := 0
	truncated := false

	for index := range maxPages {
		q := clone(base)
		q[cursorParam] = cursor
		result, err := c.cachedGet(path, characterID, q, nil)
		if err != nil {
			return Result{}, err
		}
		fetched++
		if fetched == 1 {
			expiresAt = result.ExpiresAt
		}
		allCached = allCached && result.FromCache
		if result.AgeSeconds > oldest {
			oldest = result.AgeSeconds
		}
		rows := j.Maps(result.Data)
		if len(rows) == 0 {
			break
		}
		if len(rows) > batchSize {
			batchSize = len(rows)
		}
		var nextCursor any
		for _, row := range rows {
			marker := row[cursorKey]
			if marker != nil {
				if _, ok := seen[marker]; ok {
					continue
				}
				seen[marker] = struct{}{}
				if nextCursor == nil || lessAny(marker, nextCursor) {
					nextCursor = marker
				}
			}
			data = append(data, row)
		}
		if len(rows) < batchSize {
			break
		}
		if index == maxPages-1 {
			truncated = true

			break
		}
		if nextCursor == nil || (cursor != nil && !lessAny(nextCursor, cursor)) {
			log.Printf("%s: %s did not advance past %v; stopping", path, cursorParam, cursor)

			break
		}
		cursor = nextCursor
	}
	pages := fetched

	return Result{
		Data: data, FromCache: allCached, AgeSeconds: oldest,
		ExpiresAt: expiresAt, Pages: &pages, Truncated: truncated,
	}, nil
}

func (c *Client) Post(path string, characterID *int, params map[string]any, jsonBody any) (any, error) {
	return c.write(http.MethodPost, path, characterID, params, jsonBody)
}
func (c *Client) Put(path string, characterID *int, params map[string]any, jsonBody any) (any, error) {
	return c.write(http.MethodPut, path, characterID, params, jsonBody)
}
func (c *Client) Delete(path string, characterID *int, params map[string]any, jsonBody any) (any, error) {
	return c.write(http.MethodDelete, path, characterID, params, jsonBody)
}

func (c *Client) cacheKey(path string, characterID *int, params map[string]any) string {
	var cid any
	if characterID != nil {
		cid = *characterID
	}
	canonical, _ := json.Marshal(map[string]any{"p": path, "c": cid, "q": normalise(params), "d": c.opts.CompatDate})
	sum := sha256.Sum256(canonical)

	return hex.EncodeToString(sum[:])
}

func (c *Client) headers(characterID *int) (http.Header, error) {
	h := http.Header{}
	h.Set("User-Agent", c.opts.UserAgent)
	h.Set("X-Compatibility-Date", c.opts.CompatDate)
	h.Set("Accept", "application/json")
	if characterID != nil {
		token, err := c.sso.AccessToken(*characterID)
		if err != nil {
			return nil, err
		}
		h.Set("Authorization", "Bearer "+token.AccessToken)
	}

	return h, nil
}

func (c *Client) cachedGet(path string, characterID *int, params map[string]any, cacheTTL *float64) (Result, error) {
	ctx := context.Background()
	key := c.cacheKey(path, characterID, params)
	var cached *store.CachedResponse
	if cache := c.cache(); cache != nil {
		var err error
		cached, _, err = cache.CacheGet(ctx, key)
		if err != nil {
			return Result{}, err
		}
	}
	if cached != nil && cached.Fresh() {
		return Result{Data: cached.Data(), FromCache: true, AgeSeconds: cached.AgeSeconds(), ExpiresAt: cached.ExpiresUnix(), Pages: cached.Pages}, nil
	}
	h, err := c.headers(characterID)
	if err != nil {
		return Result{}, err
	}
	if cached != nil && cached.ETag != "" {
		h.Set("If-None-Match", cached.ETag)
	}
	resp, err := c.request(http.MethodGet, path, params, h, nil, 0)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))

	if resp.StatusCode == http.StatusNotModified && cached != nil {
		c.bucket.refund()
		expiresAt := expiresAt(resp, cacheTTL)
		if cache := c.cache(); cache != nil {
			_ = cache.CacheTouch(ctx, key, unixTime(expiresAt))
		}

		return Result{Data: cached.Data(), FromCache: true, AgeSeconds: 0, ExpiresAt: expiresAt, Pages: cached.Pages}, nil
	}
	if resp.StatusCode >= 400 {
		if cached != nil && resp.StatusCode >= 500 && resp.StatusCode < 600 {
			log.Printf("%s returned %d, serving stale cache", path, resp.StatusCode)

			return Result{Data: cached.Data(), FromCache: true, AgeSeconds: cached.AgeSeconds(), ExpiresAt: cached.ExpiresUnix(), Pages: cached.Pages}, nil
		}

		return Result{}, httpError(resp.StatusCode, bodyBytes, path)
	}
	decoded := decode(resp.StatusCode, bodyBytes)
	pages := intHeader(resp, "X-Pages")
	expires := expiresAt(resp, cacheTTL)
	raw, err := json.Marshal(decoded)
	if err != nil {
		return Result{}, err
	}
	if cache := c.cache(); cache != nil {
		_ = cache.CachePut(ctx, key, store.CachedResponse{
			Body: raw, ETag: resp.Header.Get("ETag"), ExpiresAt: unixTime(expires), Pages: pages,
		})
	}

	return Result{Data: decoded, FromCache: false, AgeSeconds: 0, ExpiresAt: expires, Pages: pages}, nil
}

func (c *Client) write(method, path string, characterID *int, params map[string]any, jsonBody any) (any, error) {
	if params == nil {
		params = map[string]any{}
	}
	h, err := c.headers(characterID)
	if err != nil {
		return nil, err
	}
	if jsonBody != nil {
		h.Set("Content-Type", "application/json")
	}
	resp, err := c.request(method, path, params, h, jsonBody, 0)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode >= 400 {
		return nil, httpError(resp.StatusCode, bodyBytes, path)
	}

	return decode(resp.StatusCode, bodyBytes), nil
}

func (c *Client) request(method, path string, params map[string]any, headers http.Header, jsonBody any, attempt int) (*http.Response, error) {
	if err := c.awaitErrorBudget(); err != nil {
		return nil, err
	}
	if err := c.bucket.take(); err != nil {
		return nil, err
	}
	u := c.opts.BaseURL + path
	if q := encodeParams(params); q != "" {
		u += "?" + q
	}
	var body io.Reader
	if jsonBody != nil {
		raw, err := json.Marshal(jsonBody)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header = headers.Clone()
	c.sem <- struct{}{}
	resp, err := c.http.Do(req)
	<-c.sem
	if err != nil {
		if attempt < 2 && safeToRetry(method, err) {
			time.Sleep(backoff(attempt))

			return c.request(method, path, params, headers, jsonBody, attempt+1)
		}
		if method != http.MethodGet {
			return nil, Error{Msg: fmt.Sprintf("Network error calling %s: %v. The request may or may not have reached EVE — check the current state with the matching read tool before trying again, because repeating it could apply the change twice.", path, err)}
		}

		return nil, Error{Msg: fmt.Sprintf("Network error calling %s: %v", path, err)}
	}
	c.noteErrorHeaders(resp)
	if resp.StatusCode == 420 || resp.StatusCode == http.StatusTooManyRequests {
		if resp.StatusCode == http.StatusTooManyRequests && attempt < 1 {
			wait := min(retryAfter(resp), 2*time.Second)
			log.Printf("%s throttled (%d); one short retry after %s", path, resp.StatusCode, wait)
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			time.Sleep(wait)

			return c.request(method, path, params, headers, jsonBody, attempt+1)
		}
		err := limitError(resp, path)
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		return nil, err
	}
	if (resp.StatusCode == http.StatusInternalServerError || resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusGatewayTimeout) && attempt < 2 && method == http.MethodGet {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		time.Sleep(backoff(attempt))

		return c.request(method, path, params, headers, jsonBody, attempt+1)
	}

	return resp, nil
}

func (c *Client) noteErrorHeaders(resp *http.Response) {
	remain := intHeader(resp, "X-Esi-Error-Limit-Remain")
	if remain == nil {
		return
	}
	reset := intHeader(resp, "X-Esi-Error-Limit-Reset")
	c.errorMu.Lock()
	defer c.errorMu.Unlock()
	c.errorRemain = *remain
	sec := 0
	if reset != nil {
		sec = *reset
	}
	c.errorResetAt = time.Now().Add(time.Duration(sec) * time.Second)
	if *remain < errorFloor {
		log.Printf("ESI error budget low: %d remaining, resets in %ds", *remain, sec)
	}
}

func (c *Client) awaitErrorBudget() error {
	c.errorMu.Lock()
	defer c.errorMu.Unlock()
	if c.errorRemain >= errorFloor {
		return nil
	}
	wait := time.Until(c.errorResetAt)
	if wait <= 0 {
		c.errorRemain = 100

		return nil
	}
	remain := c.errorRemain
	resetSec := max(int(wait.Seconds()+0.5), 1)
	retryAt := c.errorResetAt

	return RateLimitedError{
		Msg: fmt.Sprintf(
			"ESI error limit is nearly spent (%d errors left, resets in %ds). This server shares one public IP, so further calls now would lock out everyone. Wait until %s, then retry the same tool. Do not retry sooner.",
			remain, resetSec, retryAt.UTC().Format(time.RFC3339),
		),
		Status:   420,
		RetryAt:  retryAt,
		RetrySec: resetSec,
		Remain:   &remain,
		ResetSec: &resetSec,
	}
}

func normalise(params map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range params {
		if v == nil {
			continue
		}
		switch t := v.(type) {
		case bool:
			if t {
				out[k] = "true"
			} else {
				out[k] = "false"
			}
		case []string:
			out[k] = strings.Join(t, ",")
		case []int:
			parts := make([]string, len(t))
			for i, n := range t {
				parts[i] = strconv.Itoa(n)
			}
			out[k] = strings.Join(parts, ",")
		default:
			out[k] = v
		}
	}

	return out
}

func encodeParams(params map[string]any) string {
	q := url.Values{}
	for k, v := range normalise(params) {
		q.Set(k, fmt.Sprint(v))
	}

	return q.Encode()
}

func decode(status int, body []byte) any {
	if status == 204 || len(body) == 0 {
		return nil
	}
	var v any
	if json.Unmarshal(body, &v) == nil {
		return v
	}

	return string(body)
}

func intHeader(resp *http.Response, name string) *int {
	raw := resp.Header.Get(name)
	if raw == "" {
		return nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return nil
	}

	return &n
}

func expiresAt(resp *http.Response, fallback *float64) float64 {
	ttl := serverTTL(resp)
	if ttl == nil {
		ttl = maxAge(resp)
	}
	if ttl == nil {
		if fallback != nil {
			ttl = fallback
		} else {
			v := 60.0
			ttl = &v
		}
	}
	if *ttl < 0 {
		*ttl = 0
	}
	if *ttl > maxCacheTTL {
		*ttl = maxCacheTTL
	}

	return float64(time.Now().Unix()) + *ttl
}

func unixTime(unix float64) time.Time {
	return time.Unix(int64(unix), 0).UTC()
}

func headerDate(resp *http.Response, name string) *time.Time {
	raw := resp.Header.Get(name)
	if raw == "" {
		return nil
	}
	t, err := http.ParseTime(raw)
	if err != nil {
		return nil
	}

	return &t
}

func serverTTL(resp *http.Response) *float64 {
	exp := headerDate(resp, "Expires")
	if exp == nil {
		return nil
	}
	served := headerDate(resp, "Date")
	var ttl float64
	if served == nil {
		ttl = time.Until(*exp).Seconds()
	} else {
		ttl = exp.Sub(*served).Seconds()
	}

	return &ttl
}

func maxAge(resp *http.Response) *float64 {
	for part := range strings.SplitSeq(resp.Header.Get("Cache-Control"), ",") {
		part = strings.TrimSpace(part)
		if after, ok := strings.CutPrefix(part, "max-age="); ok {
			f, err := strconv.ParseFloat(after, 64)
			if err != nil {
				return nil
			}

			return &f
		}
	}

	return nil
}

func retryAfter(resp *http.Response) time.Duration {
	raw := resp.Header.Get("Retry-After")
	if raw == "" {
		return 10 * time.Second
	}
	if sec, err := strconv.ParseFloat(raw, 64); err == nil {
		if sec > 0 {
			return time.Duration(sec * float64(time.Second))
		}

		return 10 * time.Second
	}
	if when, err := http.ParseTime(raw); err == nil {
		served := headerDate(resp, "Date")
		base := time.Now()
		if served != nil {
			base = *served
		}
		d := when.Sub(base)
		if d > 0 {
			return d
		}
	}

	return 10 * time.Second
}

func safeToRetry(method string, err error) bool {
	if method == http.MethodGet {
		return true
	}
	if errorAsNet(err) {
		return true
	}
	if strings.Contains(err.Error(), "connection refused") || strings.Contains(err.Error(), "no such host") {
		return true
	}

	return false
}

func errorAsNet(err error) bool {
	var op *net.OpError

	return errorAs(err, &op)
}

func errorAs(err error, target **net.OpError) bool {
	for err != nil {
		op := &net.OpError{}
		if errors.As(err, &op) {
			*target = op

			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}

	return false
}

func backoff(attempt int) time.Duration {
	base := min(time.Duration(1<<attempt)*time.Second, 8*time.Second)

	return time.Duration(float64(base) * (0.5 + rand.Float64()/2))
}

func httpError(status int, body []byte, path string) Error {
	decoded := decode(status, body)
	detail := ""
	if m := j.Map(decoded); m != nil {
		if e := j.Str(m["error"]); e != "" {
			detail = e
		}
	}
	if detail == "" && decoded != nil {
		raw, _ := json.Marshal(decoded)
		if len(raw) > 300 {
			raw = raw[:300]
		}
		detail = string(raw)
	}
	if detail == "" {
		if len(body) > 300 {
			detail = string(body[:300])
		} else {
			detail = string(body)
		}
	}
	hints := map[int]string{
		401: "the access token was rejected — the character may need to log in again",
		403: "missing scope or in-game role for this endpoint",
		404: "not found (wrong id, or the character has no such data)",
	}
	msg := fmt.Sprintf("ESI %d on %s: %s", status, path, detail)
	if h, ok := hints[status]; ok {
		msg += " (" + h + ")"
	}

	return Error{Msg: msg, Status: status, Body: decoded}
}

func limitError(resp *http.Response, path string) RateLimitedError {
	retry := retryAfter(resp)
	remain := intHeader(resp, "X-Esi-Error-Limit-Remain")
	reset := intHeader(resp, "X-Esi-Error-Limit-Reset")
	if reset != nil {
		fromReset := time.Duration(*reset) * time.Second
		if fromReset > retry {
			retry = fromReset
		}
	}
	if retry <= 0 {
		retry = 10 * time.Second
	}
	retryAt := time.Now().Add(retry)
	sec := max(int(retry.Seconds()+0.5), 1)
	remainN := 0
	if remain != nil {
		remainN = *remain
	}
	resetN := sec
	if reset != nil {
		resetN = *reset
	}

	return RateLimitedError{
		Msg: fmt.Sprintf(
			"ESI %d on %s: error limit exceeded (%d remaining, reset in %ds). Wait until %s, then retry the same tool. Do not retry sooner — this IP is shared.",
			resp.StatusCode, path, remainN, resetN, retryAt.UTC().Format(time.RFC3339),
		),
		Status:   resp.StatusCode,
		RetryAt:  retryAt,
		RetrySec: sec,
		Remain:   remain,
		ResetSec: reset,
	}
}

func clone(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	maps.Copy(out, m)

	return out
}

func isSlice(v any) bool {
	_, ok := v.([]any)

	return ok
}

func lessAny(a, b any) bool {
	return j.Float(a) < j.Float(b)
}
