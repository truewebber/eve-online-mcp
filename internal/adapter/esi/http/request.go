package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	nhttp "net/http"
	"net/url"
	"strings"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
)

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

func cachedResult(cached *cachedResponse, fromCache bool, age float64) esi.Result {
	return esi.Result{
		Data: cached.Data(), FromCache: fromCache, AgeSeconds: age,
		ExpiresAt: cached.ExpiresUnix(), Pages: cached.Pages,
	}
}

func (c *Client) cachedGet(ctx context.Context, path esi.Route, characterID *int, params map[string]any, cacheTTL *float64) (esi.Result, error) {
	key, err := c.cacheKey(path.String(), characterID, params)
	if err != nil {
		return esi.Result{}, err
	}
	cached := c.cache.get(key)
	if cached != nil && cached.Fresh() {
		return cachedResult(cached, true, cached.AgeSeconds()), nil
	}
	if err := c.checkErrorBudget(); err != nil {
		return esi.Result{}, err
	}
	h, err := c.headers(ctx, characterID)
	if err != nil {
		return esi.Result{}, err
	}
	if cached != nil && cached.ETag != "" {
		h.Set("If-None-Match", cached.ETag)
	}
	resp, err := c.request(ctx, httpCall{method: nhttp.MethodGet, route: path, params: params, headers: h})
	if err != nil {
		return esi.Result{}, err
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return esi.Result{}, wrap("cachedGet", err)
	}

	return c.finishGet(resp, bodyBytes, key, cached, path.String(), cacheTTL)
}

func (c *Client) finishGet(resp *nhttp.Response, body []byte, key string, cached *cachedResponse, path string, cacheTTL *float64) (esi.Result, error) {
	if resp.StatusCode == nhttp.StatusNotModified && cached != nil {
		c.bucket.refund()
		expiresAt := expiresAt(resp, cacheTTL)
		c.cache.touch(key, unixTime(expiresAt))

		return esi.Result{Data: cached.Data(), FromCache: true, AgeSeconds: 0, ExpiresAt: expiresAt, Pages: cached.Pages}, nil
	}
	if resp.StatusCode >= nhttp.StatusBadRequest {
		if cached != nil && resp.StatusCode >= 500 && resp.StatusCode < 600 {
			c.logger.Info("esi: serving stale cache", "path", path, "status", resp.StatusCode)

			return cachedResult(cached, true, cached.AgeSeconds()), nil
		}

		return esi.Result{}, httpError(resp.StatusCode, body, path)
	}
	decoded := decode(resp.StatusCode, body)
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

type writeCall struct {
	method      string
	route       esi.Route
	characterID *int
	params      map[string]any
	jsonBody    any
}

func (c *Client) write(ctx context.Context, in writeCall) (any, error) {
	if in.params == nil {
		in.params = map[string]any{}
	}
	if err := c.checkErrorBudget(); err != nil {
		return nil, err
	}
	h, err := c.headers(ctx, in.characterID)
	if err != nil {
		return nil, err
	}
	if in.jsonBody != nil {
		h.Set("Content-Type", "application/json")
	}
	resp, err := c.request(ctx, httpCall{method: in.method, route: in.route, params: in.params, headers: h, jsonBody: in.jsonBody})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return nil, wrap("write", err)
	}
	if resp.StatusCode >= nhttp.StatusBadRequest {
		return nil, httpError(resp.StatusCode, bodyBytes, in.route.String())
	}

	return decode(resp.StatusCode, bodyBytes), nil
}

func (c *Client) endpoint(esiPath string, params map[string]any) *url.URL {
	u := c.base
	out := u.JoinPath(strings.TrimPrefix(esiPath, "/"))
	if q := encodeParams(params); q != "" {
		out.RawQuery = q
	}

	return out
}

type httpCall struct {
	method   string
	route    esi.Route
	params   map[string]any
	headers  nhttp.Header
	jsonBody any
	attempt  int
}

func (c *Client) request(ctx context.Context, in httpCall) (*nhttp.Response, error) {
	if err := c.awaitErrorBudget(); err != nil {
		return nil, err
	}
	if err := c.bucket.take(); err != nil {
		return nil, err
	}
	u := c.endpoint(in.route.String(), in.params)
	var body io.Reader
	if in.jsonBody != nil {
		raw, err := json.Marshal(in.jsonBody)
		if err != nil {
			return nil, wrap("request", err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := nhttp.NewRequestWithContext(ctx, in.method, u.String(), body)
	if err != nil {
		return nil, wrap("request", err)
	}
	req.Header = in.headers.Clone()
	req.Pattern = in.route.Pattern()
	c.sem <- struct{}{}
	start := time.Now()
	resp, err := c.http.Do(req)
	<-c.sem
	if err != nil {
		c.observeRequest(in.method, 0, req, time.Since(start))

		return c.retryOrNetErr(ctx, in, err)
	}
	c.noteErrorHeaders(resp)
	c.observeRequest(in.method, resp.StatusCode, req, time.Since(start))
	if retry, err := c.throttleOrLimit(&in, resp); retry {
		return c.request(ctx, in)
	} else if err != nil {
		return nil, err
	}
	if serverRetry(in, resp.StatusCode) {
		if err := discardBody(resp); err != nil {
			return nil, err
		}
		time.Sleep(backoff(in.attempt))
		in.attempt++

		return c.request(ctx, in)
	}

	return resp, nil
}

func (c *Client) retryOrNetErr(ctx context.Context, in httpCall, err error) (*nhttp.Response, error) {
	if in.attempt < 2 && safeToRetry(in.method, err) {
		time.Sleep(backoff(in.attempt))
		in.attempt++

		return c.request(ctx, in)
	}
	if in.method != nhttp.MethodGet {
		return nil, esi.Error{Msg: fmt.Sprintf("Network error calling %s: %v. The request may or may not have reached EVE — check the current state with the matching read tool before trying again, because repeating it could apply the change twice.", in.route.String(), err)}
	}

	return nil, esi.Error{Msg: fmt.Sprintf("Network error calling %s: %v", in.route.String(), err)}
}

func (c *Client) throttleOrLimit(in *httpCall, resp *nhttp.Response) (bool, error) {
	if resp.StatusCode != statusErrorLimited && resp.StatusCode != nhttp.StatusTooManyRequests {
		return false, nil
	}
	if resp.StatusCode == nhttp.StatusTooManyRequests && in.attempt < 1 {
		wait := min(retryAfter(resp), throttleRetryCap)
		c.logger.Info("esi: throttled, retrying", "path", in.route.String(), "status", resp.StatusCode, "wait", wait)
		if err := discardBody(resp); err != nil {
			return false, err
		}
		time.Sleep(wait)
		in.attempt++

		return true, nil
	}
	err := limitError(resp, in.route.String())
	if discErr := discardBody(resp); discErr != nil {
		return false, discErr
	}

	return false, err
}

func serverRetry(in httpCall, status int) bool {
	if in.attempt >= 2 || in.method != nhttp.MethodGet {
		return false
	}

	return status == nhttp.StatusInternalServerError ||
		status == nhttp.StatusBadGateway ||
		status == nhttp.StatusServiceUnavailable ||
		status == nhttp.StatusGatewayTimeout
}
