#!/usr/bin/env python3
"""Python client for testing the Agent Gateway (OpenAI and Anthropic providers).

Supports an optional --mcp mode that connects to an MCP gateway endpoint,
fetches available tools, passes them to the LLM, and routes tool calls back
through the MCP gateway — exercising both the LLM and MCP gateway paths.
"""

import argparse
import http.client
import json
import os
import sys
import urllib.parse
from typing import Any

PROVIDER_OPENAI = "openai"
PROVIDER_ANTHROPIC = "anthropic"

_API_TO_PROVIDER = {
    "chat": PROVIDER_OPENAI,
    "responses": PROVIDER_OPENAI,
    "message": PROVIDER_ANTHROPIC,
}

DEFAULT_BASE_URL = {
    PROVIDER_OPENAI: "http://127.0.0.1:8080/v1",
    PROVIDER_ANTHROPIC: "http://127.0.0.1:8080",
}
DEFAULT_MODEL = {
    PROVIDER_OPENAI: "gpt-4.1",
    PROVIDER_ANTHROPIC: "claude-sonnet-4-6",
}


def _provider_from_api(api: str) -> str:
    return _API_TO_PROVIDER[api]


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Test agent-gateway with OpenAI or Anthropic Python SDK, optionally via MCP tools."
    )
    parser.add_argument(
        "--api",
        choices=("chat", "message", "responses"),
        default=os.getenv("AGW_API", "chat"),
        help=(
            "API surface to use: 'chat' (OpenAI chat completions), "
            "'message' (Anthropic messages), 'responses' (OpenAI responses)."
        ),
    )
    parser.add_argument(
        "--base-url",
        default=os.getenv("AGW_BASE_URL", ""),
        help="Gateway LLM base URL. Defaults per provider.",
    )
    parser.add_argument(
        "--api-key",
        default=os.getenv("AGW_API_KEY", ""),
        help="Local Agent Gateway virtual key.",
    )
    parser.add_argument(
        "--model",
        default=os.getenv("AGW_MODEL", ""),
        help="Model allowed by the gateway LLM route.",
    )
    parser.add_argument(
        "--prompt",
        default="用一句中文回答：2 + 2 等于几？",
        help="User prompt to send.",
    )
    parser.add_argument(
        "--system",
        default=os.getenv("AGW_SYSTEM_PROMPT", ""),
        help="Optional system prompt.",
    )
    parser.add_argument(
        "--stream",
        action="store_true",
        help="Test streaming responses (disabled when --mcp is used).",
    )
    parser.add_argument(
        "--timeout",
        type=float,
        default=float(os.getenv("AGW_TIMEOUT", "30")),
        help="Client timeout in seconds.",
    )
    parser.add_argument(
        "--max-tokens",
        type=int,
        default=int(os.getenv("AGW_MAX_TOKENS", "128")),
        help="Maximum output tokens for the request.",
    )
    # MCP options
    parser.add_argument(
        "--mcp",
        action="store_true",
        help=(
            "Enable MCP tool-use mode: fetch tools from the MCP gateway, "
            "let the LLM call them, and route results back through the gateway."
        ),
    )
    parser.add_argument(
        "--mcp-url",
        default=os.getenv("AGW_MCP_URL", "http://127.0.0.1:8080/mcp/fs"),
        help="MCP gateway endpoint URL (default: http://127.0.0.1:8080/mcp/fs).",
    )
    parser.add_argument(
        "--mcp-api-key",
        default=os.getenv("AGW_MCP_API_KEY", ""),
        help="Virtual key for the MCP route. Defaults to --api-key if not set.",
    )

    args = parser.parse_args()

    provider = _provider_from_api(args.api)
    if not args.base_url:
        args.base_url = DEFAULT_BASE_URL[provider]
    if not args.model:
        args.model = DEFAULT_MODEL[provider]
    if not args.mcp_api_key:
        args.mcp_api_key = args.api_key

    return args


# ---------------------------------------------------------------------------
# MCP session
# ---------------------------------------------------------------------------

_rpc_id = 0


def _next_rpc_id() -> int:
    global _rpc_id
    _rpc_id += 1
    return _rpc_id


def post_json(url: str, body: bytes, headers: dict[str, str], timeout: float) -> tuple[int, dict[str, str], bytes]:
    parsed = urllib.parse.urlparse(url)
    if parsed.scheme not in {"http", "https"}:
        raise RuntimeError(f"unsupported URL scheme: {parsed.scheme}")

    path = parsed.path or "/"
    if parsed.query:
        path = f"{path}?{parsed.query}"

    port = parsed.port
    if port is None:
        port = 443 if parsed.scheme == "https" else 80

    conn_cls = http.client.HTTPSConnection if parsed.scheme == "https" else http.client.HTTPConnection
    conn = conn_cls(parsed.hostname, port, timeout=timeout)
    try:
        conn.request("POST", path, body=body, headers=headers)
        resp = conn.getresponse()
        raw_headers = {k: v for k, v in resp.getheaders()}
        payload = resp.read()
        return resp.status, raw_headers, payload
    finally:
        conn.close()


class MCPSession:
    """Stateful MCP JSON-RPC session over HTTP (Streamable HTTP transport)."""

    def __init__(self, url: str, api_key: str, timeout: float) -> None:
        self.url = url
        self.api_key = api_key
        self.timeout = timeout
        self.session_id: str | None = None

    def _headers(self) -> dict[str, str]:
        h = {"Content-Type": "application/json"}
        if self.api_key:
            h["Authorization"] = f"Bearer {self.api_key}"
            h["x-api-key"] = self.api_key
        if self.session_id:
            h["MCP-Session-Id"] = self.session_id
        return h

    def _rpc(self, method: str, params: Any = None) -> tuple[Any, Any]:
        payload: dict[str, Any] = {"jsonrpc": "2.0", "id": _next_rpc_id(), "method": method}
        if params is not None:
            payload["params"] = params
        body = json.dumps(payload).encode()
        headers = self._headers()
        print(
            f"MCP request {method}: auth={'Authorization' in headers} x-api-key={'x-api-key' in headers} session={'MCP-Session-Id' in headers}",
            file=sys.stderr,
        )
        try:
            status, resp_headers, raw = post_json(self.url, body, headers, self.timeout)
            data = json.loads(raw)
        except OSError as exc:
            raise RuntimeError(f"MCP connection error: {exc}")
        except Exception as exc:
            raise RuntimeError(f"MCP request failed: {exc}")
        if status >= 400:
            try:
                data = json.loads(raw)
            except Exception:
                raise RuntimeError(f"HTTP {status}: {raw.decode(errors='replace')}")
        if "error" in data:
            err = data["error"]
            if isinstance(err, dict):
                raise RuntimeError(f"MCP error {err.get('code')}: {err.get('message')}")
            raise RuntimeError(f"MCP error: {err}")
        return data.get("result"), resp_headers

    def _notify(self, method: str, params: Any = None) -> None:
        payload: dict[str, Any] = {"jsonrpc": "2.0", "method": method}
        if params is not None:
            payload["params"] = params
        body = json.dumps(payload).encode()
        headers = self._headers()
        try:
            _, _, _ = post_json(self.url, body, headers, self.timeout)
        except Exception:
            pass

    def initialize(self) -> dict:
        result, headers = self._rpc("initialize", {
            "protocolVersion": "2024-11-05",
            "clientInfo": {"name": "test_llm_client", "version": "0.1"},
            "capabilities": {},
        })
        sid = headers.get("MCP-Session-Id") or headers.get("Mcp-Session-Id")
        if sid:
            self.session_id = sid
        self._notify("notifications/initialized", {})
        return result or {}

    def list_tools(self) -> list[dict]:
        result, _ = self._rpc("tools/list")
        return (result or {}).get("tools", [])

    def call_tool(self, name: str, arguments: dict) -> str:
        result, _ = self._rpc("tools/call", {"name": name, "arguments": arguments})
        if not result:
            return ""
        parts = [c.get("text", "") for c in result.get("content", []) if c.get("type") == "text"]
        return "\n".join(parts)


# ---------------------------------------------------------------------------
# Tool format converters
# ---------------------------------------------------------------------------

def _mcp_to_anthropic_tool(tool: dict) -> dict:
    return {
        "name": tool["name"],
        "description": tool.get("description", ""),
        "input_schema": tool.get("inputSchema", {"type": "object", "properties": {}}),
    }


def _mcp_to_openai_tool(tool: dict) -> dict:
    return {
        "type": "function",
        "function": {
            "name": tool["name"],
            "description": tool.get("description", ""),
            "parameters": tool.get("inputSchema", {"type": "object", "properties": {}}),
        },
    }


def _anthropic_block_to_dict(block: Any) -> dict:
    if block.type == "text":
        return {"type": "text", "text": block.text}
    if block.type == "tool_use":
        return {"type": "tool_use", "id": block.id, "name": block.name, "input": block.input}
    return block.model_dump()


# ---------------------------------------------------------------------------
# OpenAI provider
# ---------------------------------------------------------------------------

def _require_openai() -> Any:
    try:
        from openai import OpenAI
        return OpenAI
    except ImportError:
        print("Missing dependency: install with `python3 -m pip install openai`", file=sys.stderr)
        raise SystemExit(1)


def _print_responses_output(resp: Any) -> None:
    output_text = getattr(resp, "output_text", None)
    if output_text:
        print(output_text)
        return
    for item in getattr(resp, "output", []) or []:
        if getattr(item, "type", "") != "message":
            continue
        for part in getattr(item, "content", []) or []:
            text = getattr(part, "text", None)
            if text:
                print(text)


def run_openai(args: argparse.Namespace, messages: list[dict[str, Any]]) -> int:
    OpenAI = _require_openai()
    client = OpenAI(api_key=args.api_key, base_url=args.base_url, timeout=args.timeout, max_retries=0)

    if args.api == "responses":
        return _run_openai_responses(client, args, messages)
    return _run_openai_chat(client, args, messages)


def _run_openai_chat(client: Any, args: argparse.Namespace, messages: list[dict[str, Any]]) -> int:
    request: dict[str, Any] = {
        "model": args.model,
        "messages": messages,
        "max_tokens": args.max_tokens,
    }
    if not args.model.startswith("gpt-5"):
        request["temperature"] = 0.2

    if args.stream:
        print(f"Streaming chat completion (timeout={args.timeout}s, max_tokens={args.max_tokens}):")
        stream = client.chat.completions.create(**request, stream=True)
        for chunk in stream:
            if not chunk.choices:
                continue
            delta = chunk.choices[0].delta
            if delta and delta.content:
                print(delta.content, end="", flush=True)
        print()
        return 0

    print(f"Requesting chat completion (timeout={args.timeout}s, max_tokens={args.max_tokens})...")
    completion = client.chat.completions.create(**request)
    print(completion.choices[0].message.content)
    if completion.usage:
        print(f"usage: {completion.usage}")
    return 0


def _run_openai_responses(client: Any, args: argparse.Namespace, messages: list[dict[str, Any]]) -> int:
    try:
        responses_api = client.responses
    except AttributeError:
        print("This OpenAI SDK does not expose client.responses; upgrade with `python3 -m pip install -U openai`.", file=sys.stderr)
        return 1

    request: dict[str, Any] = {
        "model": args.model,
        "input": messages,
        "max_output_tokens": args.max_tokens,
    }

    if args.stream:
        print(f"Streaming response (timeout={args.timeout}s, max_output_tokens={args.max_tokens}):")
        stream = responses_api.create(**request, stream=True)
        for event in stream:
            if getattr(event, "type", "") == "response.output_text.delta":
                print(getattr(event, "delta", ""), end="", flush=True)
        print()
        return 0

    print(f"Requesting response (timeout={args.timeout}s, max_output_tokens={args.max_tokens})...")
    resp = responses_api.create(**request)
    _print_responses_output(resp)
    if getattr(resp, "usage", None):
        print(f"usage: {resp.usage}")
    return 0


def run_openai_with_mcp(args: argparse.Namespace, messages: list[dict[str, Any]], mcp: MCPSession) -> int:
    OpenAI = _require_openai()
    client = OpenAI(api_key=args.api_key, base_url=args.base_url, timeout=args.timeout, max_retries=0)

    raw_tools = mcp.list_tools()
    openai_tools = [_mcp_to_openai_tool(t) for t in raw_tools]
    print(f"MCP tools: {[t['function']['name'] for t in openai_tools]}")
    print()

    history: list[Any] = list(messages)
    request: dict[str, Any] = {
        "model": args.model,
        "messages": history,
        "max_tokens": args.max_tokens,
        "tools": openai_tools,
        "tool_choice": "auto",
    }
    if not args.model.startswith("gpt-5"):
        request["temperature"] = 0.2

    while True:
        print(f"Requesting chat completion with tools (max_tokens={args.max_tokens})...")
        completion = client.chat.completions.create(**request)
        msg = completion.choices[0].message

        if not msg.tool_calls:
            print(msg.content)
            if completion.usage:
                print(f"usage: {completion.usage}")
            return 0

        # Add assistant turn with tool calls
        asst_dict: dict[str, Any] = {
            "role": "assistant",
            "content": msg.content,
            "tool_calls": [
                {"id": tc.id, "type": "function", "function": {"name": tc.function.name, "arguments": tc.function.arguments}}
                for tc in msg.tool_calls
            ],
        }
        history = list(request["messages"]) + [asst_dict]

        for tc in msg.tool_calls:
            tool_args = json.loads(tc.function.arguments)
            print(f"[MCP] → {tc.function.name}({tool_args})")
            result_text = mcp.call_tool(tc.function.name, tool_args)
            print(f"[MCP] ← {result_text!r}")
            history.append({"role": "tool", "tool_call_id": tc.id, "content": result_text})

        request["messages"] = history


# ---------------------------------------------------------------------------
# Anthropic provider
# ---------------------------------------------------------------------------

def _require_anthropic() -> Any:
    try:
        import anthropic
        return anthropic
    except ImportError:
        print("Missing dependency: install with `python3 -m pip install anthropic`", file=sys.stderr)
        raise SystemExit(1)


def run_anthropic(args: argparse.Namespace, messages: list[dict[str, Any]]) -> int:
    anthropic = _require_anthropic()
    client = anthropic.Anthropic(api_key=args.api_key, base_url=args.base_url)

    kwargs: dict[str, Any] = {
        "model": args.model,
        "max_tokens": args.max_tokens,
        "messages": messages,
    }
    if args.system:
        kwargs["system"] = args.system

    try:
        if args.stream:
            print(f"Streaming response (timeout={args.timeout}s, max_tokens={args.max_tokens}):")
            with client.messages.stream(**kwargs) as stream:
                for text in stream.text_stream:
                    print(text, end="", flush=True)
            print()
            return 0

        print(f"Requesting message (timeout={args.timeout}s, max_tokens={args.max_tokens})...")
        message = client.messages.create(**kwargs)
        if not hasattr(message, "content") or not isinstance(message.content, list):
            print(f"Unexpected response type {type(message).__name__}: {message!r}", file=sys.stderr)
            return 1
        for block in message.content:
            if block.type == "text":
                print(block.text)
        if message.usage:
            print(f"usage: input_tokens={message.usage.input_tokens}, output_tokens={message.usage.output_tokens}")
    except anthropic.APIStatusError as e:
        print(f"API error {e.status_code}: {e.message}", file=sys.stderr)
        return 1
    except anthropic.APIConnectionError as e:
        print(f"Connection error: {e}", file=sys.stderr)
        return 1
    except Exception as e:
        print(f"Unexpected error ({type(e).__name__}): {e}", file=sys.stderr)
        return 1
    return 0


def run_anthropic_with_mcp(args: argparse.Namespace, messages: list[dict[str, Any]], mcp: MCPSession) -> int:
    anthropic = _require_anthropic()
    client = anthropic.Anthropic(api_key=args.api_key, base_url=args.base_url)

    raw_tools = mcp.list_tools()
    anthropic_tools = [_mcp_to_anthropic_tool(t) for t in raw_tools]
    print(f"MCP tools: {[t['name'] for t in anthropic_tools]}")
    print()

    kwargs: dict[str, Any] = {
        "model": args.model,
        "max_tokens": args.max_tokens,
        "messages": list(messages),
        "tools": anthropic_tools,
    }
    if args.system:
        kwargs["system"] = args.system

    try:
        while True:
            print(f"Requesting message with tools (max_tokens={args.max_tokens})...")
            message = client.messages.create(**kwargs)

            tool_use_blocks = [b for b in message.content if b.type == "tool_use"]
            if not tool_use_blocks:
                for b in message.content:
                    if b.type == "text":
                        print(b.text)
                if message.usage:
                    print(f"usage: input_tokens={message.usage.input_tokens}, output_tokens={message.usage.output_tokens}")
                return 0

            # Append assistant turn
            kwargs["messages"] = list(kwargs["messages"]) + [
                {"role": "assistant", "content": [_anthropic_block_to_dict(b) for b in message.content]}
            ]

            # Execute tool calls and collect results
            tool_results = []
            for block in tool_use_blocks:
                print(f"[MCP] → {block.name}({block.input})")
                result_text = mcp.call_tool(block.name, block.input)
                print(f"[MCP] ← {result_text!r}")
                tool_results.append({
                    "type": "tool_result",
                    "tool_use_id": block.id,
                    "content": [{"type": "text", "text": result_text}],
                })

            kwargs["messages"] = list(kwargs["messages"]) + [
                {"role": "user", "content": tool_results}
            ]

    except anthropic.APIStatusError as e:
        print(f"API error {e.status_code}: {e.message}", file=sys.stderr)
        return 1
    except anthropic.APIConnectionError as e:
        print(f"Connection error: {e}", file=sys.stderr)
        return 1
    except RuntimeError as e:
        print(f"MCP error: {e}", file=sys.stderr)
        return 1
    except Exception as e:
        print(f"Unexpected error ({type(e).__name__}): {e}", file=sys.stderr)
        return 1


# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------

def main() -> int:
    args = parse_args()
    if not args.api_key:
        print("AGW_API_KEY is required because virtual key values are generated by the gateway.", file=sys.stderr)
        return 1

    provider = _provider_from_api(args.api)

    messages: list[dict[str, Any]] = []
    if args.system and provider == PROVIDER_OPENAI:
        messages.append({"role": "system", "content": args.system})
    if provider == PROVIDER_OPENAI:
        messages.append({"role": "user", "content": args.prompt})
    else:
        messages.append({"role": "user", "content": [{"type": "text", "text": args.prompt}]})

    if args.mcp:
        key_hint = f"{args.mcp_api_key[:4]}****" if args.mcp_api_key else "(none — set AGW_MCP_API_KEY or pass --mcp-api-key)"
        print(f"Initializing MCP session at {args.mcp_url}  key={key_hint}")
        mcp = MCPSession(args.mcp_url, args.mcp_api_key, args.timeout)
        try:
            info = mcp.initialize()
        except RuntimeError as e:
            print(f"MCP init failed: {e}", file=sys.stderr)
            if "not allowed" in str(e) or "virtual key" in str(e):
                print(
                    "Hint: the virtual key may not be authorized for this MCP route.\n"
                    "Use --mcp-api-key <key> or AGW_MCP_API_KEY to supply a separate key\n"
                    "that has the MCP route in its allowed_route_ids.",
                    file=sys.stderr,
                )
            return 1
        server = info.get("serverInfo", {})
        print(f"MCP server: {server.get('name', '?')} {server.get('version', '?')}  session={mcp.session_id!r}")
        print()

        if provider == PROVIDER_ANTHROPIC:
            return run_anthropic_with_mcp(args, messages, mcp)
        return run_openai_with_mcp(args, messages, mcp)

    if provider == PROVIDER_ANTHROPIC:
        return run_anthropic(args, messages)
    return run_openai(args, messages)


if __name__ == "__main__":
    raise SystemExit(main())
