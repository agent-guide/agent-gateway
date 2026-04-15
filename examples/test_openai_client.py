#!/usr/bin/env python3
"""OpenAI-compatible Python client for testing the Agent Gateway."""

import argparse
import os
import sys

try:
    from openai import OpenAI
except ImportError:
    print("Missing dependency: install with `python3 -m pip install openai`", file=sys.stderr)
    raise SystemExit(1)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Test caddy-agent-gateway with the OpenAI Python SDK.")
    parser.add_argument(
        "--base-url",
        default=os.getenv("AGENT_GATEWAY_BASE_URL", "http://127.0.0.1:8082/v1"),
        help="Gateway OpenAI-compatible base URL.",
    )
    parser.add_argument(
        "--api-key",
        default=os.getenv("AGENT_GATEWAY_API_KEY", "test-key"),
        help="Local Agent Gateway API key. Sent as Authorization: Bearer <key>.",
    )
    parser.add_argument(
        "--model",
        default=os.getenv("AGENT_GATEWAY_MODEL", "gpt-4.1"),
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
        help="Optional system prompt. Disabled by default for broad provider compatibility.",
    )
    parser.add_argument(
        "--stream",
        action="store_true",
        help="Test streaming chat completions.",
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    client = OpenAI(api_key=args.api_key, base_url=args.base_url)

    messages = []
    if args.system:
        messages.append({"role": "system", "content": args.system})
    messages.append({"role": "user", "content": args.prompt})

    request = {
        "model": args.model,
        "messages": messages,
        "max_tokens": 128,
        "temperature": 0.2,
    }

    if args.stream:
        print("Streaming response:")
        stream = client.chat.completions.create(**request, stream=True)
        for chunk in stream:
            if not chunk.choices:
                continue
            delta = chunk.choices[0].delta
            if delta and delta.content:
                print(delta.content, end="", flush=True)
        print()
        return 0

    completion = client.chat.completions.create(**request)
    print(completion.choices[0].message.content)
    if completion.usage:
        print(f"usage: {completion.usage}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
