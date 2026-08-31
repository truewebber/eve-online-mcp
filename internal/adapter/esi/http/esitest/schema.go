package esitest

import (
	"encoding/json"
	"fmt"
	nhttp "net/http"
)

const (
	schemaWalletISK   = 1_000_000.0
	schemaItemID1     = 1_000_000_000_001
	schemaItemID2     = 1_000_000_000_002
	schemaItemID3     = 1_000_000_000_003
	schemaRifterType  = 587
	schemaJitaStation = 60003760
	schemaStackSmall  = 50
	schemaStackLarge  = 100
)

func SchemaFixture(spec Spec) (Fixture, error) {
	body, err := schemaBody(spec)
	if err != nil {
		return Fixture{}, err
	}
	status := spec.Status
	if status == 0 {
		status = nhttp.StatusOK
	}

	return Fixture{
		Method:     spec.Method,
		Path:       spec.Path,
		Query:      spec.Query,
		Source:     SourceOpenAPI,
		CompatDate: CompatDate,
		Status:     status,
		Headers:    schemaHeaders(spec, status),
		Body:       body,
	}, nil
}

func schemaHeaders(spec Spec, status int) map[string]string {
	h := map[string]string{
		"Content-Type":             "application/json",
		"X-Compatibility-Date":     CompatDate,
		"X-Esi-Error-Limit-Remain": "100",
		"X-Esi-Error-Limit-Reset":  "60",
		"ETag":                     `"openapi"`,
		"Cache-Control":            "public",
		"Expires":                  "Wed, 19 Aug 2026 00:00:00 GMT",
	}
	if spec.Query["page"] != "" || spec.Path == Path("characters", id(FixtureCharacterID), "assets") {
		h["X-Pages"] = "2"
	}
	if status == statusErrorLimited {
		h["X-Esi-Error-Limit-Remain"] = "0"
		h["X-Esi-Error-Limit-Reset"] = "15"
		h["Retry-After"] = "15"
	}

	return h
}

func schemaBody(spec Spec) (json.RawMessage, error) {
	switch {
	case spec.Status == statusForbidden:
		return marshalBody(map[string]any{"error": "Forbidden"})
	case spec.Status == statusErrorLimited:
		return marshalBody(map[string]any{"error": "This software has exceeded the error limit for ESI."})
	case spec.Path == Path("characters", id(FixtureCharacterID), "wallet"):
		return marshalBody(schemaWalletISK)
	case spec.Path == Path("characters", id(FixtureCharacterID), "assets"):
		return marshalBody(schemaAssets(spec.Query["page"]))
	case spec.Path == Path("characters", id(FixtureCharacterID), "mail"):
		return marshalBody(schemaMail())
	default:
		return nil, errNoSchema(spec)
	}
}

func schemaAssets(page string) []map[string]any {
	item := func(itemID, typeID, locationID, qty int) map[string]any {
		return map[string]any{
			"item_id":       itemID,
			"type_id":       typeID,
			"location_id":   locationID,
			"location_type": "station",
			"location_flag": "Hangar",
			"quantity":      qty,
			"is_singleton":  false,
		}
	}
	if page == "2" {
		return []map[string]any{item(schemaItemID3, TritaniumType, schemaJitaStation, schemaStackSmall)}
	}

	return []map[string]any{
		item(schemaItemID1, TritaniumType, schemaJitaStation, schemaStackLarge),
		item(schemaItemID2, schemaRifterType, schemaJitaStation, 1),
	}
}

func schemaMail() []map[string]any {
	return []map[string]any{
		{
			"mail_id":   1,
			"subject":   "example",
			"from":      PublicCharacterID,
			"timestamp": "2026-08-18T00:00:00Z",
			"is_read":   false,
			"labels":    []int{1},
			"recipients": []map[string]any{
				{"recipient_id": FixtureCharacterID, "recipient_type": "character"},
			},
		},
	}
}

func marshalBody(v any) (json.RawMessage, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("esitest: schema: %w", err)
	}

	return raw, nil
}

type schemaError struct {
	method, path string
}

func errNoSchema(spec Spec) error {
	return schemaError{method: spec.Method, path: spec.Path}
}

func (e schemaError) Error() string {
	return "esitest: no openapi fixture for " + e.method + " " + e.path
}
