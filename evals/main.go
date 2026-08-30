// Eval harness for the EVE MCP server.
//
// Two deterministic gates that run without a model:
//
//	go run ./evals lint    // tool definitions meet the quality bar
//	go run ./evals smoke   // every read tool answers, and what it costs
//
// The agentic tasks in tasks.yaml need a model in the loop; see evals/README.md.
// Exit code is non-zero when a gate fails, so this is CI-usable.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultURL = "http://127.0.0.1:8765/mcp"

	// A tool description shorter than this is almost certainly not explaining
	// when to use it, only what it is.
	minDescriptionChars = 120
	// Above this a single tool is crowding out the rest of the tool list.
	maxDescriptionChars = 2000
	// A read call costing more than this by default is not respecting context.
	maxDefaultResponseChars = 6000
)

// Tools whose rows are already minimal — every field is high-signal, so a
// concise/detailed split would return the same thing twice.
var noResponseFormatNeeded = map[string]struct{}{
	"eve_character_standings": {},
	"eve_industry_mining":     {},
	"eve_universe_search":     {},
	"eve_universe_hotspots":   {},
}

// Tools that mutate game state, or need arguments only a human can supply.
var skipInSmoke = map[string]struct{}{
	"eve_auth_logout":      {},
	"eve_auth_login_url":   {},
	"eve_mail_read":        {},
	"eve_ui_set_waypoint":  {},
	"eve_ui_open_window":   {},
	"eve_fitting_save":     {},
	"eve_fitting_delete":   {},
	"eve_mail_mark":        {},
	"eve_mail_delete":      {},
	"eve_mail_send":        {},
	"eve_contacts_set":     {},
	"eve_contacts_delete":  {},
	"eve_calendar_respond": {},
	// Corporation reads need in-game roles the default smoke character may lack.
	// eve_corp_overview stays in smoke: it is public-info plus roles and must
	// answer even for an NPC corp.
	"eve_corp_assets_list":   {},
	"eve_corp_assets_find":   {},
	"eve_corp_blueprints":    {},
	"eve_corp_wallet":        {},
	"eve_corp_industry_jobs": {},
	"eve_corp_mining":        {},
	"eve_corp_orders":        {},
	"eve_corp_contracts":     {},
	"eve_corp_killmails":     {},
	"eve_corp_structures":    {},
	"eve_corp_members":       {},
}

// Minimal arguments for tools that require some.
var smokeArgs = map[string]map[string]any{
	"eve_market_price":    {"item": "Tritanium"},
	"eve_universe_item":   {"item": "Rifter"},
	"eve_universe_system": {"system": "Jita"},
	"eve_universe_route":  {"origin": "Jita", "destination": "Amarr"},
	"eve_universe_search": {"query": "Rifter"},
	"eve_assets_find":     {"name": "Drake"},
}

const usage = `Eval harness for the EVE MCP server.

Usage:
  evals <lint|smoke|all> [--url URL] [--token TOKEN]

Flags:
  --url    MCP endpoint (default http://127.0.0.1:8765/mcp)
  --token  Bearer token; falls back to EVE_MCP_TOKEN

lint checks tool definitions. smoke calls every read tool. all runs both.
`

type rpc struct {
	url    string
	token  string
	client *http.Client
	id     int
}

func newRPC(url, token string) *rpc {
	return &rpc{
		url:    url,
		token:  token,
		client: &http.Client{Timeout: 120 * time.Second},
	}
}

func (r *rpc) call(method string, params map[string]any) (map[string]any, error) {
	r.id++
	if params == nil {
		params = map[string]any{}
	}
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      r.id,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, r.url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach the MCP server at %s: %w", r.url, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("cannot reach the MCP server at %s: %w", r.url, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("cannot reach the MCP server at %s: HTTP Error %d: %s", r.url, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	body := string(raw)
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "data: ") {
			var out map[string]any
			if err := json.Unmarshal([]byte(line[6:]), &out); err != nil {
				return nil, err
			}
			return out, nil
		}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *rpc) tools() ([]map[string]any, error) {
	msg, err := r.call("tools/list", nil)
	if err != nil {
		return nil, err
	}
	result, _ := msg["result"].(map[string]any)
	raw, _ := result["tools"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, t := range raw {
		if m, ok := t.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out, nil
}

func (r *rpc) toolCall(name string, args map[string]any) (string, error) {
	if args == nil {
		args = map[string]any{}
	}
	msg, err := r.call("tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return "", err
	}
	if errObj, ok := msg["error"]; ok {
		raw, _ := json.Marshal(errObj)
		return string(raw), nil
	}
	result, _ := msg["result"].(map[string]any)
	content, _ := result["content"].([]any)
	var b strings.Builder
	for _, c := range content {
		item, _ := c.(map[string]any)
		text, _ := item["text"].(string)
		b.WriteString(text)
	}
	return b.String(), nil
}

func lint(r *rpc) int {
	tools, err := r.tools()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var failures, warnings []string
	params := 0
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		description, _ := tool["description"].(string)
		schema, _ := tool["inputSchema"].(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		if props == nil {
			props = map[string]any{}
		}
		params += len(props)

		if !strings.HasPrefix(name, "eve_") {
			failures = append(failures, name+": not namespaced under 'eve_'")
		}
		if len(description) < minDescriptionChars {
			failures = append(failures, fmt.Sprintf("%s: description is only %d chars", name, len(description)))
		}
		if len(description) > maxDescriptionChars {
			warnings = append(warnings, fmt.Sprintf("%s: description is %d chars, consider trimming", name, len(description)))
		}
		if strings.Contains(description, "\n    ") || description != strings.TrimSpace(description) {
			failures = append(failures, name+": description carries raw docstring indentation")
		}

		for param, specAny := range props {
			spec, _ := specAny.(map[string]any)
			if spec == nil {
				spec = map[string]any{}
			}
			if desc, _ := spec["description"].(string); desc == "" {
				failures = append(failures, name+"."+param+": no description in the schema")
			}
			// Game ids are opaque 64-bit values with no meaningful upper bound;
			// only tunables like `limit` benefit from a declared range.
			if typ, _ := spec["type"].(string); typ == "integer" {
				if _, ok := spec["maximum"]; !ok && !strings.HasSuffix(param, "_id") {
					warnings = append(warnings, name+"."+param+": unbounded integer, no maximum in schema")
				}
			}
			switch param {
			case "user", "id", "target_id", "data", "input":
				warnings = append(warnings, name+"."+param+": ambiguous parameter name")
			}
		}

		if _, hasLimit := props["limit"]; hasLimit {
			_, hasFormat := props["response_format"]
			if !hasFormat {
				if _, skip := noResponseFormatNeeded[name]; !skip {
					warnings = append(warnings, name+": has `limit` but no `response_format`")
				}
			}
		}
	}

	fmt.Printf("linted %d tools, %d parameters\n", len(tools), params)
	for _, w := range warnings {
		fmt.Println("  WARN  " + w)
	}
	for _, f := range failures {
		fmt.Println("  FAIL  " + f)
	}
	if len(failures) > 0 {
		fmt.Printf("\n%d failure(s)\n", len(failures))
		return 1
	}
	fmt.Printf("\nall gates passed (%d warning(s))\n", len(warnings))
	return 0
}

func smoke(r *rpc) int {
	all, err := r.tools()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var tools []map[string]any
	for _, t := range all {
		name, _ := t["name"].(string)
		if _, skip := skipInSmoke[name]; skip {
			continue
		}
		tools = append(tools, t)
	}
	sort.Slice(tools, func(i, j int) bool {
		a, _ := tools[i]["name"].(string)
		b, _ := tools[j]["name"].(string)
		return a < b
	})

	fmt.Printf("%-30s %7s %6s  %s\n", "tool", "chars", "~tok", "status")
	total := 0
	var failures []string
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		text, err := r.toolCall(name, smokeArgs[name])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		total += len(text)
		status := "ok"
		var parsed any
		if err := json.Unmarshal([]byte(text), &parsed); err != nil {
			status = "not JSON"
			failures = append(failures, name)
		} else if m, ok := parsed.(map[string]any); ok {
			if errVal, has := m["error"]; has && errVal != nil && errVal != "" {
				s := fmt.Sprint(errVal)
				if len(s) > 60 {
					s = s[:60]
				}
				status = "ERROR " + s
				failures = append(failures, name)
			}
		}
		if len(text) > maxDefaultResponseChars {
			status += fmt.Sprintf("  OVERSIZED (>%d chars by default)", maxDefaultResponseChars)
			failures = append(failures, name)
		}
		fmt.Printf("%-30s %7s %6s  %s\n", name, comma(len(text)), comma(len(text)/4), status)
	}

	fmt.Printf("\ntotal if every tool were called once: %s chars (~%s tokens)\n", comma(total), comma(total/4))
	if len(failures) > 0 {
		seen := map[string]struct{}{}
		var uniq []string
		for _, n := range failures {
			if _, ok := seen[n]; ok {
				continue
			}
			seen[n] = struct{}{}
			uniq = append(uniq, n)
		}
		sort.Strings(uniq)
		fmt.Printf("%d tool(s) need attention: %v\n", len(uniq), uniq)
		return 1
	}
	fmt.Println("all read tools healthy")
	return 0
}

func comma(n int) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := strconv.Itoa(n)
	var b strings.Builder
	pre := len(s) % 3
	if pre == 0 && len(s) > 0 {
		pre = 3
	}
	b.WriteString(s[:pre])
	for i := pre; i < len(s); i += 3 {
		b.WriteByte(',')
		b.WriteString(s[i : i+3])
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("evals", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	url := fs.String("url", defaultURL, "MCP endpoint")
	token := fs.String("token", "", "Bearer token (or EVE_MCP_TOKEN)")

	if len(args) == 0 {
		fs.Usage()
		return 2
	}
	gate := args[0]
	if gate == "-h" || gate == "--help" {
		fmt.Fprint(os.Stdout, usage)
		return 0
	}
	if gate != "lint" && gate != "smoke" && gate != "all" {
		fmt.Fprintf(os.Stderr, "unknown gate %q (want lint, smoke, or all)\n", gate)
		fs.Usage()
		return 2
	}
	if err := fs.Parse(args[1:]); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	tok := *token
	if tok == "" {
		tok = os.Getenv("EVE_MCP_TOKEN")
	}
	client := newRPC(*url, tok)
	switch gate {
	case "lint":
		return lint(client)
	case "smoke":
		return smoke(client)
	default:
		if code := lint(client); code != 0 {
			return code
		}
		return smoke(client)
	}
}
