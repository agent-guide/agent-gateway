#!/usr/bin/env python3
"""Anthropic-compatible Python client for testing the Agent Gateway."""

import argparse
import os
import sys

try:
    import anthropic
except ImportError:
    print("Missing dependency: install with `python3 -m pip install anthropic`", file=sys.stderr)
    raise SystemExit(1)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Test caddy-agent-gateway with the Anthropic Python SDK.")
    parser.add_argument(
        "--base-url",
        default=os.getenv("AGENT_GATEWAY_BASE_URL", "http://127.0.0.1:8082"),
        help="Gateway Anthropic-compatible base URL (without /v1).",
    )
    parser.add_argument(
        "--api-key",
        default=os.getenv("AGENT_GATEWAY_API_KEY", "test-key"),
        help="Local Agent Gateway API key. Sent as x-api-key header.",
    )
    parser.add_argument(
        "--model",
        default=os.getenv("AGENT_GATEWAY_MODEL", "claude-sonnet-4-6"),
        help="Model allowed by the gateway route.",
    )
    parser.add_argument(
        "--prompt",
        default="用一句中文回答：2 + 2 等于几？",
        help="User prompt to send.",
    )
    parser.add_argument(
        "--system",
        default=os.getenv("AGENT_GATEWAY_SYSTEM_PROMPT", ""),
        help="Optional system prompt.",
    )
    parser.add_argument(
        "--stream",
        action="store_true",
        help="Test streaming messages.",
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    client = anthropic.Anthropic(
        api_key=args.api_key,
        base_url=args.base_url,
    )

    # Pass content as an explicit content block so the gateway always receives
    # the array format regardless of the SDK version in use.
    kwargs: dict = {
        "model": args.model,
        "max_tokens": 128,
        "messages": [{"role": "user", "content": [{"type": "text", "text": args.prompt}]}],
    }
    if args.system:
        kwargs["system"] = args.system

    try:
        if args.stream:
            print("Streaming response:")
            with client.messages.stream(**kwargs) as stream:
                for text in stream.text_stream:
                    print(text, end="", flush=True)
            print()
            return 0

        message = client.messages.create(**kwargs)
        if not hasattr(message, "content") or not isinstance(message.content, list):
            # Guard against SDK versions that return raw response data on error.
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


if __name__ == "__main__":
    raise SystemExit(main())
