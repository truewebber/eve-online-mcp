package http

import (
	"strings"
	"time"
)

func (c *Client) observeRequest(method string, status int, path string, d time.Duration) {
	c.observe.Request(method, status, templatePath(path), d)
}

func templatePath(path string) string {
	if path == "" {
		return path
	}
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if isIDSegment(p) {
			parts[i] = "{id}"
		}
	}

	return strings.Join(parts, "/")
}

func isIDSegment(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}

	return true
}
