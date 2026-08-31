package tests

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/truewebber/eve-online-mcp/internal/domain/j"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestLintNamespace(t *testing.T) {
	t.Parallel()
	bad := lintTool(map[string]any{fieldName: "wallet_list", fieldDescription: strings.Repeat("x", minDescriptionChars)})
	if !hasFailure(bad, "not namespaced") {
		t.Fatalf("want namespace failure, got %v", bad)
	}
	for _, tool := range liveTools(t) {
		if fails := lintTool(tool); hasFailure(fails, "not namespaced") {
			t.Fatalf("%s: %v", j.Str(tool[fieldName]), fails)
		}
	}
}

func TestLintDescriptionLength(t *testing.T) {
	t.Parallel()
	bad := lintTool(map[string]any{fieldName: "eve_short", fieldDescription: "too short"})
	if !hasFailure(bad, "description is only") {
		t.Fatalf("want length failure, got %v", bad)
	}
	for _, tool := range liveTools(t) {
		if fails := lintTool(tool); hasFailure(fails, "description is only") {
			t.Fatalf("%s: %v", j.Str(tool[fieldName]), fails)
		}
	}
}

func TestLintDescriptionIndent(t *testing.T) {
	t.Parallel()
	bad := lintTool(map[string]any{
		fieldName:        "eve_indented",
		fieldDescription: strings.Repeat("x", minDescriptionChars) + "\n    indented",
	})
	if !hasFailure(bad, "indentation") {
		t.Fatalf("want indent failure, got %v", bad)
	}
	for _, tool := range liveTools(t) {
		if fails := lintTool(tool); hasFailure(fails, "indentation") {
			t.Fatalf("%s: %v", j.Str(tool[fieldName]), fails)
		}
	}
}

func TestLintParamDescriptions(t *testing.T) {
	t.Parallel()
	bad := lintTool(map[string]any{
		fieldName:        "eve_params",
		fieldDescription: strings.Repeat("x", minDescriptionChars),
		fieldInputSchema: map[string]any{
			fieldProperties: map[string]any{fieldLimit: map[string]any{fieldType: typeInteger}},
		},
	})
	if !hasFailure(bad, "no description") {
		t.Fatalf("want param description failure, got %v", bad)
	}
	for _, tool := range liveTools(t) {
		if fails := lintTool(tool); hasFailure(fails, "no description") {
			t.Fatalf("%s: %v", j.Str(tool[fieldName]), fails)
		}
	}
}

func TestLintDescriptionTooLong(t *testing.T) {
	t.Parallel()
	got := inspectTool(map[string]any{
		fieldName:        "eve_long",
		fieldDescription: strings.Repeat("x", maxDescriptionChars+1),
	})
	if !hasFailure(got.warnings, "consider trimming") {
		t.Fatalf("want max-length warning, got %v", got.warnings)
	}
}

func TestLintUnboundedInteger(t *testing.T) {
	t.Parallel()
	got := inspectTool(map[string]any{
		fieldName:        "eve_bounds",
		fieldDescription: strings.Repeat("x", minDescriptionChars),
		fieldInputSchema: map[string]any{
			fieldProperties: map[string]any{fieldLimit: map[string]any{
				fieldDescription: "page size",
				fieldType:        typeInteger,
			}},
		},
	})
	if !hasFailure(got.warnings, "unbounded integer") {
		t.Fatalf("want unbounded warning, got %v", got.warnings)
	}
}

func TestLintResponseFormatMissing(t *testing.T) {
	t.Parallel()
	got := inspectTool(map[string]any{
		fieldName:        "eve_assets_list",
		fieldDescription: strings.Repeat("x", minDescriptionChars),
		fieldInputSchema: map[string]any{
			fieldProperties: map[string]any{fieldLimit: map[string]any{
				fieldDescription: "page size",
				fieldType:        typeInteger,
				"maximum":        500,
			}},
		},
	})
	if !hasFailure(got.warnings, "no `response_format`") {
		t.Fatalf("want response_format warning, got %v", got.warnings)
	}
}

func TestNeedsResponseFormatExceptions(t *testing.T) {
	t.Parallel()
	if needsResponseFormat("eve_universe_search") || needsResponseFormat("eve_universe_hotspots") {
		t.Fatal("listed exceptions must not require response_format")
	}
	if !needsResponseFormat("eve_assets_list") {
		t.Fatal("list tools require response_format")
	}
}

func liveTools(t *testing.T) []map[string]any {
	t.Helper()
	e := openEnv(t)
	got, err := e.client.ListTools(t.Context(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	out := make([]map[string]any, 0, len(got.Tools))
	for _, tool := range got.Tools {
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

func hasFailure(fails []string, needle string) bool {
	for _, f := range fails {
		if strings.Contains(f, needle) {
			return true
		}
	}

	return false
}
