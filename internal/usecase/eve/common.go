package eve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"eve-mcp/internal/adapter/esi"
	"eve-mcp/internal/adapter/sso"
	"eve-mcp/internal/domain/character"
	"eve-mcp/internal/domain/j"
	"eve-mcp/internal/domain/write"
	"eve-mcp/internal/usecase/session"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addTool[In any](s *mcp.Server, t *mcp.Tool, h mcp.ToolHandlerFor[In, any]) {
	schema, err := jsonschema.For[In](nil)
	if err == nil {
		patchBounds(schema)
		t.InputSchema = schema
	}
	mcp.AddTool(s, t, h)
}

func f64ptr(v float64) *float64 { return &v }

func patchBounds(schema *jsonschema.Schema) {
	if schema == nil {
		return
	}
	for name, prop := range schema.Properties {
		if prop == nil {
			continue
		}
		switch name {
		case "limit", "items":
			prop.Minimum, prop.Maximum = f64ptr(1), f64ptr(500)
		case "division":
			prop.Minimum, prop.Maximum = f64ptr(1), f64ptr(7)
		case "history_days":
			prop.Minimum, prop.Maximum = f64ptr(0), f64ptr(365)
		case "approved_cost":
			prop.Minimum = f64ptr(0)
		}
	}
}

const (
	characterDesc = "Character name (e.g. 'Jane Doe') or numeric character id. Leave empty to use the only authorized character; required when several are authorized — call eve_auth_status to list them."
	detailDesc    = "'concise' (default) returns only the high-signal fields and costs far fewer tokens. Use 'detailed' when you need secondary fields and raw ids."
	confirmDesc   = "Leave empty on the first call: the tool returns a preview of exactly what it would do plus a single-use token. Show that preview to the user, get an explicit yes, then call again with identical arguments and the token here."
	limitDesc     = "Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist."
	divisionDesc  = "Corporation wallet division, 1 through 7. Division 1 is the master wallet. Named divisions (if this character is a Director) come back from eve_corp_overview."
)

type empty struct{}

func Result(v any) (*mcp.CallToolResult, any, error) {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}, nil, nil
}

func Call(ctx context.Context, fn func(*session.Session) (any, error)) (*mcp.CallToolResult, any, error) {
	a, err := session.From(ctx)
	if err != nil {
		return Handle(err)
	}
	v, err := fn(a)
	if err != nil {
		return Handle(err)
	}
	return Result(v)
}

func Handle(err error) (*mcp.CallToolResult, any, error) {
	var ae sso.Error
	var nf character.NotFound
	var wb write.Blocked
	var rl esi.RateLimited
	var ee esi.Error
	switch {
	case errors.As(err, &ae):
		return Result(map[string]any{"error": ae.Error(), "kind": "AuthError"})
	case errors.As(err, &nf):
		return Result(map[string]any{"error": nf.Error(), "kind": "CharacterNotFound"})
	case errors.As(err, &wb):
		return Result(map[string]any{"error": wb.Error(), "kind": "WriteBlocked"})
	case errors.As(err, &rl):
		out := map[string]any{
			"error":               rl.Error(),
			"kind":                "EsiRateLimited",
			"status":              rl.Status,
			"retry_after_seconds": rl.RetrySec,
			"retry_at":            rl.RetryAt.UTC().Format(time.RFC3339),
			"hint":                "CCP's ESI error limit is shared for this server's public IP. Wait until retry_at, then call the same tool once. Do not retry in a loop.",
		}
		if rl.Remain != nil {
			out["error_limit_remain"] = *rl.Remain
		}
		if rl.ResetSec != nil {
			out["error_limit_reset_seconds"] = *rl.ResetSec
		}
		return Result(out)
	case errors.As(err, &ee):
		return Result(map[string]any{"error": ee.Error(), "kind": "EsiError", "status": ee.Status})
	default:
		return Result(map[string]any{"error": err.Error(), "kind": "Error"})
	}
}

func limitOr(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}

func concise(format string) bool { return format != "detailed" }

func boolDef(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

func isk(value any) string {
	if value == nil {
		return "—"
	}
	amount := j.Float(value)
	if math.IsNaN(amount) {
		return "—"
	}
	abs := math.Abs(amount)
	switch {
	case abs >= 1e12:
		return fmt.Sprintf("%.2ft", amount/1e12)
	case abs >= 1e9:
		return fmt.Sprintf("%.2fb", amount/1e9)
	case abs >= 1e6:
		return fmt.Sprintf("%.2fm", amount/1e6)
	case abs >= 1e3:
		return fmt.Sprintf("%.2fk", amount/1e3)
	default:
		return fmt.Sprintf("%.2f", amount)
	}
}

func unitPrice(prices map[int]map[string]float64, typeID int) float64 {
	entry := prices[typeID]
	if entry["average"] != 0 {
		return entry["average"]
	}
	return entry["adjusted"]
}

func lineValue(prices map[int]map[string]float64, typeID, qty int) float64 {
	return unitPrice(prices, typeID) * float64(qty)
}

func project(rows []map[string]any, keep []string, concise bool) []map[string]any {
	keepSet := map[string]struct{}{}
	for _, k := range keep {
		keepSet[k] = struct{}{}
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		picked := map[string]any{}
		for k, v := range row {
			if concise {
				if _, ok := keepSet[k]; !ok {
					continue
				}
			}
			if emptyVal(v) {
				continue
			}
			picked[k] = v
		}
		out = append(out, picked)
	}
	return out
}

func emptyVal(v any) bool {
	if v == nil {
		return true
	}
	switch t := v.(type) {
	case string:
		return t == ""
	case []any:
		return len(t) == 0
	case []string:
		return len(t) == 0
	case map[string]any:
		return len(t) == 0
	}
	return false
}

func page(rows []map[string]any, limit int, hint string) ([]map[string]any, map[string]any) {
	if limit <= 0 {
		limit = 15
	}
	if len(rows) <= limit {
		return rows, map[string]any{"returned": len(rows), "truncated": false}
	}
	if hint == "" {
		hint = fmt.Sprintf("Raise `limit` (currently %d).", limit)
	}
	return rows[:limit], map[string]any{
		"returned": limit, "total_available": len(rows), "truncated": true, "how_to_see_more": hint,
	}
}

func merge(dst map[string]any, extra map[string]any) map[string]any {
	for k, v := range extra {
		dst[k] = v
	}
	return dst
}

func compact(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		if emptyVal(v) {
			continue
		}
		out[k] = v
	}
	return out
}

func rootLocations(items []map[string]any) map[int]int {
	byID := map[int]map[string]any{}
	for _, i := range items {
		byID[j.Int(i["item_id"])] = i
	}
	roots := map[int]int{}
	for _, item := range items {
		var chain []int
		current := item
		var resolved int
		for {
			itemID := j.Int(current["item_id"])
			if r, ok := roots[itemID]; ok {
				resolved = r
				break
			}
			parentID := j.Int(current["location_id"])
			parent, ok := byID[parentID]
			if !ok {
				resolved = parentID
				break
			}
			loop := false
			for _, n := range chain {
				if n == itemID {
					loop = true
					break
				}
			}
			if loop {
				resolved = parentID
				break
			}
			chain = append(chain, itemID)
			current = parent
		}
		for _, n := range chain {
			roots[n] = resolved
		}
		roots[j.Int(item["item_id"])] = resolved
	}
	return roots
}

func roman(level int) string {
	switch level {
	case 1:
		return "I"
	case 2:
		return "II"
	case 3:
		return "III"
	case 4:
		return "IV"
	case 5:
		return "V"
	case 0:
		return "0"
	default:
		return fmt.Sprint(level)
	}
}

func parseTime(value string) *time.Time {
	if value == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, strings.ReplaceAll(value, "Z", "+00:00"))
	if err != nil {
		t, err = time.Parse("2006-01-02T15:04:05", value)
		if err != nil {
			return nil
		}
	}
	return &t
}

func humanDelta(d time.Duration) string {
	seconds := int(d.Seconds())
	if seconds < 0 {
		return "done"
	}
	days, rem := seconds/86400, seconds%86400
	hours, rem := rem/3600, rem%3600
	minutes := rem / 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh", days, hours)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

func idsFrom(values ...any) []int {
	var out []int
	for _, v := range values {
		id := j.Int(v)
		if id != 0 {
			out = append(out, id)
		}
	}
	return out
}

func keys(m map[int]int) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func setToList(m map[int]struct{}) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func intersect(have []string, want map[string]struct{}) []string {
	var out []string
	for _, s := range have {
		if _, ok := want[s]; ok {
			out = append(out, s)
		}
	}
	return out
}

var activities = map[int]string{
	1: "Manufacturing", 3: "Researching Time Efficiency",
	4: "Researching Material Efficiency", 5: "Copying",
	7: "Reverse Engineering", 8: "Invention", 9: "Reactions", 11: "Reactions",
}
