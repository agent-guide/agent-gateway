# LLM Request Flow

This page records the current LLM request path at a high level.

## Flow

1. `http.handlers.agent_route_dispatcher` receives the HTTP request.
2. The dispatcher resolves the matching route by host, path prefix, and method.
3. The route `protocol` selects the LLM wire-format handler.
4. The request path is rewritten by removing the matched `path_prefix`.
5. The gateway extracts and validates the VirtualKey from `Authorization: Bearer` or `x-api-key` when required.
6. The protocol handler converts the HTTP payload into the internal provider request.
7. In direct-provider mode, the gateway uses `target_policy.provider_target.provider_id` and treats the request `model` as the upstream model name.
8. In logical-model mode, the model catalog resolves the request model name to one concrete provider/model binding and rewrites the upstream model.
9. The selected provider executes `Chat` or `StreamChat`.
10. The protocol handler converts the provider response back to protocol-specific JSON or SSE.

## Current Supported LLM Endpoints

- OpenAI-compatible:
  - `POST /v1/chat/completions`
- Anthropic-compatible:
  - `POST /v1/messages`

Current partial support:

- OpenAI `/v1/models` and `/v1/embeddings` are recognized by path matching but not fully wired through the serving path
- Anthropic `POST /v1/messages/count_tokens` returns `501 Not Implemented`
