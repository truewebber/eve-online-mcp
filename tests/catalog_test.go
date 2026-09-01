package tests

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCatalog(t *testing.T) {
	t.Parallel()
	root, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	toolsText, err := readDoc(root, docsTOOLS)
	if err != nil {
		t.Fatal(err)
	}
	esiText, err := readDoc(root, docsESI)
	if err != nil {
		t.Fatal(err)
	}
	cat, err := parseTOOLS(toolsText)
	if err != nil {
		t.Fatal(err)
	}
	esi, err := parseESI(esiText)
	if err != nil {
		t.Fatal(err)
	}
	calls, err := extractCalls(root)
	if err != nil {
		t.Fatal(err)
	}
	e := openEnv(t)
	got, err := e.client.ListTools(t.Context(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	live := marshalTools(t, got.Tools)
	init := e.client.InitializeResult()
	served := ""
	if init != nil {
		served = init.Instructions
	}
	findings := diffTools(cat, live)
	findings = append(findings, diffInstructions(cat.Instructions, served)...)
	findings = append(findings, diffESI(esi, calls)...)
	if len(findings) == 0 {
		return
	}
	t.Errorf("%d catalogue findings, grouped by tool then field:", len(findings))
	current := ""
	for _, f := range findings {
		if f.Tool != current {
			current = f.Tool
			t.Errorf("--- %s", f.Tool)
		}
		t.Errorf("%s %s", findingOwner(f), formatFinding(f))
	}
}

func marshalTools(t *testing.T, tools []*mcp.Tool) []map[string]any {
	t.Helper()
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		raw, err := json.Marshal(tool)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}

	return out
}

func findingOwner(f finding) string {
	if isPagingParam(f.Field) {
		return "T27"
	}

	return "T26"
}

func formatFinding(f finding) string {
	switch f.Tool {
	case "instructions":
		doc, got := quotePair(f.Doc, f.Got)

		return "instructions text: " + docsTOOLS + " " + doc + "; " + sideInit + " " + got
	case "esi":
		return "esi " + f.Field + ": " + docsESI + " " + quoteSide(f.Doc) + "; call site " + quoteSide(f.Got)
	default:
		return f.String()
	}
}
