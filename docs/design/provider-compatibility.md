# Provider compatibility dimensions

Capability status: **Implemented** for the behavior described below.

## Separate owners

Repeated Claude/Codex product names describe separate configuration dimensions:

| Dimension | Example | Owner and meaning |
| --- | --- | --- |
| Client ingress | Route `protocol: cc` | Dispatcher CC profile over the shared Anthropic Messages core; includes the local `count_tokens` estimate |
| Upstream LLM | Provider `provider_type: claudecode` or `codex` | Provider authentication, native protocol, request shaping and capabilities |
| Provider compatibility | `options.compact: cc` or `codex` | Selected provider's client compatibility shaping; not context compaction |
| ACP execution | Agent `runtime.acp.agent_type: codex` | Native Agent execution through `codex-acp`; independent of the LLM provider |

These dimensions can be combined independently where the requested protocol
features are supported. A `cc` route does not require a `claudecode` provider or
an ACP Agent. A Codex LLM provider is a Responses implementation; its name does
not make it a Chat Completions provider or an ACP process. Native fidelity and
candidate feature filtering still govern which combinations can serve a request.

## Current option support

The configuration key remains `compact`. `CompactModeFromOptions` recognizes
`cc`, `codex`, and `none`, ignoring case and surrounding whitespace. Missing or
empty string values resolve to `none`. Constructors that call this parser
reject unknown strings and non-string values, including null. The parser's
existence does not imply that every constructor calls it.

| Provider | `cc` | `codex` | Missing / empty / `none` | Invalid string or non-string |
| --- | --- | --- | --- | --- |
| `openai`, `qwen`, `deepseek`, `grok`, `openrouter`, `zhipu` | Accepted and applied to Chat construction: strip unsupported `metadata` and `user` extra fields | Accepted but ignored | Accepted; shaping disabled | Constructor error |
| `codex` | Accepted and applied: filter unsupported Claude Code stateful tools/tool choices and suppress their corresponding translated stream calls | Accepted but ignored | Accepted; CC shaping disabled | Constructor error |
| `claudecode` | Accepted but ignored | Accepted and applied: translate Codex tool names to Claude Code names and restore response names | Accepted; Codex shaping disabled | Constructor error |
| `anthropic`, `gemini`, `ollama` | Accepted but ignored | Accepted but ignored | Accepted; no compatibility shaping | Accepted but ignored; no strict compact-option validation |

The first row's shaping occurs in each provider's Chat model construction, not
in every native protocol implementation. In particular, Grok native Responses
does not acquire Chat shaping merely because it shares `openaibase.Base`.
Responses-via-Chat traverses Chat construction, while native Responses remains
a separate capability. Codex also applies native request sanitization regardless
of compatibility mode; `none` does not disable its upstream protocol rules.

`openaibase.NewBase` records a `CCCompat` flag. Each of the six Chat providers
actually uses it; Ollama embeds Base but does not consume that flag in its
dedicated eino component path. Invalid options become `none` through the tolerant
`ProviderConfig.CompactMode` helper in Base; that is not a constructor error for
Ollama. Anthropic and Gemini do not use the compact parser.

Implementation owners are [the parser](../../pkg/llm/provider/options.go),
[Base](../../pkg/llm/provider/openaibase/provider.go), each provider's
`newChatModel` construction, [Codex](../../pkg/llm/provider/codex/provider.go),
and [Claude Code](../../pkg/llm/provider/claudecode/provider.go). The CC and
Anthropic ingress profiles continue sharing the Messages lifecycle.

## Contract decision

Retain `compact` for this inventory; no option rename or new rejection rule is
introduced here. The key's meaning is client compatibility, with the explicit
provider support matrix above. Any later rename or stricter inapplicable-mode
policy is a separate configuration-contract change requiring raw inspection,
offline repair for existing data, and coordinated parser/import/export/example
updates. The runtime must not grow dual-read aliases for an ordinary field
rename, and unknown options must not be advertised as effective governance.
