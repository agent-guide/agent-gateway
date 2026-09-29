# Protocol Support Strategy

Capability status: **Implemented**.

## 1. Purpose

This document defines how `agent-gateway` separates and extends its public
protocol families. It is not an implementation roadmap. A new protocol is
accepted only when it has a clear product boundary, protocol owner, route
semantics, governance behavior, and observability contract.

## 2. Current Protocol Families

| Family | Ingress | Runtime owner | Primary role |
|---|---|---|---|
| OpenAI-compatible | Chat Completions and Responses HTTP APIs | `pkg/dispatcher/llmapi/openai` | LLM resource access |
| Anthropic-compatible | Messages API; count-tokens is recognized but unsupported | shared Messages core plus `anthropic` profile | LLM resource access |
| Claude Code-compatible | Messages-compatible `cc` profile with local count-tokens estimate | shared Messages core plus `cc` profile | Claude Code client ingress |
| MCP | Streamable HTTP JSON-RPC ingress | dispatcher plus `pkg/mcp/service` | tool/resource/prompt access |
| Agent common turn | `protocol=agent` SSE turn API | `pkg/agent/runtime` backend selected by Agent | runtime-neutral Agent execution |
| Native A2A | `protocol=a2a` A2A 1.0 JSON-RPC/SSE | dispatcher plus `pkg/a2a/proxy` | governed native HTTP Agent access |

ACP is an Agent runtime protocol, not a separate public route family. ACP and
builtin Agents use the common Agent turn contract. HTTP Agents can use that
translated contract or native A2A ingress.

## 3. Separation Principles

### 3.1 Protocol parsing stays at the edge

Wire validation, request parsing, response encoding, streaming state, and
protocol error mapping belong to the owning protocol adapter. Provider and
runtime layers do not inspect arbitrary inbound JSON to rediscover protocol
requirements.

### 3.2 Route kind and route protocol are separate

Route kind selects the broad execution family (`llm`, `mcp`, or `agent`).
Route protocol selects the caller-facing wire contract within that family.
Runtime type separately answers who owns an Agent's lifecycle.

Adding a dialect must not create a runtime type merely because it has a new
wire format.

### 3.3 Shared internal types do not erase native semantics

Normalized provider and Agent contracts enable routing and common policy, but
protocol-native state is retained when normalization would lose required
identity, ordering, history, or extension fields. Native relay remains
validated and governed; it is not an unchecked bypass.

### 3.4 Governance precedes execution

Every public protocol path defines:

- exact route matching and admission order;
- VirtualKey and rate-limit behavior;
- request and response size limits;
- credential termination and replacement;
- trace/depth propagation;
- bounded error classification and usage accounting.

A transparent proxy that bypasses these rules is not a gateway protocol
adapter.

### 3.5 Capability reporting is fail-closed

Routes and runtime views advertise only behavior that the selected provider,
service, or Agent can execute. Unsupported operations return a typed failure;
they do not silently fall through to a different protocol or runtime.

## 4. Extension Contract

A new ingress protocol requires:

1. a durable design defining its wire and governance invariants;
2. an owning adapter package with no control-plane persistence authority;
3. route protocol registration and consistency validation;
4. protocol-correct streaming and pre/post-commit failure behavior;
5. bounded request, response, event, and timeout behavior;
6. credential and hop-by-hop header rules where HTTP is involved;
7. typed usage-event projection and bounded Prometheus labels;
8. reference documentation and an executable end-to-end test;
9. explicit binary linkage for registered factories or codecs.

If the work is actively scheduled, its order and delivery gates belong in a
time-bound file under `docs/plans/`. Once shipped, that plan is deleted and
release notes record the delivered capability.

## 5. Caddy And Standalone Assembly

Both binaries assemble the same reusable dispatcher and gateway runtime. Caddy
owns HTTP server lifecycle and module loading; `agwd` owns conventional
`net/http` assembly. Protocol handlers and runtime backends remain reusable
below those adapters, and both assembly paths must expose the same configured
capabilities.

Factory-registered protocol codecs, providers, and custom builtin Agents
require explicit blank imports in every binary that promises them. A feature
is not supported merely because its package exists in the module graph.

## 6. Unsupported Protocol Expansion

The current strategy does not imply support for:

- arbitrary OpenAI/Anthropic endpoint pass-through;
- A2A v0.3, A2A REST, or A2A gRPC;
- a standalone public ACP route family;
- gateway-owned durable workflow or Agent task protocols;
- automatic translation between every Agent runtime and every ingress
  protocol;
- protocol support implemented solely by Caddy `reverse_proxy`.

Each such addition requires an explicit design rather than an entry in a build
order.

## 7. Decision Summary

- Keep LLM, MCP, common Agent, and native A2A ingress as explicit protocol
  families.
- Keep ACP, HTTP, and builtin as Agent runtime types selected behind the Agent
  control plane.
- Reuse shared routing, credentials, limits, tracing, and observability without
  collapsing protocol-native semantics.
- Add protocols through bounded adapters and honest capabilities, not generic
  passthrough.
- Track scheduled delivery in temporary plans and delivered history in release
  notes.
