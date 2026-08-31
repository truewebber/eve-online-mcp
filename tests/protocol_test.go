package tests

import (
	"io"
	nhttp "net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestInitializeAndToolsList(t *testing.T) {
	t.Parallel()
	e := openEnv(t)
	if e.client == nil {
		t.Fatal("initialize did not produce a session")
	}
	got, err := e.client.ListTools(t.Context(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tools) == 0 {
		t.Fatal("tools/list returned no tools")
	}
}

func TestMCPUnauthorized(t *testing.T) {
	t.Parallel()
	e := openEnv(t)
	req, err := nhttp.NewRequestWithContext(t.Context(), nhttp.MethodPost, e.server.URL+"/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := nhttp.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != nhttp.StatusUnauthorized {
		t.Fatalf("status %d", resp.StatusCode)
	}
	www := resp.Header.Get("WWW-Authenticate")
	if !strings.Contains(www, "resource_metadata=") || !strings.Contains(www, `scope="eve"`) {
		t.Fatalf("WWW-Authenticate %q", www)
	}
}
