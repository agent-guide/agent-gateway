# LLM Request Flow

This page records the current LLM request path at a high level.

## Flow

1. `http.handlers.agent_route_dispatcher` receives the HTTP request.
2. The dispatcher resolves the matching route by host, path prefix, and method.
3. The route `protocol` selects the LLM wire-format handler.
4. The request path is rewritten by removing the matched `path_prefix`.
5. The gateway extracts and validates the VirtualKey from `Authorization: Bearer` or `x-api-key` when required.
6. The protocol handler converts the HTTP payload into the internal provider request.
7. The request model (or route default) selects an explicitly exposed ProviderModel or LogicalModel.
8. A concrete model fixes the binding; a logical model selects an eligible registered candidate. The gateway rewrites only the attempt-local upstream model.
9. The selected provider executes `Chat` or `StreamChat`.
10. The protocol handler converts the provider response back to protocol-specific JSON or SSE.

## Current Supported LLM Endpoints

- OpenAI-compatible:
  - `POST /v1/chat/completions`
  - `POST /v1/responses`
  - `GET /v1/models`
- Anthropic-compatible:
  - `POST /v1/messages`

Current endpoint limits:

- OpenAI `/v1/embeddings` is recognized by path matching but not fully wired through the serving path
- on `protocol=cc` routes, `POST /v1/messages/count_tokens` returns a local
  estimate without resolving or calling a provider; the standard
  `protocol=anthropic` profile recognizes the endpoint but returns
  `501 Not Implemented`
