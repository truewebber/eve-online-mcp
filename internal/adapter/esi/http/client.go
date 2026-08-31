package http

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	nhttp "net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/truewebber/gopkg/log"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/domain/j"
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
	http         *nhttp.Client
	auth         esi.TokenSource
	cache        *responseCache
	sem          chan struct{}
	errorRemain  int
	errorResetAt time.Time
	errorMu      sync.Mutex
	bucket       *userBucket
	logger       log.Logger
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
		http:        httpClient,
		cache:       newResponseCache(),
		sem:         make(chan struct{}, opts.MaxConcurrency),
		errorRemain: errorLimitBudget,
		bucket:      newUserBucket(),
		logger:      logger,
	}
}

func (c *Client) ForUser(auth esi.TokenSource) esi.Client {
	return &Client{
		opts:        c.opts,
		http:        c.http,
		auth:        auth,
		cache:       c.cache,
		sem:         make(chan struct{}, c.opts.MaxConcurrency),
		errorRemain: errorLimitBudget,
		bucket:      newUserBucket(),
		logger:      c.logger,
	}
}

func (c *Client) Get(ctx context.Context, path string, characterID *int, params map[string]any, cacheTTL *float64) (esi.Result, error) {
	if params == nil {
		params = map[string]any{}
	}

	return c.cachedGet(ctx, path, characterID, params, cacheTTL)
}

func (c *Client) GetAllPages(ctx context.Context, path string, characterID *int, params map[string]any, maxPages int) (esi.Result, error) {
	if params == nil {
		params = map[string]any{}
	}
	firstParams := clone(params)
	firstParams["page"] = 1
	first, err := c.cachedGet(ctx, path, characterID, firstParams, nil)
	if err != nil {
		return esi.Result{}, err
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
		c.logger.Info("esi: capping pages", "path", path, "total", total, "capped", capped)
	}
	type box struct {
		r   esi.Result
		err error
	}
	ch := make(chan box, capped-1)
	for page := 2; page <= capped; page++ {
		p := clone(params)
		p["page"] = page
		go func() {
			r, err := c.cachedGet(ctx, path, characterID, p, nil)
			ch <- box{r, err}
		}()
	}
	data := append([]any{}, j.Slice(first.Data)...)
	oldest := first.AgeSeconds
	allCached := first.FromCache
	for range capped - 1 {
		b := <-ch
		if b.err != nil {
			return esi.Result{}, b.err
		}
		if s := j.Slice(b.r.Data); s != nil {
			data = append(data, s...)
		}
		if b.r.AgeSeconds > oldest {
			oldest = b.r.AgeSeconds
		}
		allCached = allCached && b.r.FromCache
	}

	return esi.Result{
		Data: data, FromCache: allCached, AgeSeconds: oldest,
		ExpiresAt: first.ExpiresAt, Pages: &total, Truncated: capped < total,
	}, nil
}

type cursorWalk struct {
	path, cursorParam, cursorKey string
	characterID                  *int
	batchSize, maxPages          int
	cursor                       any
	base                         map[string]any
	data                         []any
	seen                         map[any]struct{}
	oldest, expiresAt            float64
	allCached                    bool
	fetched                      int
	truncated                    bool
}

func (c *Client) GetCursorPages(ctx context.Context, path string, characterID *int, params map[string]any, cursorParam, cursorKey string, batchSize, maxPages int) (esi.Result, error) {
	if params == nil {
		params = map[string]any{}
	}
	if maxPages < 1 {
		maxPages = 1
	}
	if batchSize < 1 {
		batchSize = 1
	}
	walk := cursorWalk{
		path: path, characterID: characterID,
		cursorParam: cursorParam, cursorKey: cursorKey,
		batchSize: batchSize, maxPages: maxPages,
		cursor: params[cursorParam], base: clone(params),
		seen: map[any]struct{}{}, allCached: true,
	}
	for index := range maxPages {
		cont, err := c.stepCursor(ctx, &walk, index)
		if err != nil {
			return esi.Result{}, err
		}
		if !cont {
			break
		}
	}
	pages := walk.fetched

	return esi.Result{
		Data: walk.data, FromCache: walk.allCached, AgeSeconds: walk.oldest,
		ExpiresAt: walk.expiresAt, Pages: &pages, Truncated: walk.truncated,
	}, nil
}

func (c *Client) Post(ctx context.Context, path string, characterID *int, params map[string]any, jsonBody any) (any, error) {
	return c.write(ctx, nhttp.MethodPost, path, characterID, params, jsonBody)
}

func (c *Client) Put(ctx context.Context, path string, characterID *int, params map[string]any, jsonBody any) (any, error) {
	return c.write(ctx, nhttp.MethodPut, path, characterID, params, jsonBody)
}

func (c *Client) Delete(ctx context.Context, path string, characterID *int, params map[string]any, jsonBody any) (any, error) {
	return c.write(ctx, nhttp.MethodDelete, path, characterID, params, jsonBody)
}

func (c *Client) stepCursor(ctx context.Context, w *cursorWalk, index int) (bool, error) {
	q := clone(w.base)
	q[w.cursorParam] = w.cursor
	result, err := c.cachedGet(ctx, w.path, w.characterID, q, nil)
	if err != nil {
		return false, err
	}
	w.fetched++
	if w.fetched == 1 {
		w.expiresAt = result.ExpiresAt
	}
	w.allCached = w.allCached && result.FromCache
	if result.AgeSeconds > w.oldest {
		w.oldest = result.AgeSeconds
	}
	rows := j.Maps(result.Data)
	if len(rows) == 0 {
		return false, nil
	}
	if len(rows) > w.batchSize {
		w.batchSize = len(rows)
	}
	nextCursor := mergeCursorRows(w, rows)
	if len(rows) < w.batchSize {
		return false, nil
	}
	if index == w.maxPages-1 {
		w.truncated = true

		return false, nil
	}
	if nextCursor == nil || (w.cursor != nil && !lessAny(nextCursor, w.cursor)) {
		c.logger.Info("esi: cursor did not advance", "path", w.path, "cursor", w.cursorParam, "at", w.cursor)

		return false, nil
	}
	w.cursor = nextCursor

	return true, nil
}

func mergeCursorRows(w *cursorWalk, rows []map[string]any) any {
	var nextCursor any
	for _, row := range rows {
		marker := row[w.cursorKey]
		if marker != nil {
			if _, ok := w.seen[marker]; ok {
				continue
			}
			w.seen[marker] = struct{}{}
			if nextCursor == nil || lessAny(marker, nextCursor) {
				nextCursor = marker
			}
		}
		w.data = append(w.data, row)
	}

	return nextCursor
}

func (c *Client) cacheKey(path string, characterID *int, params map[string]any) (string, error) {
	var cid any
	if characterID != nil {
		cid = *characterID
	}
	canonical, err := json.Marshal(map[string]any{"p": path, "c": cid, "q": normalise(params), "d": c.opts.CompatDate})
	if err != nil {
		return "", wrap("cacheKey", err)
	}
	sum := sha256.Sum256(canonical)

	return hex.EncodeToString(sum[:]), nil
}

func (c *Client) headers(ctx context.Context, characterID *int) (nhttp.Header, error) {
	h := nhttp.Header{}
	h.Set("User-Agent", c.opts.UserAgent)
	h.Set("X-Compatibility-Date", c.opts.CompatDate)
	h.Set("Accept", "application/json")
	if characterID != nil {
		if c.auth == nil {
			return nil, wrap("headers", errNoTokenSource)
		}
		token, err := c.auth.AccessToken(ctx, *characterID)
		if err != nil {
			return nil, wrap("headers", err)
		}
		h.Set("Authorization", "Bearer "+token)
	}

	return h, nil
}

func (c *Client) cachedGet(ctx context.Context, path string, characterID *int, params map[string]any, cacheTTL *float64) (esi.Result, error) {
	key, err := c.cacheKey(path, characterID, params)
	if err != nil {
		return esi.Result{}, err
	}
	cached := c.cache.get(key)
	if cached != nil && cached.Fresh() {
		return esi.Result{Data: cached.Data(), FromCache: true, AgeSeconds: cached.AgeSeconds(), ExpiresAt: cached.ExpiresUnix(), Pages: cached.Pages}, nil
	}
	h, err := c.headers(ctx, characterID)
	if err != nil {
		return esi.Result{}, err
	}
	if cached != nil && cached.ETag != "" {
		h.Set("If-None-Match", cached.ETag)
	}
	resp, err := c.request(ctx, nhttp.MethodGet, path, params, h, nil, 0)
	if err != nil {
		return esi.Result{}, err
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return esi.Result{}, wrap("cachedGet", err)
	}

	if resp.StatusCode == nhttp.StatusNotModified && cached != nil {
		c.bucket.refund()
		expiresAt := expiresAt(resp, cacheTTL)
		c.cache.touch(key, unixTime(expiresAt))

		return esi.Result{Data: cached.Data(), FromCache: true, AgeSeconds: 0, ExpiresAt: expiresAt, Pages: cached.Pages}, nil
	}
	if resp.StatusCode >= nhttp.StatusBadRequest {
		if cached != nil && resp.StatusCode >= 500 && resp.StatusCode < 600 {
			c.logger.Info("esi: serving stale cache", "path", path, "status", resp.StatusCode)

			return esi.Result{Data: cached.Data(), FromCache: true, AgeSeconds: cached.AgeSeconds(), ExpiresAt: cached.ExpiresUnix(), Pages: cached.Pages}, nil
		}

		return esi.Result{}, httpError(resp.StatusCode, bodyBytes, path)
	}
	decoded := decode(resp.StatusCode, bodyBytes)
	pages := intHeader(resp, "X-Pages")
	expires := expiresAt(resp, cacheTTL)
	raw, err := json.Marshal(decoded)
	if err != nil {
		return esi.Result{}, wrap("cachedGet", err)
	}
	c.cache.put(key, cachedResponse{
		Body: raw, ETag: resp.Header.Get("ETag"), ExpiresAt: unixTime(expires), Pages: pages,
	})

	return esi.Result{Data: decoded, FromCache: false, AgeSeconds: 0, ExpiresAt: expires, Pages: pages}, nil
}

func (c *Client) write(ctx context.Context, method, path string, characterID *int, params map[string]any, jsonBody any) (any, error) {
	if params == nil {
		params = map[string]any{}
	}
	h, err := c.headers(ctx, characterID)
	if err != nil {
		return nil, err
	}
	if jsonBody != nil {
		h.Set("Content-Type", "application/json")
	}
	resp, err := c.request(ctx, method, path, params, h, jsonBody, 0)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return nil, wrap("write", err)
	}
	if resp.StatusCode >= nhttp.StatusBadRequest {
		return nil, httpError(resp.StatusCode, bodyBytes, path)
	}

	return decode(resp.StatusCode, bodyBytes), nil
}

func (c *Client) endpoint(esiPath string, params map[string]any) (*url.URL, error) {
	base, err := url.Parse(c.opts.BaseURL)
	if err != nil {
		return nil, wrap("endpoint", err)
	}
	u := base.JoinPath(strings.TrimPrefix(esiPath, "/"))
	if q := encodeParams(params); q != "" {
		u.RawQuery = q
	}

	return u, nil
}

func (c *Client) request(ctx context.Context, method, path string, params map[string]any, headers nhttp.Header, jsonBody any, attempt int) (*nhttp.Response, error) {
	if err := c.awaitErrorBudget(); err != nil {
		return nil, err
	}
	if err := c.bucket.take(); err != nil {
		return nil, err
	}
	u, err := c.endpoint(path, params)
	if err != nil {
		return nil, err
	}
	var body io.Reader
	if jsonBody != nil {
		raw, err := json.Marshal(jsonBody)
		if err != nil {
			return nil, wrap("request", err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := nhttp.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, wrap("request", err)
	}
	req.Header = headers.Clone()
	c.sem <- struct{}{}
	resp, err := c.http.Do(req)
	<-c.sem
	if err != nil {
		if attempt < 2 && safeToRetry(method, err) {
			time.Sleep(backoff(attempt))

			return c.request(ctx, method, path, params, headers, jsonBody, attempt+1)
		}
		if method != nhttp.MethodGet {
			return nil, esi.Error{Msg: fmt.Sprintf("Network error calling %s: %v. The request may or may not have reached EVE — check the current state with the matching read tool before trying again, because repeating it could apply the change twice.", path, err)}
		}

		return nil, esi.Error{Msg: fmt.Sprintf("Network error calling %s: %v", path, err)}
	}
	c.noteErrorHeaders(resp)
	if resp.StatusCode == statusErrorLimited || resp.StatusCode == nhttp.StatusTooManyRequests {
		if resp.StatusCode == nhttp.StatusTooManyRequests && attempt < 1 {
			wait := min(retryAfter(resp), throttleRetryCap)
			c.logger.Info("esi: throttled, retrying", "path", path, "status", resp.StatusCode, "wait", wait)
			if err := discardBody(resp); err != nil {
				return nil, err
			}
			time.Sleep(wait)

			return c.request(ctx, method, path, params, headers, jsonBody, attempt+1)
		}
		err := limitError(resp, path)
		if discErr := discardBody(resp); discErr != nil {
			return nil, discErr
		}

		return nil, err
	}
	if (resp.StatusCode == nhttp.StatusInternalServerError || resp.StatusCode == nhttp.StatusBadGateway || resp.StatusCode == nhttp.StatusServiceUnavailable || resp.StatusCode == nhttp.StatusGatewayTimeout) && attempt < 2 && method == nhttp.MethodGet {
		if err := discardBody(resp); err != nil {
			return nil, err
		}
		time.Sleep(backoff(attempt))

		return c.request(ctx, method, path, params, headers, jsonBody, attempt+1)
	}

	return resp, nil
}

func discardBody(resp *nhttp.Response) error {
	_, err := io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	return wrap("discardBody", err)
}

func (c *Client) noteErrorHeaders(resp *nhttp.Response) {
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
		c.logger.Info("esi: error budget low", "remaining", *remain, "reset_sec", sec)
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
		c.errorRemain = errorLimitBudget

		return nil
	}
	remain := c.errorRemain
	resetSec := max(int(wait.Seconds()+roundHalf), 1)
	retryAt := c.errorResetAt

	return esi.RateLimitedError{
		Msg: fmt.Sprintf(
			"ESI error limit is nearly spent (%d errors left, resets in %ds). This server shares one public IP, so further calls now would lock out everyone. Wait until %s, then retry the same tool. Do not retry sooner.",
			remain, resetSec, retryAt.UTC().Format(time.RFC3339),
		),
		Status:   statusErrorLimited,
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

func intHeader(resp *nhttp.Response, name string) *int {
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

func expiresAt(resp *nhttp.Response, fallback *float64) float64 {
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

func headerDate(resp *nhttp.Response, name string) *time.Time {
	raw := resp.Header.Get(name)
	if raw == "" {
		return nil
	}
	t, err := nhttp.ParseTime(raw)
	if err != nil {
		return nil
	}

	return &t
}

func serverTTL(resp *nhttp.Response) *float64 {
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

func maxAge(resp *nhttp.Response) *float64 {
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

func retryAfter(resp *nhttp.Response) time.Duration {
	raw := resp.Header.Get("Retry-After")
	if raw == "" {
		return defaultRetryAfter
	}
	if sec, err := strconv.ParseFloat(raw, 64); err == nil {
		if sec > 0 {
			return time.Duration(sec * float64(time.Second))
		}

		return defaultRetryAfter
	}
	if when, err := nhttp.ParseTime(raw); err == nil {
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

	return defaultRetryAfter
}

func safeToRetry(method string, err error) bool {
	if method == nhttp.MethodGet {
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
	base := min(time.Duration(1<<attempt)*time.Second, backoffCap)
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return base
	}
	frac := float64(binary.BigEndian.Uint64(b[:])) / float64(^uint64(0))

	return time.Duration(float64(base) * (jitterFloor * (1 + frac)))
}

func httpError(status int, body []byte, path string) esi.Error {
	decoded := decode(status, body)
	detail := ""
	if m := j.Map(decoded); m != nil {
		if e := j.Str(m["error"]); e != "" {
			detail = e
		}
	}
	if detail == "" && decoded != nil {
		raw, err := json.Marshal(decoded)
		if err == nil {
			if len(raw) > errorSnippet {
				raw = raw[:errorSnippet]
			}
			detail = string(raw)
		}
	}
	if detail == "" {
		if len(body) > errorSnippet {
			detail = string(body[:errorSnippet])
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

	return esi.Error{Msg: msg, Status: status, Body: decoded}
}

func limitError(resp *nhttp.Response, path string) esi.RateLimitedError {
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
		retry = defaultRetryAfter
	}
	retryAt := time.Now().Add(retry)
	sec := max(int(retry.Seconds()+roundHalf), 1)
	remainN := 0
	if remain != nil {
		remainN = *remain
	}
	resetN := sec
	if reset != nil {
		resetN = *reset
	}

	return esi.RateLimitedError{
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
