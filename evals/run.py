#!/usr/bin/env python3
"""Eval harness for the EVE MCP server.

Two deterministic gates that run without a model:

    python evals/run.py lint    # tool definitions meet the quality bar
    python evals/run.py smoke   # every read tool answers, and what it costs

The agentic tasks in tasks.yaml need a model in the loop; see evals/README.md.
Exit code is non-zero when a gate fails, so this is CI-usable.
"""
from __future__ import annotations

import argparse
import json
import sys
import urllib.error
import urllib.request

DEFAULT_URL = "http://127.0.0.1:8765/mcp"

#: A tool description shorter than this is almost certainly not explaining
#: when to use it, only what it is.
MIN_DESCRIPTION_CHARS = 120
#: Above this a single tool is crowding out the rest of the tool list.
MAX_DESCRIPTION_CHARS = 2000
#: A read call costing more than this by default is not respecting context.
MAX_DEFAULT_RESPONSE_CHARS = 6000

#: Tools whose rows are already minimal — every field is high-signal, so a
#: concise/detailed split would return the same thing twice.
NO_RESPONSE_FORMAT_NEEDED = {
    "eve_character_standings",
    "eve_industry_mining",
    "eve_universe_search",
    "eve_universe_hotspots",
}

#: Tools that mutate game state, or need arguments only a human can supply.
SKIP_IN_SMOKE = {
    "eve_auth_logout",
    "eve_auth_login_url",
    "eve_mail_read",
    "eve_ui_set_waypoint",
    "eve_ui_open_window",
    "eve_fitting_save",
    "eve_fitting_delete",
    "eve_mail_mark",
    "eve_mail_delete",
    "eve_mail_send",
    "eve_contacts_set",
    "eve_contacts_delete",
    "eve_calendar_respond",
}

#: Minimal arguments for tools that require some.
SMOKE_ARGS = {
    "eve_market_price": {"item": "Tritanium"},
    "eve_universe_item": {"item": "Rifter"},
    "eve_universe_system": {"system": "Jita"},
    "eve_universe_route": {"origin": "Jita", "destination": "Amarr"},
    "eve_universe_search": {"query": "Rifter"},
    "eve_assets_find": {"name": "Drake"},
}


class Rpc:
    def __init__(self, url: str, token: str = "") -> None:
        self.url = url
        self.token = token
        self._id = 0

    def __call__(self, method: str, params: dict | None = None) -> dict:
        self._id += 1
        payload = {"jsonrpc": "2.0", "id": self._id, "method": method, "params": params or {}}
        headers = {
            "Content-Type": "application/json",
            "Accept": "application/json, text/event-stream",
        }
        if self.token:
            headers["Authorization"] = f"Bearer {self.token}"
        request = urllib.request.Request(
            self.url, data=json.dumps(payload).encode(), headers=headers
        )
        try:
            raw = urllib.request.urlopen(request, timeout=120).read().decode()
        except urllib.error.URLError as exc:
            sys.exit(f"Cannot reach the MCP server at {self.url}: {exc}")
        for line in raw.splitlines():
            if line.startswith("data: "):
                return json.loads(line[6:])
        return json.loads(raw)

    def tools(self) -> list[dict]:
        return self("tools/list")["result"]["tools"]

    def call(self, name: str, args: dict) -> str:
        response = self("tools/call", {"name": name, "arguments": args})
        if "error" in response:
            return json.dumps(response["error"])
        result = response["result"]
        return "".join(c.get("text", "") for c in result.get("content", []))


def lint(rpc: Rpc) -> int:
    """Check the tool definitions against the practices we committed to."""
    tools = rpc.tools()
    failures: list[str] = []
    warnings: list[str] = []

    for tool in tools:
        name = tool["name"]
        description = tool.get("description", "")
        props: dict = tool["inputSchema"].get("properties", {})

        if not name.startswith("eve_"):
            failures.append(f"{name}: not namespaced under 'eve_'")
        if len(description) < MIN_DESCRIPTION_CHARS:
            failures.append(f"{name}: description is only {len(description)} chars")
        if len(description) > MAX_DESCRIPTION_CHARS:
            warnings.append(f"{name}: description is {len(description)} chars, consider trimming")
        if "\n    " in description or description != description.strip():
            failures.append(f"{name}: description carries raw docstring indentation")

        for param, spec in props.items():
            if not spec.get("description"):
                failures.append(f"{name}.{param}: no description in the schema")
            # Game ids are opaque 64-bit values with no meaningful upper bound;
            # only tunables like `limit` benefit from a declared range.
            if (
                spec.get("type") == "integer"
                and "maximum" not in spec
                and not param.endswith("_id")
            ):
                warnings.append(f"{name}.{param}: unbounded integer, no maximum in schema")
            if param in ("user", "id", "target_id", "data", "input"):
                warnings.append(f"{name}.{param}: ambiguous parameter name")

        # Any tool that can return a long list should let the caller shrink it.
        if (
            "limit" in props
            and "response_format" not in props
            and name not in NO_RESPONSE_FORMAT_NEEDED
        ):
            warnings.append(f"{name}: has `limit` but no `response_format`")

    print(f"linted {len(tools)} tools, {sum(len(t['inputSchema'].get('properties', {})) for t in tools)} parameters")
    for warning in warnings:
        print(f"  WARN  {warning}")
    for failure in failures:
        print(f"  FAIL  {failure}")
    if failures:
        print(f"\n{len(failures)} failure(s)")
        return 1
    print(f"\nall gates passed ({len(warnings)} warning(s))")
    return 0


def smoke(rpc: Rpc) -> int:
    """Call every read tool with its defaults and report health plus cost."""
    tools = [t for t in rpc.tools() if t["name"] not in SKIP_IN_SMOKE]
    failures = []
    print(f"{'tool':30} {'chars':>7} {'~tok':>6}  status")
    total = 0
    for tool in sorted(tools, key=lambda t: t["name"]):
        name = tool["name"]
        text = rpc.call(name, SMOKE_ARGS.get(name, {}))
        total += len(text)
        status = "ok"
        try:
            parsed = json.loads(text)
            if isinstance(parsed, dict) and parsed.get("error"):
                status = f"ERROR {parsed['error'][:60]}"
                failures.append(name)
        except json.JSONDecodeError:
            status = "not JSON"
            failures.append(name)
        if len(text) > MAX_DEFAULT_RESPONSE_CHARS:
            status += f"  OVERSIZED (>{MAX_DEFAULT_RESPONSE_CHARS} chars by default)"
            failures.append(name)
        print(f"{name:30} {len(text):>7,} {len(text)//4:>6,}  {status}")

    print(f"\ntotal if every tool were called once: {total:,} chars (~{total//4:,} tokens)")
    if failures:
        print(f"{len(failures)} tool(s) need attention: {sorted(set(failures))}")
        return 1
    print("all read tools healthy")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("gate", choices=["lint", "smoke", "all"])
    parser.add_argument("--url", default=DEFAULT_URL)
    parser.add_argument("--token", default="", help="EVE_MCP_TOKEN, if the server requires one")
    args = parser.parse_args()

    rpc = Rpc(args.url, args.token)
    if args.gate == "lint":
        return lint(rpc)
    if args.gate == "smoke":
        return smoke(rpc)
    return lint(rpc) or smoke(rpc)


if __name__ == "__main__":
    sys.exit(main())
