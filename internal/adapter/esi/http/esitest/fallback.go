package esitest

import (
	"encoding/json"
	"errors"
	nhttp "net/http"
	"strings"
)

const (
	npcCorpID      = 1000009
	npcCorpTaxRate = 0.1
)

type Fallback struct {
	Inner *Transport
}

func (f *Fallback) RoundTrip(req *nhttp.Request) (*nhttp.Response, error) {
	if f == nil || f.Inner == nil {
		return nil, ErrNoFixture
	}
	resp, err := f.Inner.RoundTrip(req)
	if err == nil {
		return resp, nil
	}
	if !errors.Is(err, ErrNoFixture) {
		return nil, err
	}

	return generic(req).Response(), nil
}

func generic(req *nhttp.Request) Fixture {
	return Fixture{
		Method:     req.Method,
		Path:       req.URL.Path,
		Source:     SourceOpenAPI,
		CompatDate: CompatDate,
		Status:     nhttp.StatusOK,
		Headers: map[string]string{
			"Content-Type":             "application/json",
			"X-Compatibility-Date":     CompatDate,
			"X-Esi-Error-Limit-Remain": "100",
			"X-Esi-Error-Limit-Reset":  "60",
			"ETag":                     `"fallback"`,
			"Expires":                  "Wed, 19 Aug 2026 00:00:00 GMT",
		},
		Body: genericBody(req.URL.Path),
	}
}

func genericBody(path string) json.RawMessage {
	switch {
	case isCharacterSheet(path):
		return mustJSON(map[string]any{
			"name":            "Fixture Pilot",
			"corporation_id":  npcCorpID,
			"security_status": 0.0,
			"birthday":        "2010-01-01T00:00:00Z",
		})
	case isCorporationSheet(path):
		return mustJSON(map[string]any{
			"name":         "CBD Corporation",
			"ticker":       "CBD",
			"ceo_id":       1,
			"member_count": 1,
			"tax_rate":     npcCorpTaxRate,
		})
	case isWalletBalance(path):
		return json.RawMessage("1000000.0")
	case strings.HasSuffix(path, "/search"):
		return mustJSON(map[string]any{
			"inventory_type": []int{schemaRifterType},
			"solar_system":   []int{JitaSystem},
		})
	case strings.HasSuffix(path, "/skills"):
		return mustJSON(map[string]any{"skills": []any{}, "total_sp": 0})
	case strings.HasSuffix(path, "/clones"):
		return mustJSON(map[string]any{"jump_clones": []any{}})
	case isObjectPath(path):
		return json.RawMessage("{}")
	default:
		return json.RawMessage("[]")
	}
}

func isCharacterSheet(path string) bool {
	return pathDepth(path) == 2 && strings.HasPrefix(path, "/characters/")
}

func isCorporationSheet(path string) bool {
	return pathDepth(path) == 2 && strings.HasPrefix(path, "/corporations/")
}

func isWalletBalance(path string) bool {
	return strings.HasSuffix(path, "/wallet") && !strings.Contains(path, "/wallet/")
}

func isObjectPath(path string) bool {
	switch {
	case strings.Contains(path, "/universe/types/"),
		strings.Contains(path, "/universe/groups/"),
		strings.Contains(path, "/universe/systems/"),
		strings.Contains(path, "/universe/constellations/"),
		strings.HasSuffix(path, "/location"),
		strings.HasSuffix(path, "/ship"),
		strings.HasSuffix(path, "/online"),
		strings.HasSuffix(path, "/attributes"):
		return true
	default:
		return false
	}
}

func pathDepth(path string) int {
	n := 0
	for part := range strings.SplitSeq(strings.Trim(path, "/"), "/") {
		if part != "" {
			n++
		}
	}

	return n
}

func mustJSON(v any) json.RawMessage {
	raw, err := marshalBody(v)
	if err != nil {
		return json.RawMessage("{}")
	}

	return raw
}
