#!/usr/bin/env python3
"""Minimal Streamable HTTP MCP server for local testing.

Implements the same handler logic as the Go version (fake_mcp_server.go was removed).

Usage:
    python3 examples/fake_mcp_server.py [-addr :8765]

Then point your MCP service URL at http://localhost:8765/mcp.
"""

import argparse
import itertools
import json
import logging
from http.server import BaseHTTPRequestHandler, HTTPServer

logging.basicConfig(level=logging.INFO, format="%(message)s")
log = logging.getLogger(__name__)

_session_counter = itertools.count(1)

_TOOLS = [
    {
        "name": "echo",
        "description": "Echoes the input text back.",
        "inputSchema": {
            "type": "object",
            "properties": {"text": {"type": "string", "description": "Text to echo"}},
            "required": ["text"],
        },
    },
    {
        "name": "add",
        "description": "Adds two numbers.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "a": {"type": "number"},
                "b": {"type": "number"},
            },
            "required": ["a", "b"],
        },
    },
]


class MCPHandler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        pass  # silence default access log; we use our own

    def do_POST(self):
        if self.path != "/mcp":
            self.send_error(404)
            return

        length = int(self.headers.get("Content-Length", 0))
        try:
            msg = json.loads(self.rfile.read(length))
        except Exception as exc:
            self.send_error(400, str(exc))
            return

        method = msg.get("method", "")
        session_id = self.headers.get("MCP-Session-Id", "")
        log.info("← %s  session=%r", method, session_id)

        if method == "initialize":
            sid = f"fake-sid-{next(_session_counter)}"
            self._json({"jsonrpc": "2.0", "id": msg.get("id"), "result": {
                "protocolVersion": "2025-11-25",
                "capabilities": {"tools": {}},
                "serverInfo": {"name": "fake-mcp", "version": "0.1"},
            }}, extra_headers={"MCP-Session-Id": sid})
            log.info("→ initialize OK  sid=%s", sid)

        elif method == "notifications/initialized":
            self.send_response(202)
            self.end_headers()
            log.info("→ 202 Accepted")

        elif method == "tools/list":
            self._json({"jsonrpc": "2.0", "id": msg.get("id"), "result": {"tools": _TOOLS}})
            log.info("→ tools/list OK")

        elif method == "tools/call":
            params = msg.get("params") or {}
            name = params.get("name", "")
            args = params.get("arguments") or {}

            if name == "echo":
                text = args.get("text", "")
            elif name == "add":
                text = str(args.get("a", 0) + args.get("b", 0))
            else:
                text = f"unknown tool: {name}"

            self._json({"jsonrpc": "2.0", "id": msg.get("id"), "result": {
                "content": [{"type": "text", "text": text}],
            }})
            log.info("→ tools/call %s → %r", name, text)

        else:
            self.send_response(202)
            self.end_headers()
            log.info("→ 202 Accepted (unknown method)")

    def _json(self, payload: dict, extra_headers: dict | None = None):
        body = json.dumps(payload).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        for k, v in (extra_headers or {}).items():
            self.send_header(k, v)
        self.end_headers()
        self.wfile.write(body)


def main():
    parser = argparse.ArgumentParser(description="Fake MCP HTTP server for local testing.")
    parser.add_argument("--addr", default=":8765", help="Listen address (default: :8765)")
    args = parser.parse_args()

    host, _, port_str = args.addr.rpartition(":")
    host = host or "localhost"
    port = int(port_str)

    server = HTTPServer((host, port), MCPHandler)
    log.info("fake MCP server listening on %s  (endpoint: http://%s:%d/mcp)", args.addr, host, port)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()
