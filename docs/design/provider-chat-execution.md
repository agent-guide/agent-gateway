# Scoped eino chat execution

Capability: Implemented.

`pkg/llm/provider/einochat` shares only Generate invocation, the existing
non-stream RetryProviderCall policy, SDK error normalization and ChatResponse
wrapping, plus one-shot stream opening. Each vendor builder still returns its
selected ToolCallingChatModel, messages and model options. The helper has no
vendor switches and does not construct requests or declare capabilities.

| Provider | Shared execution | Behavior retained locally |
| --- | --- | --- |
| OpenAI | Generate and stream opening | finish_reason completion/truncation check; native Responses |
| Qwen | Generate and stream opening | thinking/extra-field merge and request override priority |
| DeepSeek | Generate and stream opening | developer normalization and thinking defaults; Responses-via-chat |
| Grok | Generate and stream opening | model-specific reasoning/sampling; native Responses shaping |
| OpenRouter | Generate and stream opening | component, headers and native Responses support |
| Ollama | Generate and stream opening | dedicated component and BaseURL conversion; Responses-via-chat |
| Zhipu | Generate | API profile/capabilities, stream construction flag, tool_stream and diagnostic logging; Responses-via-chat |

The builder executes again on each existing non-stream retry. Builder failures
retain their old treatment, and upstream Generate failures use the same error
normalizer/status wrapper as before extraction. Existing retry counts, delay and
cancellation error mapping are unchanged. Stream opening is never retried and
returns the original reader; the helper cannot consume, buffer, convert or retry
partial output. OpenAI applies its existing finish check outside the helper.
Zhipu keeps its distinct stream/logging flow rather than adding callbacks or
vendor branches to share those few lines.

Anthropic, Claude Code, Codex and Gemini are outside this extraction. Native
Responses remain distinct from Responses-via-chat adapters. Native protocol
state, tools, SSE semantics, signatures and relay eligibility are unchanged.
Tests cover retry construction/counts, client errors, message identity/tool calls,
stream opening and midstream failure, existing cancellation mapping, vendor
parameter suites, native relay fidelity and OpenAI truncation behavior.
