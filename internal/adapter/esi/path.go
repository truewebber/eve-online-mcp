package esi

import (
	"net/url"
	"strconv"
	"strings"
)

func Path(elem ...string) string {
	parts := make([]string, len(elem))
	for i, e := range elem {
		parts[i] = url.PathEscape(e)
	}

	return "/" + strings.Join(parts, "/")
}

func ID(id int) string {
	return strconv.Itoa(id)
}
