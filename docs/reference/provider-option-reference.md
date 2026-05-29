# Provider Option Reference

This page lists the current common provider config fields and notable provider-specific options.

## Common Provider Fields

These fields are parsed from provider blocks in the Caddyfile and are also reflected in provider config objects:

- `provider_type`
- `api_key`
- `base_url`
- `default_model`
- `request_timeout_seconds`
- `max_retries`
- `retry_delay_seconds`
- `max_idle_connections`
- `max_idle_connections_per_host`
- `idle_keep_alive_timeout_seconds`
- `proxy_url`
- `header <name> <value>`
- `option <key> <value>`

## Field Notes

`provider_type`

- required
- must match the actual mounted provider module type

`api_key`

- provider-local fallback API key
- does not register as a managed credential

`base_url`

- overrides the provider default base URL

`default_model`

- provider default upstream model
- useful as an operator default, but route behavior still determines how request models are interpreted

Network tuning fields:

- `request_timeout_seconds`
- `max_retries`
- `retry_delay_seconds`
- `max_idle_connections`
- `max_idle_connections_per_host`
- `idle_keep_alive_timeout_seconds`
- `proxy_url`

Extra outbound request shaping:

- `header <name> <value>` adds a persistent extra HTTP header
- `option <key> <value>` sets provider-specific options and is parsed as strings in the Caddyfile

## Built-In Provider Defaults

- `openai`: `https://api.openai.com/v1`
- `anthropic`: `https://api.anthropic.com`
- `codex`: `https://chatgpt.com/backend-api/codex`
- `openrouter`: `https://openrouter.ai/api/v1`
- `deepseek`: `https://api.deepseek.com`
- `zhipu`: `https://open.bigmodel.cn/api/paas/v4`

## Notable Provider-Specific Options

`codex`

- uses OpenAI-compatible `POST /responses`
- custom `base_url` must match the upstream codex-compatible deployment
- `option cc_compat true` enables Claude Code CLI compatibility mode by filtering stateful Claude Code tools that Codex-compatible upstreams do not reliably sequence

`deepseek`

- `option path <path>`
- `option response_format_type <text|json_object>`
- tuning options such as `max_tokens`, `temperature`, `top_p`, `presence_penalty`, `frequency_penalty`, `log_probs`, and `top_log_probs`

`zhipu`

- `option thinking_type <disabled|enabled|none>`

## Current Built-In Provider Types

- `openai`
- `anthropic`
- `codex`
- `claudecode`
- `gemini`
- `ollama`
- `openrouter`
- `deepseek`
- `zhipu`

## Related Docs

- [../guides/providers.md](../guides/providers.md)
- [caddyfile-reference.md](caddyfile-reference.md)
