package esitest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	nhttp "net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type Fixture struct {
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	Query      map[string]string `json:"query,omitempty"`
	Source     string            `json:"source"`
	CompatDate string            `json:"compat_date"`
	Status     int               `json:"status"`
	Headers    map[string]string `json:"headers"`
	Body       json.RawMessage   `json:"body"`
}

func Key(method, path string, query url.Values) string {
	var b strings.Builder
	b.WriteString(method)
	b.WriteByte(' ')
	b.WriteString(path)
	if page := query.Get("page"); page != "" {
		b.WriteString("?page=")
		b.WriteString(page)
	}

	return b.String()
}

func (f Fixture) Key() string {
	q := url.Values{}
	for k, v := range f.Query {
		q.Set(k, v)
	}

	return Key(f.Method, f.Path, q)
}

func (f Fixture) Filename() string {
	name := f.Method + f.Path
	if page := f.Query["page"]; page != "" {
		name += "-page-" + page
	}
	var b strings.Builder
	for _, r := range name {
		switch r {
		case '/', ' ':
			b.WriteByte('-')
		default:
			b.WriteRune(r)
		}
	}

	return strings.Trim(b.String(), "-") + ".json"
}

func (f Fixture) Response() *nhttp.Response {
	header := nhttp.Header{}
	for k, v := range f.Headers {
		header.Set(k, v)
	}
	body := f.Body
	if len(body) == 0 {
		body = []byte("null")
	}

	return &nhttp.Response{
		StatusCode:    f.Status,
		Status:        fmt.Sprintf("%d %s", f.Status, nhttp.StatusText(f.Status)),
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}

func ReadDir(dir string) (map[string]Fixture, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("esitest: read %s: %w", dir, err)
	}
	out := map[string]Fixture{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		f, err := readFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out[f.Key()] = f
	}

	return out, nil
}

func Write(dir string, f Fixture) error {
	if err := os.MkdirAll(dir, testdataDirPerm); err != nil {
		return fmt.Errorf("esitest: mkdir: %w", err)
	}
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("esitest: encode: %w", err)
	}
	raw = append(raw, '\n')
	name := filepath.Join(dir, f.Filename())
	if err := os.WriteFile(name, raw, testdataFilePerm); err != nil {
		return fmt.Errorf("esitest: write %s: %w", name, err)
	}

	return nil
}

func readFile(path string) (Fixture, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Fixture{}, fmt.Errorf("esitest: read %s: %w", path, err)
	}
	var f Fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		return Fixture{}, fmt.Errorf("esitest: decode %s: %w", path, err)
	}

	return f, nil
}
