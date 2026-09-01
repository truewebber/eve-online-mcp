package tests

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCatalogParserOk(t *testing.T) {
	t.Parallel()
	cat, err := parseTOOLS(readTestdata(t, "catalog/ok.md"))
	if err != nil {
		t.Fatal(err)
	}
	checkCatalogOkMeta(t, cat)
	checkCatalogOkTools(t, cat)
	checkCatalogOkPaging(t, cat)
}

func checkCatalogOkMeta(t *testing.T, cat catalog) {
	t.Helper()
	if cat.Instructions != "Hello from the contract." {
		t.Fatalf("instructions %q", cat.Instructions)
	}
	if cat.Shared["limit"] == "" || cat.Shared["offset"] == "" {
		t.Fatalf("shared %+v", cat.Shared)
	}
}

func checkCatalogOkTools(t *testing.T, cat catalog) {
	t.Helper()
	status, ok := cat.Tools["eve_server_status"]
	if !ok || status.Params == nil || len(status.Params) != 0 {
		t.Fatalf("status %+v", status)
	}
	if !strings.Contains(status.Description, "Tranquility server status") || strings.Contains(status.Description, "Source") {
		t.Fatalf("description %q", status.Description)
	}
	assets := cat.Tools["eve_assets_list"]
	if assets.Params["limit"].Description != cat.Shared["limit"] {
		t.Fatalf("shared limit %q", assets.Params["limit"].Description)
	}
	if assets.Params["min_value"].SchemaType != typeNumber || assets.Params["min_value"].Bounds.Min == nil || *assets.Params["min_value"].Bounds.Min != 0 {
		t.Fatalf("min_value %+v", assets.Params["min_value"])
	}
	if assets.Params["location"].Required {
		t.Fatal("location required")
	}
}

func checkCatalogOkPaging(t *testing.T, cat catalog) {
	t.Helper()
	if cat.Paging["eve_mail_list"].Param != paramLastMailID {
		t.Fatalf("cursor %+v", cat.Paging["eve_mail_list"])
	}
	if cat.Paging["eve_assets_blueprints"].Kind != pageNumbered || cat.Paging["eve_assets_list"].Kind != pageFolded {
		t.Fatalf("paging %+v", cat.Paging)
	}
	if cat.Paging["eve_server_status"].Kind != pageNone {
		t.Fatalf("none %+v", cat.Paging["eve_server_status"])
	}
}

func TestCatalogParserModules(t *testing.T) {
	t.Parallel()
	cat, err := parseTOOLS(readTestdata(t, "catalog/modules.md"))
	if err != nil {
		t.Fatal(err)
	}
	mod := cat.Tools["eve_fitting_save"].Params["modules"]
	if !mod.Required || mod.SchemaType != typeArray || mod.ItemType != typeObject {
		t.Fatalf("modules %+v", mod)
	}
	if !mod.Fields["name"].Required || mod.Fields["flag"].Required {
		t.Fatalf("fields %+v", mod.Fields)
	}
	if mod.Fields["name"].Description != "Exact module name." {
		t.Fatalf("field desc %q", mod.Fields["name"].Description)
	}
}

func TestCatalogParserBrokenShared(t *testing.T) {
	t.Parallel()
	_, err := parseTOOLS(readTestdata(t, "catalog/broken_shared.md"))
	if !errors.Is(err, errUnknownShared) {
		t.Fatalf("%v", err)
	}
}

func TestCatalogParserBrokenHeading(t *testing.T) {
	t.Parallel()
	_, err := parseTOOLS(readTestdata(t, "catalog/broken_heading.md"))
	if !errors.Is(err, errBadHeading) {
		t.Fatalf("%v", err)
	}
}

func TestCatalogParserBrokenTable(t *testing.T) {
	t.Parallel()
	_, err := parseTOOLS(readTestdata(t, "catalog/broken_table.md"))
	if !errors.Is(err, errBadTable) {
		t.Fatalf("%v", err)
	}
}

func TestCatalogParserBrokenRequired(t *testing.T) {
	t.Parallel()
	_, err := parseTOOLS(readTestdata(t, "catalog/broken_required.md"))
	if !errors.Is(err, errBadRequired) {
		t.Fatalf("%v", err)
	}
}

func TestCatalogParserBrokenBounds(t *testing.T) {
	t.Parallel()
	_, err := parseTOOLS(readTestdata(t, "catalog/broken_bounds.md"))
	if !errors.Is(err, errBadBounds) {
		t.Fatalf("%v", err)
	}
}

func TestCatalogParserESIOk(t *testing.T) {
	t.Parallel()
	doc, err := parseESI(readTestdata(t, "esi/ok.md"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := doc.Endpoints[esiKey("GET", "/status")]; !ok {
		t.Fatalf("%+v", doc.Endpoints)
	}
	if _, ok := doc.Endpoints[esiKey("GET", "/characters/{}/wallet")]; !ok {
		t.Fatal("wallet not normalised")
	}
	if _, ok := doc.Endpoints[esiKey("GET", "https://login.eveonline.com/v2/oauth/authorize")]; ok {
		t.Fatal("SSO row leaked")
	}
	if len(doc.Endpoints) != 3 {
		t.Fatalf("len %d %+v", len(doc.Endpoints), doc.Endpoints)
	}
}

func TestCatalogParserESIBrokenRow(t *testing.T) {
	t.Parallel()
	_, err := parseESI(readTestdata(t, "esi/broken_row.md"))
	if !errors.Is(err, errBadESIMethod) {
		t.Fatalf("%v", err)
	}
}

func TestCatalogParserNormalizePath(t *testing.T) {
	t.Parallel()
	if got := normalizePath("/characters/%d/mail/%s"); got != "/characters/{}/mail/{}" {
		t.Fatal(got)
	}
	if got := normalizePath("/characters/{character_id}/calendar/{event_id}"); got != "/characters/{}/calendar/{}" {
		t.Fatal(got)
	}
}

func TestCatalogParserRealTOOLS(t *testing.T) {
	t.Parallel()
	cat := mustParseDocs(t)
	if len(cat.Tools) != 52 {
		t.Fatalf("tools %d", len(cat.Tools))
	}
	if cat.Tools["eve_calendar_list"].Params[paramFromEvent].Name == "" {
		t.Fatal("from_event")
	}
	if cat.Tools["eve_mail_compose"].Params["to"].SchemaType != typeArray {
		t.Fatal("to list")
	}
	if cat.Shared["confirm_token"] == "" || cat.Instructions == "" {
		t.Fatal("shared or instructions")
	}
	if cat.Paging["eve_mail_list"].Param != paramLastMailID || cat.Paging["eve_corp_members"].Kind != pageNone {
		t.Fatalf("paging %+v", cat.Paging)
	}
}

func TestCatalogParserExtractLiteralAndCtor(t *testing.T) {
	t.Parallel()
	calls, err := extractSource("sample.go", readTestdata(t, "catalog/src_literal.go"))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, c := range calls {
		got[esiKey(c.Method, c.Path)] = true
	}
	for _, want := range []string{
		esiKey(methodGET, "/status"),
		esiKey(methodPOST, "/characters/{}/cspa"),
		esiKey(methodPUT, "/characters/{}/mail/{}"),
	} {
		if !got[want] {
			t.Fatalf("missing %s in %+v", want, got)
		}
	}
}

func TestCatalogParserExtractParamFromCaller(t *testing.T) {
	t.Parallel()
	calls, err := extractSource("sample.go", readTestdata(t, "catalog/src_caller.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !hasCall(calls, methodGET, "/characters/{}/killmails/recent") {
		t.Fatalf("%+v", calls)
	}
}

func TestCatalogParserExtractAssignedMethods(t *testing.T) {
	t.Parallel()
	calls, err := extractSource("sample.go", readTestdata(t, "catalog/src_assign.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !hasCall(calls, methodPOST, "/characters/{}/contacts") || !hasCall(calls, methodPUT, "/characters/{}/contacts") {
		t.Fatalf("%+v", calls)
	}
}

func TestCatalogParserDiffReportsBothSides(t *testing.T) {
	t.Parallel()
	const (
		demo = "eve_demo"
		item = "item"
	)
	doc := catalog{
		Tools: map[string]toolSpec{
			demo: {
				Name: demo, Description: "Contract text.",
				Params: map[string]paramSpec{
					item: {Name: item, SchemaType: typeString, Required: true, Description: "Exact name."},
				},
			},
		},
	}
	live := []map[string]any{{
		fieldName:        demo,
		fieldDescription: "Different text.",
		fieldInputSchema: map[string]any{
			fieldProperties: map[string]any{
				item: map[string]any{fieldType: typeInteger, fieldDescription: "Exact name."},
			},
		},
	}}
	got := diffTools(doc, live)
	if !hasFinding(got, demo, fieldDescription) || !hasFinding(got, demo, item+"."+fieldType) {
		t.Fatalf("%v", got)
	}
	for _, f := range got {
		if f.Field == item+"."+fieldType && (f.Doc != typeString || f.Got != typeInteger) {
			t.Fatalf("imprecise type finding %+v", f)
		}
	}
}

func TestCatalogParserDiffESIBothDirections(t *testing.T) {
	t.Parallel()
	doc := esiDoc{Endpoints: map[string]esiRow{
		esiKey(methodGET, "/status"):                 {Method: methodGET, Path: "/status"},
		esiKey(methodPOST, "/ui/openwindow/newmail"): {Method: methodPOST, Path: "/ui/openwindow/newmail"},
	}}
	calls := []esiCall{
		{Method: methodGET, Path: "/status", File: "internal/usecase/eve/account.go"},
		{Method: methodGET, Path: "/invented", File: "internal/usecase/eve/x.go"},
	}
	got := diffESI(doc, calls)
	if !hasFinding(got, "esi", esiKey(methodPOST, "/ui/openwindow/newmail")) {
		t.Fatalf("missing row: %v", got)
	}
	if !hasFinding(got, "esi", esiKey(methodGET, "/invented")) {
		t.Fatalf("missing call: %v", got)
	}
}

func TestCatalogParserNullableSchemaType(t *testing.T) {
	t.Parallel()
	if schemaTypeOf(map[string]any{fieldType: []any{"null", typeBoolean}}) != typeBoolean {
		t.Fatal("nullable bool")
	}
	if schemaTypeOf(map[string]any{fieldType: []any{"null", typeArray}, fieldItems: map[string]any{fieldType: typeString}}) != typeArray {
		t.Fatal("nullable array")
	}
	if schemaTypeOf(map[string]any{fieldItems: map[string]any{fieldType: typeObject}}) != typeArray {
		t.Fatal("items implies array")
	}
}

func TestCatalogParserDiffInstructions(t *testing.T) {
	t.Parallel()
	got := diffInstructions("alpha", "beta")
	if len(got) != 1 || got[0].Doc != "alpha" || got[0].Got != "beta" {
		t.Fatalf("%v", got)
	}
	if diffInstructions(" same \n", "same") != nil {
		t.Fatal("trim")
	}
}

func readTestdata(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}

	return string(raw)
}

func mustParseDocs(t *testing.T) catalog {
	t.Helper()
	root, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	text, err := readDoc(root, docsTOOLS)
	if err != nil {
		t.Fatal(err)
	}
	cat, err := parseTOOLS(text)
	if err != nil {
		t.Fatal(err)
	}

	return cat
}

func hasCall(calls []esiCall, method, path string) bool {
	for _, c := range calls {
		if c.Method == method && c.Path == path {
			return true
		}
	}

	return false
}

func hasFinding(fs []finding, tool, field string) bool {
	for _, f := range fs {
		if f.Tool == tool && f.Field == field {
			return true
		}
	}

	return false
}
