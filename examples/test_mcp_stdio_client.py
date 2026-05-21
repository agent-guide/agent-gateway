#!/usr/bin/env python3
"""MCP stdio gateway client example.

Sends MCP JSON-RPC requests to the agent-gateway over HTTP, routing them to a
stdio-backed upstream MCP process (e.g. @modelcontextprotocol/server-filesystem).

Prerequisites
-------------
1. Install the filesystem MCP server:
       npm install -g @modelcontextprotocol/server-filesystem

2. Apply the MCP bundle (creates the service, route, and virtual key):
       agwctl gateway apply -f examples/gateway.bundle.mcp.yaml

3. Retrieve the generated virtual key value:
       agwctl gateway list virtual-keys

4. Run the gateway:
       ./agw run --config ./Caddyfile.example

Usage
-----
    export AGW_API_KEY=<virtual-key-value>
    python3 examples/test_mcp_stdio_client.py

    # List available tools only
    python3 examples/test_mcp_stdio_client.py --list-tools

    # Call a specific tool
    python3 examples/test_mcp_stdio_client.py --call-tool list_directory --params '{"path": "/tmp"}'

    # Target a different gateway or path prefix
    python3 examples/test_mcp_stdio_client.py --base-url http://127.0.0.1:8080 --path-prefix /mcp/fs
"""

import argparse
import json
import os
import sys
import urllib.error
import urllib.request
from typing import Any


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Test agent-gateway stdio MCP upstream via HTTP JSON-RPC."
    )
    parser.add_argument(
        "--base-url",
        default=os.getenv("AGW_BASE_URL", "http://127.0.0.1:8080"),
        help="Gateway base URL (no trailing slash).",
    )
    parser.add_argument(
        "--path-prefix",
        default=os.getenv("AGW_MCP_PATH", "/mcp/fs"),
        help="MCP route path prefix configured in the gateway.",
    )
    parser.add_argument(
        "--api-key",
        default=os.getenv("AGW_API_KEY", ""),
        help="Virtual key value. Sent as Authorization: Bearer <key>.",
    )
    parser.add_argument(
        "--timeout",
        type=float,
        default=float(os.getenv("AGW_TIMEOUT", "30")),
        help="Request timeout in seconds.",
    )
    parser.add_argument(
        "--list-tools",
        action="store_true",
        help="List available tools and exit.",
    )
    parser.add_argument(
        "--call-tool",
        metavar="TOOL_NAME",
        default="",
        help="Call a named tool and print the result.",
    )
    parser.add_argument(
        "--params",
        default="{}",
        help='JSON object of tool parameters (used with --call-tool). Default: {}',
    )
    return parser.parse_args()


_request_id = 0


def next_id() -> int:
    global _request_id
    _request_id += 1
    return _request_id


def send(url: str, api_key: str, timeout: float, method: str, params: Any = None) -> Any:
    """Send a single MCP JSON-RPC request and return the result field."""
    payload = {
        "jsonrpc": "2.0",
        "id": next_id(),
        "method": method,
    }
    if params is not None:
        payload["params"] = params

    body = json.dumps(payload).encode()
    headers = {"Content-Type": "application/json"}
    if api_key:
        headers["Authorization"] = f"Bearer {api_key}"

    req = urllib.request.Request(url, data=body, headers=headers, method="POST")
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            data = json.loads(resp.read())
    except urllib.error.HTTPError as exc:
        raw = exc.read()
        try:
            data = json.loads(raw)
        except Exception:
            print(f"HTTP {exc.code}: {raw.decode(errors='replace')}", file=sys.stderr)
            raise SystemExit(1)
    except urllib.error.URLError as exc:
        print(f"Connection error: {exc.reason}", file=sys.stderr)
        raise SystemExit(1)

    if "error" in data:
        err = data["error"]
        print(f"MCP error {err.get('code')}: {err.get('message')}", file=sys.stderr)
        raise SystemExit(1)

    return data.get("result")


def do_initialize(url: str, api_key: str, timeout: float) -> dict:
    result = send(url, api_key, timeout, "initialize", {
        "protocolVersion": "2024-11-05",
        "clientInfo": {"name": "test_mcp_stdio_client", "version": "0.1"},
        "capabilities": {},
    })
    # Fire notifications/initialized (no response expected — gateway handles it)
    notif = {
        "jsonrpc": "2.0",
        "method": "notifications/initialized",
        "params": {},
    }
    body = json.dumps(notif).encode()
    headers = {"Content-Type": "application/json"}
    if api_key:
        headers["Authorization"] = f"Bearer {api_key}"
    req = urllib.request.Request(url, data=body, headers=headers, method="POST")
    try:
        with urllib.request.urlopen(req, timeout=timeout):
            pass
    except Exception:
        pass  # notifications return 204 or are silently accepted
    return result or {}


def do_list_tools(url: str, api_key: str, timeout: float) -> list[dict]:
    result = send(url, api_key, timeout, "tools/list")
    return result.get("tools", []) if result else []


def do_call_tool(url: str, api_key: str, timeout: float, name: str, params: dict) -> Any:
    return send(url, api_key, timeout, "tools/call", {"name": name, "arguments": params})


def main() -> int:
    args = parse_args()
    if not args.api_key:
        print(
            "AGW_API_KEY is required. Retrieve the virtual key value with:\n"
            "  agwctl gateway list virtual-keys",
            file=sys.stderr,
        )
        return 1

    url = args.base_url.rstrip("/") + args.path_prefix

    # Initialize the MCP session
    print(f"Connecting to MCP gateway at {url} ...")
    init = do_initialize(url, args.api_key, args.timeout)
    server_info = init.get("serverInfo", {})
    proto = init.get("protocolVersion", "unknown")
    print(f"Connected: server={server_info.get('name', '?')} version={server_info.get('version', '?')} protocol={proto}")
    print()

    if args.call_tool:
        try:
            tool_params = json.loads(args.params)
        except json.JSONDecodeError as exc:
            print(f"Invalid --params JSON: {exc}", file=sys.stderr)
            return 1
        print(f"Calling tool '{args.call_tool}' with params: {tool_params}")
        result = do_call_tool(url, args.api_key, args.timeout, args.call_tool, tool_params)
        print(json.dumps(result, indent=2, ensure_ascii=False))
        return 0

    # Default: list tools (also the only action when --list-tools is passed)
    tools = do_list_tools(url, args.api_key, args.timeout)
    if not tools:
        print("No tools returned by the upstream MCP server.")
        return 0

    print(f"Available tools ({len(tools)}):")
    for tool in tools:
        name = tool.get("name", "?")
        desc = tool.get("description", "")
        print(f"  {name}: {desc}")

    if not args.list_tools:
        # Quick demo: call the first tool with empty params to show a full round-trip
        first = tools[0]["name"]
        print()
        print(f"Demo: calling '{first}' with empty params ...")
        try:
            result = do_call_tool(url, args.api_key, args.timeout, first, {})
            print(json.dumps(result, indent=2, ensure_ascii=False))
        except SystemExit:
            print("(tool call returned an error — pass --call-tool and --params for a valid invocation)")

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
