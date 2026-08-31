package esitest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	nhttp "net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
)

const (
	recordTimeout = 30 * time.Second
	maxBody       = 16 << 20
	userAgent     = "eve-mcp-test"
)

var errNeedAccessToken = errors.New("esitest: authenticated fixture needs ESI_ACCESS_TOKEN")

func Record(ctx context.Context, spec Spec, token string) (Fixture, error) {
	u, err := esiURL(spec.Path, spec.Query)
	if err != nil {
		return Fixture{}, err
	}
	var body io.Reader
	if spec.Body != nil {
		raw, err := json.Marshal(spec.Body)
		if err != nil {
			return Fixture{}, fmt.Errorf("esitest: encode body: %w", err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := nhttp.NewRequestWithContext(ctx, spec.Method, u.String(), body)
	if err != nil {
		return Fixture{}, fmt.Errorf("esitest: request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", recordAgent())
	req.Header.Set("X-Compatibility-Date", CompatDate)
	if spec.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if spec.Auth {
		if token == "" {
			return Fixture{}, fmt.Errorf("%s %s: %w", spec.Method, spec.Path, errNeedAccessToken)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &nhttp.Client{Timeout: recordTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return Fixture{}, fmt.Errorf("esitest: %s %s: %w", spec.Method, spec.Path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return Fixture{}, fmt.Errorf("esitest: read %s %s: %w", spec.Method, spec.Path, err)
	}

	return Fixture{
		Method:     spec.Method,
		Path:       spec.Path,
		Query:      spec.Query,
		Source:     SourceRecorded,
		CompatDate: CompatDate,
		Status:     resp.StatusCode,
		Headers:    keepHeaders(resp.Header),
		Body:       compactJSON(raw),
	}, nil
}

func AccessToken() string {
	return os.Getenv("ESI_ACCESS_TOKEN")
}

func esiURL(esiPath string, query map[string]string) (*url.URL, error) {
	u, err := url.Parse(esi.DefaultBaseURL)
	if err != nil {
		return nil, fmt.Errorf("esitest: base url: %w", err)
	}
	u = u.JoinPath(strings.TrimPrefix(esiPath, "/"))
	if len(query) == 0 {
		return u, nil
	}
	q := url.Values{}
	for k, v := range query {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()

	return u, nil
}

func recordAgent() string {
	if contact := os.Getenv("CONTACT"); contact != "" {
		return userAgent + " " + contact
	}

	return userAgent
}

func keepHeaders(h nhttp.Header) map[string]string {
	drop := map[string]struct{}{
		"Connection":        {},
		"Keep-Alive":        {},
		"Transfer-Encoding": {},
		"Te":                {},
		"Trailer":           {},
		"Upgrade":           {},
		"Content-Encoding":  {},
		"Content-Length":    {},
	}
	out := map[string]string{}
	for k, vs := range h {
		if _, skip := drop[nhttp.CanonicalHeaderKey(k)]; skip {
			continue
		}
		if len(vs) == 0 || vs[0] == "" {
			continue
		}
		out[k] = vs[0]
	}

	return out
}

func compactJSON(raw []byte) json.RawMessage {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	if json.Valid(raw) {
		var buf bytes.Buffer
		if err := json.Compact(&buf, raw); err == nil {
			return buf.Bytes()
		}

		return raw
	}

	enc, err := json.Marshal(string(raw))
	if err != nil {
		return json.RawMessage("null")
	}

	return enc
}
