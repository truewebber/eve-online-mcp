package esi

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const idPattern = "{id}"

type Route struct {
	raw     string
	pattern string
}

func (r Route) String() string {
	return r.raw
}

func (r Route) Pattern() string {
	if r.pattern == "" {
		return r.raw
	}

	return r.pattern
}

type Arg struct {
	value string
}

func ID(id int) Arg {
	return Arg{value: strconv.Itoa(id)}
}

func Param(value string) Arg {
	return Arg{value: value}
}

func Path(elem ...any) Route {
	if r, ok := staticRoute(elem); ok {
		return r
	}
	raw := make([]string, len(elem))
	pat := make([]string, len(elem))
	for i, e := range elem {
		raw[i], pat[i] = segment(e)
	}

	return Route{raw: "/" + strings.Join(raw, "/"), pattern: "/" + strings.Join(pat, "/")}
}

func staticRoute(elem []any) (Route, bool) {
	if len(elem) != 1 {
		return Route{}, false
	}
	s, ok := elem[0].(string)
	if !ok || !strings.HasPrefix(s, "/") {
		return Route{}, false
	}

	return Route{raw: s, pattern: s}, true
}

func segment(e any) (string, string) {
	switch x := e.(type) {
	case Arg:
		return url.PathEscape(x.value), idPattern
	case string:
		esc := url.PathEscape(x)

		return esc, esc
	default:
		return url.PathEscape(fmt.Sprint(e)), idPattern
	}
}
