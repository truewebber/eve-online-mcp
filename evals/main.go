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
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/domain/j"
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

	rpcTimeout    = 120 * time.Second
	errorPreview  = 60
	charsPerToken = 4
	thousandGroup = 3
	exitUsage     = 2
)

// Tools whose rows are already minimal — every field is high-signal, so a
// concise/detailed split would return the same thing twice.
func needsResponseFormat(name string) bool {
	switch name {
	case "eve_character_standings", "eve_industry_mining", "eve_universe_search", "eve_universe_hotspots":
		return false
	default:
		return true
	}
}

func skipInSmoke(name string) bool {
	switch name {
	case "eve_auth_logout", "eve_auth_login_url", "eve_mail_read",
		"eve_ui_set_waypoint", "eve_ui_open_window",
		"eve_fitting_save", "eve_fitting_delete",
		"eve_mail_mark", "eve_mail_delete", "eve_mail_send",
		"eve_contacts_set", "eve_contacts_delete", "eve_calendar_respond":
		return true
	// Corporation reads need in-game roles the default smoke character may lack.
	// eve_corp_overview is not listed: it answers even for an NPC corp.
	case "eve_corp_assets_list", "eve_corp_assets_find", "eve_corp_blueprints",
		"eve_corp_wallet", "eve_corp_industry_jobs", "eve_corp_mining",
		"eve_corp_orders", "eve_corp_contracts", "eve_corp_killmails",
		"eve_corp_structures", "eve_corp_members":
		return true
	default:
		return false
	}
}

func smokeArgs(name string) map[string]any {
	switch name {
	case "eve_market_price":
		return map[string]any{"item": "Tritanium"}
	case "eve_universe_item":
		return map[string]any{"item": "Rifter"}
	case "eve_universe_system":
		return map[string]any{"system": "Jita"}
	case "eve_universe_route":
		return map[string]any{"origin": "Jita", "destination": "Amarr"}
	case "eve_universe_search":
		return map[string]any{"query": "Rifter"}
	case "eve_assets_find":
		return map[string]any{"name": "Drake"}
	default:
		return nil
	}
}

const usage = `Eval harness for the EVE MCP server.

Usage:
  evals <lint|smoke|all> [--url URL] [--token TOKEN]

Flags:
  --url    MCP endpoint (default http://127.0.0.1:8765/mcp)
  --token  Bearer token; falls back to EVE_MCP_TOKEN

lint checks tool definitions. smoke calls every read tool. all runs both.
`

var (
	errUnreachable = errors.New("cannot reach the MCP server")
	errBadMCPURL   = errors.New("MCP URL must be http(s) with a host")
)

type rpc struct {
	endpoint *url.URL
	token    string
	client   *http.Client
	id       int
}

func parseMCPURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse MCP URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errBadMCPURL
	}
	if u.Host == "" {
		return nil, errBadMCPURL
	}

	return &url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path, RawPath: u.RawPath, RawQuery: u.RawQuery}, nil
}

func newRPC(raw, token string) (*rpc, error) {
	u, err := parseMCPURL(raw)
	if err != nil {
		return nil, err
	}

	return &rpc{
		endpoint: u,
		token:    token,
		client:   &http.Client{Timeout: rpcTimeout},
	}, nil
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
		return nil, fmt.Errorf("marshal RPC: %w", err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, r.endpoint.String(), bytes.NewReader(payload)) //nolint:gosec // CLI --url is the operator MCP endpoint
	if err != nil {
		return nil, fmt.Errorf("build RPC request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	resp, err := r.client.Do(req) //nolint:gosec // CLI --url is the operator MCP endpoint
	if err != nil {
		return nil, fmt.Errorf("cannot reach the MCP server at %s: %w", r.endpoint, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("cannot reach the MCP server at %s: %w", r.endpoint, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w at %s: HTTP Error %d: %s", errUnreachable, r.endpoint, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	body := string(raw)
	for line := range strings.SplitSeq(body, "\n") {
		if strings.HasPrefix(line, "data: ") {
			var out map[string]any
			err := json.Unmarshal([]byte(line[6:]), &out)
			if err != nil {
				return nil, fmt.Errorf("decode SSE: %w", err)
			}

			return out, nil
		}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode RPC: %w", err)
	}

	return out, nil
}

func (r *rpc) tools() ([]map[string]any, error) {
	msg, err := r.call("tools/list", nil)
	if err != nil {
		return nil, err
	}
	result := j.Map(msg["result"])
	raw := j.Slice(result["tools"])
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
		raw, err := json.Marshal(errObj)
		if err != nil {
			return "", fmt.Errorf("marshal tool error: %w", err)
		}

		return string(raw), nil
	}
	result := j.Map(msg["result"])
	content := j.Slice(result["content"])
	var b strings.Builder
	for _, c := range content {
		item := j.Map(c)
		b.WriteString(j.Str(item["text"]))
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
		f, w, n := lintTool(tool)
		failures = append(failures, f...)
		warnings = append(warnings, w...)
		params += n
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

func lintTool(tool map[string]any) ([]string, []string, int) {
	name := j.Str(tool["name"])
	description := j.Str(tool["description"])
	schema := j.Map(tool["inputSchema"])
	props := j.Map(schema["properties"])
	if props == nil {
		props = map[string]any{}
	}
	var failures, warnings []string
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
	f, w := lintProps(name, props)
	failures = append(failures, f...)
	warnings = append(warnings, w...)

	return failures, warnings, len(props)
}

func lintProps(name string, props map[string]any) ([]string, []string) {
	var failures, warnings []string
	for param, specAny := range props {
		spec := j.Map(specAny)
		if j.Str(spec["description"]) == "" {
			failures = append(failures, name+"."+param+": no description in the schema")
		}
		// Game ids are opaque 64-bit values with no meaningful upper bound;
		// only tunables like `limit` benefit from a declared range.
		if j.Str(spec["type"]) == "integer" {
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
		if !hasFormat && needsResponseFormat(name) {
			warnings = append(warnings, name+": has `limit` but no `response_format`")
		}
	}

	return failures, warnings
}

func smoke(r *rpc) int {
	all, err := r.tools()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}
	var tools []map[string]any
	for _, t := range all {
		name := j.Str(t["name"])
		if skipInSmoke(name) {
			continue
		}
		tools = append(tools, t)
	}
	sort.Slice(tools, func(i, k int) bool {
		a := j.Str(tools[i]["name"])
		b := j.Str(tools[k]["name"])

		return a < b
	})

	fmt.Printf("%-30s %7s %6s  %s\n", "tool", "chars", "~tok", "status")
	total := 0
	var failures []string
	for _, tool := range tools {
		name := j.Str(tool["name"])
		text, err := r.toolCall(name, smokeArgs(name))
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
				if len(s) > errorPreview {
					s = s[:errorPreview]
				}
				status = "ERROR " + s
				failures = append(failures, name)
			}
		}
		if len(text) > maxDefaultResponseChars {
			status += fmt.Sprintf("  OVERSIZED (>%d chars by default)", maxDefaultResponseChars)
			failures = append(failures, name)
		}
		fmt.Printf("%-30s %7s %6s  %s\n", name, comma(len(text)), comma(len(text)/charsPerToken), status)
	}

	fmt.Printf("\ntotal if every tool were called once: %s chars (~%s tokens)\n", comma(total), comma(total/charsPerToken))
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
	pre := len(s) % thousandGroup
	if pre == 0 && len(s) > 0 {
		pre = thousandGroup
	}
	b.WriteString(s[:pre])
	for i := pre; i < len(s); i += thousandGroup {
		b.WriteByte(',')
		b.WriteString(s[i : i+thousandGroup])
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

		return exitUsage
	}
	gate := args[0]
	if gate == "-h" || gate == "--help" {
		fmt.Fprint(os.Stdout, usage)

		return 0
	}
	if gate != "lint" && gate != "smoke" && gate != "all" {
		fmt.Fprintf(os.Stderr, "unknown gate %q (want lint, smoke, or all)\n", gate)
		fs.Usage()

		return exitUsage
	}
	err := fs.Parse(args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}

		return exitUsage
	}
	tok := *token
	if tok == "" {
		tok = os.Getenv("EVE_MCP_TOKEN")
	}
	client, err := newRPC(*url, tok)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return exitUsage
	}
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
