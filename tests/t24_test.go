package tests

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/truewebber/eve-online-mcp/internal/j"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCorpOverviewNPCListsNoTools(t *testing.T) {
	t.Parallel()
	e := openEnv(t)
	res, err := e.client.CallTool(t.Context(), &mcp.CallToolParams{Name: "eve_corp_overview"})
	if err != nil {
		t.Fatal(err)
	}
	out := parseTool(t, res)
	if j.Str(out["corporation_kind"]) != "npc" {
		t.Fatalf("kind %v", out["corporation_kind"])
	}
	if len(j.Slice(out["available_tools"])) != 0 {
		t.Fatalf("available_tools %v", out["available_tools"])
	}
}

func TestCalendarRespondReachableFromList(t *testing.T) {
	t.Parallel()
	e := openEnv(t)
	listed, err := e.client.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "eve_calendar_list",
		Arguments: map[string]any{"limit": 50},
	})
	if err != nil {
		t.Fatal(err)
	}
	page := parseTool(t, listed)
	events := j.Maps(page["events"])
	if len(events) == 0 {
		t.Fatal("calendar_list returned no events")
	}
	eventID := j.Int(events[0]["event_id"])
	res, err := e.client.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "eve_calendar_respond",
		Arguments: map[string]any{
			"event_id": eventID,
			"response": "accepted",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := parseTool(t, res)
	if j.Str(out["status"]) != "confirmation_required" {
		t.Fatalf("%+v", out)
	}
	if j.Str(out["confirm_token"]) == "" {
		t.Fatal("no confirm_token")
	}
}

func TestMailSendPreviewStatesPricedCSPA(t *testing.T) {
	t.Parallel()
	e := openEnv(t)
	res, err := e.client.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "eve_mail_send",
		Arguments: map[string]any{
			"to":            []string{"Tritanium"},
			"subject":       "hello",
			"body":          "world",
			"approved_cost": 3000,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := parseTool(t, res)
	will := j.Map(out["will_do"])
	if j.Float(will["priced_cspa_cost_isk"]) != 2950 {
		t.Fatalf("priced charge %+v", will)
	}
	if j.Str(out["confirm_token"]) == "" {
		t.Fatal("no token after a priced preview")
	}
}

func TestOpenWindowNewmailNamesAcceptedValues(t *testing.T) {
	t.Parallel()
	e := openEnv(t)
	res, err := e.client.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "eve_ui_open_window",
		Arguments: map[string]any{"window": "newmail", "target": "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := toolText(res)
	if !strings.Contains(text, "market") || !strings.Contains(text, "info") || !strings.Contains(text, "contract") {
		t.Fatalf("%s", text)
	}
	if strings.Contains(text, "Show Info") && strings.Contains(text, "opened") {
		t.Fatalf("fell through: %s", text)
	}
}

func parseTool(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	raw := toolText(res)
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}

	return out
}
