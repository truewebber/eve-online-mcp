package eve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
	"github.com/truewebber/eve-online-mcp/internal/j"
	"github.com/truewebber/eve-online-mcp/internal/usecase/session"

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

func sessionTool[In any](fn func(context.Context, *session.Session, In) (any, error)) mcp.ToolHandlerFor[In, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		return Call(ctx, func(a *session.Session) (any, error) {
			return fn(ctx, a, in)
		})
	}
}

func patchBounds(schema *jsonschema.Schema) {
	if schema == nil {
		return
	}
	for name, prop := range schema.Properties {
		if prop == nil {
			continue
		}
		switch name {
		case "limit":
			prop.Minimum, prop.Maximum = new(1.0), new(argLimitMax)
		case fItems:
			prop.Minimum, prop.Maximum = new(1.0), new(argItemsMax)
		case fDivision:
			prop.Minimum, prop.Maximum = new(1.0), new(argDivisionMax)
		case "history_days":
			prop.Minimum, prop.Maximum = new(0.0), new(argHistoryDays)
		case fApprovedCost, "min_value":
			prop.Minimum = new(0.0)
		case fStanding:
			prop.Minimum, prop.Maximum = new(argStandingMin), new(argStandingMax)
		case fFromEvent, fEventID, fMailID, fFittingID, fPage, fLastMailID:
			prop.Minimum = new(1.0)
		case fOffset:
			prop.Minimum = new(0.0)
		}
	}
}

type empty struct{}

func Result(v any) (*mcp.CallToolResult, any, error) {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, nil, wrap("Result", err)
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
		a.Logger.Error("eve: tool", "err", err, "character", a.CharacterID)

		return Handle(err)
	}

	return Result(v)
}

func Handle(err error) (*mcp.CallToolResult, any, error) {
	out := session.MapError(err)
	out[fError] = toolSentence(err)
	if names := unresolvedNames(err); len(names) > 0 {
		out["names"] = names
	}
	if v, ok := errors.AsType[ValidationError](err); ok {
		out["field"] = v.Field
	}
	if charge, ok := errors.AsType[CSPAExceedsError](err); ok {
		out["priced_cspa_cost_isk"] = charge.Cost
		out[fApprovedCost] = charge.Approved
		out["shortfall_isk"] = charge.Cost - float64(charge.Approved)
	}

	return Result(out)
}

func esiPath(elem ...any) esi.Route {
	return esi.Path(elem...)
}

func esiID(id int) esi.Arg {
	return esi.ID(id)
}

func zkillURL(killmailID int) string {
	return (&url.URL{Scheme: "https", Host: "zkillboard.com"}).JoinPath("kill", strconv.Itoa(killmailID)).String()
}

func limitOr(v, def int) int {
	if v <= 0 {
		return def
	}

	return v
}

func concise(format string) bool { return format != formatDetailed }

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
	case abs >= iskTrillion:
		return fmt.Sprintf("%.2ft", amount/iskTrillion)
	case abs >= iskBillion:
		return fmt.Sprintf("%.2fb", amount/iskBillion)
	case abs >= iskMillion:
		return fmt.Sprintf("%.2fm", amount/iskMillion)
	case abs >= iskThousand:
		return fmt.Sprintf("%.2fk", amount/iskThousand)
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

type limitedPage struct {
	Rows   []map[string]any
	fields map[string]any
}

type cursorPage struct {
	Rows       []map[string]any
	NextCursor int
	fields     map[string]any
}

type numberedPage struct {
	Rows       []map[string]any
	Page       int
	TotalPages int
	fields     map[string]any
}

type foldedPage struct {
	Rows   []map[string]any
	Total  int
	fields map[string]any
}

func applyLimit(rows []map[string]any, limit int, hint string) limitedPage {
	limit = limitOr(limit, limitDefault)
	if len(rows) <= limit {
		return limitedPage{Rows: rows, fields: map[string]any{fReturned: len(rows), fTruncated: false}}
	}
	if hint == "" {
		hint = fmt.Sprintf("Raise `limit` (currently %d).", limit)
	}

	return limitedPage{Rows: rows[:limit], fields: map[string]any{
		fReturned: limit, "total_available": len(rows), fTruncated: true, "how_to_see_more": hint,
	}}
}

type cursorPageIn struct {
	Shown     []map[string]any
	Limit     int
	Key, Hint string
	ESI       []map[string]any
}

func pageByCursor(in cursorPageIn) cursorPage {
	clipped := applyLimit(in.Shown, in.Limit, in.Hint)
	next := 0
	if len(clipped.Rows) == limitOr(in.Limit, limitDefault) && len(clipped.Rows) > 0 {
		next = j.Int(clipped.Rows[len(clipped.Rows)-1][in.Key])
	} else if len(in.ESI) >= esiCursorPage && len(in.ESI) > 0 {
		next = j.Int(in.ESI[len(in.ESI)-1][in.Key])
	}
	if next != 0 {
		clipped.fields[fNextCursor] = next
	}

	return cursorPage{Rows: clipped.Rows, NextCursor: next, fields: clipped.fields}
}

func pageByNumber(rows []map[string]any, page, totalPages, limit int) numberedPage {
	page = pageOr(page)
	if totalPages < 1 {
		totalPages = 1
	}
	clipped := applyLimit(rows, limit, "")
	clipped.fields[fPage] = page
	clipped.fields[fTotalPages] = totalPages

	return numberedPage{Rows: clipped.Rows, Page: page, TotalPages: totalPages, fields: clipped.fields}
}

func pageByOffset(rows []map[string]any, offset, limit int, hint string) foldedPage {
	if offset < 0 {
		offset = 0
	}
	limit = limitOr(limit, limitDefault)
	total := len(rows)
	if offset > total {
		offset = total
	}
	rest := rows[offset:]
	fields := map[string]any{fReturned: len(rest), fTruncated: false, fTotal: total}
	visible := rest
	if len(rest) > limit {
		visible = rest[:limit]
		if hint == "" {
			hint = fmt.Sprintf("Pass offset=%d, or raise `limit` (currently %d).", offset+limit, limit)
		}
		fields = map[string]any{
			fReturned: limit, fTruncated: true, fTotal: total, "how_to_see_more": hint,
		}
	}

	return foldedPage{Rows: visible, Total: total, fields: fields}
}

func pageOr(n int) int {
	if n < 1 {
		return 1
	}

	return n
}

func esiPageQuery(page int, extra map[string]any) map[string]any {
	out := maps.Clone(extra)
	if out == nil {
		out = map[string]any{}
	}
	out[fPage] = pageOr(page)

	return out
}

func esiCursorQuery(name string, value int) map[string]any {
	if value < 1 {
		return nil
	}

	return map[string]any{name: value}
}

func merge(dst map[string]any, extra map[string]any) map[string]any {
	maps.Copy(dst, extra)

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
			loop := slices.Contains(chain, itemID)
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
	numerals := [...]string{"0", "I", "II", "III", "IV", "V"}
	if level >= 0 && level < len(numerals) {
		return numerals[level]
	}

	return strconv.Itoa(level)
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
		return vDone
	}
	days, rem := seconds/secondsPerDay, seconds%secondsPerDay
	hours, rem := rem/secondsPerHour, rem%secondsPerHour
	minutes := rem / secondsPerMinute
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

func staleNote(ages ...float64) string {
	return esi.Result{AgeSeconds: oldestAge(ages)}.StaleNote()
}

func oldestAge(ages []float64) float64 {
	var oldest float64
	for i, age := range ages {
		if i == 0 || age > oldest {
			oldest = age
		}
	}

	return oldest
}
