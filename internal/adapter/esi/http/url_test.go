package http

import (
	"encoding/json"
	"io"
	nhttp "net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
)

func TestRequestAssemblesURL(t *testing.T) {
	t.Parallel()
	var got *url.URL
	srv := httptest.NewServer(nhttp.HandlerFunc(func(w nhttp.ResponseWriter, r *nhttp.Request) {
		got = r.URL
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{}`))
		if err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(srv.Close)
	c := mustClient(t, testOptions(srv.URL), srv.Client())
	_, err := c.Get(t.Context(), esi.Path("characters", esi.ID(1), "skills"), nil, map[string]any{"page": 2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("no request")
	}
	if got.Path != "/characters/1/skills" {
		t.Fatalf("path %q", got.Path)
	}
	if got.Query().Get("page") != "2" {
		t.Fatalf("query %q", got.RawQuery)
	}
}

func TestRequestKeepsHostWhenPathHasDotDot(t *testing.T) {
	t.Parallel()
	var gotHost, gotPath string
	srv := httptest.NewServer(nhttp.HandlerFunc(func(w nhttp.ResponseWriter, r *nhttp.Request) {
		gotHost = r.Host
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, err := io.WriteString(w, `{}`)
		if err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(srv.Close)
	c := mustClient(t, testOptions(srv.URL), srv.Client())
	_, err := c.Get(t.Context(), "/characters/1/../../../evil", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	base, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if gotHost != base.Host {
		t.Fatalf("host %q want %q", gotHost, base.Host)
	}
	if gotPath != "/evil" {
		t.Fatalf("path %q", gotPath)
	}
}

func TestRequestKeepsPathWhenIDHasDotDot(t *testing.T) {
	t.Parallel()
	var gotPath string
	srv := httptest.NewServer(nhttp.HandlerFunc(func(w nhttp.ResponseWriter, r *nhttp.Request) {
		gotPath = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		_, err := io.WriteString(w, `{}`)
		if err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(srv.Close)
	c := mustClient(t, testOptions(srv.URL), srv.Client())
	_, err := c.Get(t.Context(), esi.Path("characters", "../evil", "skills"), nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/characters/..%2Fevil/skills" {
		t.Fatalf("path %q", gotPath)
	}
}

func TestRequestKeepsPathWhenIDLooksLikeHost(t *testing.T) {
	t.Parallel()
	var gotHost, gotPath string
	srv := httptest.NewServer(nhttp.HandlerFunc(func(w nhttp.ResponseWriter, r *nhttp.Request) {
		gotHost = r.Host
		gotPath = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		_, err := io.WriteString(w, `{}`)
		if err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(srv.Close)
	c := mustClient(t, testOptions(srv.URL), srv.Client())
	_, err := c.Get(t.Context(), esi.Path("characters", "//evil.example", "skills"), nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	base, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if gotHost != base.Host {
		t.Fatalf("host %q want %q", gotHost, base.Host)
	}
	if gotPath != "/characters/%2F%2Fevil.example/skills" {
		t.Fatalf("path %q", gotPath)
	}
}

func TestRequestEncodesQuery(t *testing.T) {
	t.Parallel()
	for _, search := range []string{"a&b=c", "a b"} {
		t.Run(search, func(t *testing.T) {
			t.Parallel()
			var rawQuery string
			srv := httptest.NewServer(nhttp.HandlerFunc(func(w nhttp.ResponseWriter, r *nhttp.Request) {
				rawQuery = r.URL.RawQuery
				w.Header().Set("Content-Type", "application/json")
				err := json.NewEncoder(w).Encode(map[string]any{})
				if err != nil {
					t.Error(err)
				}
			}))
			t.Cleanup(srv.Close)
			c := mustClient(t, testOptions(srv.URL), srv.Client())
			_, err := c.Get(t.Context(), "/search", nil, map[string]any{"search": search}, nil)
			if err != nil {
				t.Fatal(err)
			}
			q, err := url.ParseQuery(rawQuery)
			if err != nil {
				t.Fatal(err)
			}
			if q.Get("search") != search {
				t.Fatalf("query %q", rawQuery)
			}
		})
	}
}
