package http

import (
	"crypto/rand"
	"encoding/binary"
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
	"time"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/j"
)

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
	if resp.StatusCode >= nhttp.StatusBadRequest {
		c.budget.charge()
	}
}

func (c *Client) checkErrorBudget() error {
	return c.budget.check(c.currentRemain())
}

func (c *Client) currentRemain() int {
	c.errorMu.Lock()
	defer c.errorMu.Unlock()

	return c.errorRemain
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

func withPage(params map[string]any, page int) map[string]any {
	out := clone(params)
	if page < 1 {
		page = 1
	}
	out["page"] = page

	return out
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

func parseBase(raw string) (url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || !u.IsAbs() {
		return url.URL{}, errBaseURLRequired
	}

	return *u, nil
}
