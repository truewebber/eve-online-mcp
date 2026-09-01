package eve

import (
	"slices"
	"strings"
)

func enumInvariant(allowed ...string) string {
	return "must be one of " + strings.Join(allowed, ", ")
}

func pickEnum(field, raw, fallback string, allowed ...string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return fallback, nil
	}
	if slices.Contains(allowed, v) {
		return v, nil
	}

	return "", ValidationError{Field: field, Invariant: enumInvariant(allowed...)}
}

func requireEnum(field, raw string, allowed ...string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" || !slices.Contains(allowed, v) {
		return "", ValidationError{Field: field, Invariant: enumInvariant(allowed...)}
	}

	return v, nil
}

func rejectUnknownFormat(raw string) error {
	_, err := pickEnum("response_format", raw, formatConcise, formatConcise, formatDetailed)

	return err
}
