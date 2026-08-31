package tests

import (
	"encoding/json"
	"fmt"
	"sort"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestReadToolsSmoke(t *testing.T) { //nolint:tparallel // one session is shared across tool calls
	t.Parallel()
	e := openEnv(t)
	got, err := e.client.ListTools(t.Context(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range got.Tools {
		if skipInSmoke(tool.Name) {
			continue
		}
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Fatal("no read tools")
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			res, err := e.client.CallTool(t.Context(), &mcp.CallToolParams{
				Name:      name,
				Arguments: smokeArgs(name),
			})
			if err != nil {
				t.Fatal(err)
			}
			text := toolText(res)
			if len(text) > maxDefaultResponseChars {
				t.Fatalf("default response is %d chars, cap %d", len(text), maxDefaultResponseChars)
			}
			var parsed any
			if err := json.Unmarshal([]byte(text), &parsed); err != nil {
				t.Fatalf("not JSON: %v\n%s", err, text)
			}
			if m, ok := parsed.(map[string]any); ok {
				if errVal, has := m["error"]; has && errVal != nil && errVal != "" {
					s := fmt.Sprint(errVal)
					if len(s) > errorPreview {
						s = s[:errorPreview]
					}
					t.Fatalf("error %s kind %v", s, m["kind"])
				}
			}
		})
	}
}
