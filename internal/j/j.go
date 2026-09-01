package j

import (
	"encoding/json"
	"strconv"
)

func Map(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}

	return map[string]any{}
}

func Slice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}

	return nil
}

func Maps(v any) []map[string]any {
	raw := Slice(v)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}

	return out
}

func Get(m map[string]any, key string) any {
	if m == nil {
		return nil
	}

	return m[key]
}

func Str(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		return ""
	}
}

func Bool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	default:
		return false
	}
}

func Float(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case json.Number:
		f, err := t.Float64()
		if err != nil {
			return 0
		}

		return f
	case string:
		f, err := strconv.ParseFloat(t, 64)
		if err != nil {
			return 0
		}

		return f
	default:
		return 0
	}
}

func Int(v any) int {
	return int(Float(v))
}

func Int64(v any) int64 {
	return int64(Float(v))
}

func Has(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	_, ok := m[key]

	return ok
}
