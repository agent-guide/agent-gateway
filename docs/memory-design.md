# Agent Gateway Memory Design

## 1. Scope

This document describes the design for route-level memory infrastructure in `agent-gateway`.

Memory is gateway infrastructure, not an MCP tool. Unlike MCP tools, which agents explicitly invoke as part of their reasoning, memory context is retrieved and injected by the gateway and new memory candidates are extracted by the gateway according to route policy.

This document is intentionally aligned with:

- `observability-design.md` for trace propagation and audit correlation
- `mcp-gateway-architecture.md` for shared dispatcher and routecore boundaries
- `mcp-tool-policy-design.md` for the route-level policy model pattern

The current implementation baseline is:

- LLM routing and dispatch through `caddy/dispatcher/` and `pkg/dispatcher/`
- shared route persistence through `pkg/gateway/routecore/`
- LLM route expansion through `pkg/gateway/llmroute/`
- Admin API in `pkg/admin`
- `/admin/memory/...` endpoints are currently stubbed (`501 Not Implemented`)
- an early memory abstraction already exists in `pkg/llm/memory/`

## 2. Goals

- Inject relevant memory context into eligible LLM requests before forwarding to the provider
- Extract and store new memory entries from eligible LLM responses, preferring async extraction
- support route-level memory policy for retrieval scope, token budget, injection style, extraction mode, and sharing rules
- support shared memory across multiple routes without requiring direct agent-to-agent coordination
- keep tenant, session, and trace boundaries explicit and auditable
- integrate with the unified observability model so each memory action can be correlated to the LLM interaction that caused it

## 3. Non-Goals

This design does not attempt to:

- expose memory as MCP tools
- apply memory policy to MCP protocol requests
- make every LLM ingress shape memory-aware in phase 1
- require the gateway to infer session identity from prompt text alone
- build a general-purpose standalone vector database product

## 4. Design Principles

### 4.1 Route-Level Policy, Shared Runtime Foundation

Like MCP tool policy, memory policy is owned by the route, not by the provider and not by the memory backend. The same provider may be exposed through different routes with different memory policies.

### 4.2 Semantic Work Is Delegated, Policy Stays In The Gateway

The gateway stays semantically thin. It may call an embedding model or a secondary LLM to support retrieval and extraction, but the gateway itself remains the policy and orchestration layer.

### 4.3 Identity Must Be Explicit

Memory retrieval and writes are only safe when the gateway can identify the tenant and session boundaries of the request. If those identities are unavailable, the gateway must degrade safely rather than guessing.

### 4.4 The Dispatcher Owns Routing, A Memory-Aware LLM Pipeline Owns Mutation

The current `pkg/dispatcher.Handler` resolves the route and selects the protocol handler, but it does not currently own the full provider request/response lifecycle. Memory cannot be implemented only as a small hook after `PrepareLLMApiRequest()` because extraction needs access to provider-facing requests, provider responses, and streaming completion state. The runtime must therefore introduce a memory-aware LLM execution pipeline below route dispatch and above protocol-specific response encoding.

## 5. Capability Matrix

Memory support is not uniform across all current LLM ingress shapes.

### 5.1 Supported In Phase 1

- OpenAI-compatible chat completions: retrieval and injection
- Anthropic messages: retrieval and injection

### 5.2 Supported In Phase 2

- OpenAI-compatible chat completions: async extraction
- Anthropic messages: async extraction

### 5.3 Deferred Or Restricted

- OpenAI-compatible Responses API: retrieval may be supported only through a dedicated adapter that can safely augment `instructions` or equivalent structured fields; extraction is deferred
- streaming extraction from client-visible SSE output: deferred
- inband extraction that changes the primary model output contract: opt-in only, not the default
- embeddings and models endpoints: never memory-aware

This document only defines required behavior for request shapes that carry conversational content and can be safely enriched without breaking protocol guarantees.

## 6. Architecture

### 6.1 High-Level Flow

Memory policy hooks into the LLM path at two points, but those hooks must live inside a memory-aware LLM execution pipeline rather than as dispatcher-only helpers:

```text
client LLM API request
  -> agent_route_dispatcher
  -> resolve AgentRoute
  -> validate VirtualKey
  -> resolve LLMRoute
  -> protocol handler parses inbound request
  -> memory-aware LLM pipeline
       |
       +-- [MEMORY RETRIEVAL]
       |      resolve tenant/session identity
       |      build retrieval query
       |      query memory backend
       |      inject memory into provider-facing request
       |
       +-- provider.Chat() / provider.StreamChat()
       |
       +-- collect response completion state when supported
       |
       +-- [MEMORY EXTRACTION]
              schedule async extraction from normalized conversation + response
  -> protocol handler writes normal client response
```

### 6.2 Why This Boundary Matters

`pkg/dispatcher/handler.go` currently resolves the route, calls `PrepareLLMApiRequest()`, resolves the routed provider, and then hands control to `ServeLLMApi(...)`. That shape is sufficient for routing, but insufficient for transparent extraction because the dispatcher no longer sees the final provider response or streaming completion.

Memory therefore requires one of these two implementation patterns:

1. introduce a shared LLM executor between dispatch and protocol encoding
2. refactor protocol handlers into explicit parse / execute / encode phases and place memory between parse and execute, with extraction fed from execute results

This document assumes option 2 because it preserves the current protocol-aware handler architecture described in `mcp-gateway-architecture.md` while creating a reusable execution seam.

### 6.3 Multi-Agent Shared Memory

Multiple routes may reference the same shared namespace:

```text
route: code-agent    -> read:  shared:project-alpha
route: review-agent  -> read:  shared:project-alpha
route: pm-agent      -> read:  shared:project-alpha

route: code-agent    -> write: route-private
                      -> promote high-confidence entries to shared:project-alpha
```

The gateway manages namespace routing. Agents do not coordinate directly.

## 7. Identity, Session, And Trace Model

Memory state must be keyed by explicit runtime identity, not by heuristic prompt parsing.

### 7.1 Trace Identity

Trace propagation follows `observability-design.md`.

Request extraction precedence is:

1. `traceparent` / `tracestate`
2. `X-Trace-ID` and `X-Span-ID`
3. gateway-generated trace context

Every memory retrieval or extraction audit record must carry the same `trace_id`, `span_id`, and `parent_span_id` as the LLM interaction that triggered it.

### 7.2 Tenant Identity

Memory isolation is tenant-scoped first, namespace-scoped second.

Phase 1 tenant identity is:

- `virtual_key_id` when the route requires a VirtualKey
- otherwise route-scoped only, with shared memory disabled by default

Shared namespaces must never cross tenant boundaries unless an explicit later-phase policy introduces a stronger tenant model than `virtual_key_id`.

### 7.3 Session Identity

The gateway must not infer session identity from prompt text.

Phase 1 session resolution precedence is:

1. explicit gateway header `X-Session-ID`
2. protocol-specific session field explicitly mapped by the ingress adapter, when one is later defined
3. optional route policy fallback to `trace_id`
4. no session identity

Default behavior:

- if no session identity is available, session-scoped retrieval and session-scoped writes are skipped
- route-private retrieval may still run if the route policy allows it

Using `trace_id` as session fallback is opt-in because traces model request lineage, not always conversation lineage.

### 7.4 Agent Identity

The gateway must distinguish route identity from agent identity.

- `route_id` identifies gateway policy ownership
- `agent_id` identifies the logical agent writer/reader when supplied by the caller or later orchestration layers

Phase 1 does not require `agent_id`. Route-private namespaces therefore key by route, not by inferred agent name.

## 8. Memory Policy Model

### 8.1 Configuration Shape

`memory_policy` is an optional LLM-route field. When absent, the route behaves exactly as before.

```json
{
  "id": "route:code-agent",
  "kind": "llm",
  "protocol": "anthropic",
  "memory_policy": {
    "identity": {
      "allow_trace_fallback_for_session": false
    },
    "retrieval": {
      "route_private": true,
      "shared_namespaces": ["shared:project-alpha"],
      "session_namespaces": ["session"],
      "top_k": 5,
      "token_budget": 800,
      "injection": "system_prompt_suffix",
      "format": "structured_summary"
    },
    "extraction": {
      "mode": "async_llm",
      "write_private": true,
      "write_session": true,
      "promote_to_shared_namespaces": ["shared:project-alpha"],
      "shared_promotion_min_confidence": 0.9,
      "async_model": "claude-haiku-4-5-20251001",
      "schema": {
        "facts": true,
        "preferences": true,
        "decisions": true,
        "corrections": false
      }
    }
  }
}
```

### 8.2 Namespace Kinds

Namespaces are logical policy names, not raw global IDs.

- `route-private`: storage partition derived from `(tenant_scope, route_id)`
- `shared:<scope>`: shared partition derived from `(tenant_scope, shared scope)`
- `session`: storage partition derived from `(tenant_scope, resolved session_id)`

This avoids embedding guessed caller identity into namespace strings and makes tenant isolation explicit.

### 8.3 Retrieval Policy

Fields:

- `route_private`: whether to query the route-private namespace
- `shared_namespaces`: ordered list of shared scopes to query
- `session_namespaces`: whether to query the resolved session namespace; phase 1 only supports `["session"]`
- `top_k`: maximum number of raw candidates before final ranking and token filtering
- `token_budget`: maximum tokens allocated to injected memory
- `injection`: where to inject context. Phase 1 supports `system_prompt_suffix` only
- `format`: how to format injected memory. Phase 1 supports `structured_summary`

### 8.4 Extraction Policy

Fields:

- `mode`: `rules` or `async_llm` in phase 2; `inband` is opt-in and not recommended
- `write_private`: write extracted entries to the route-private namespace
- `write_session`: write extracted entries to the resolved session namespace when session identity exists
- `promote_to_shared_namespaces`: shared scopes eligible for promotion
- `shared_promotion_min_confidence`: confidence threshold for shared promotion
- `async_model`: model used for asynchronous extraction
- `schema`: entry types enabled for extraction

## 9. Retrieval Design

### 9.1 Query Construction

The retrieval query is built from normalized conversational content, not from raw HTTP bodies.

Phase 1 query input:

- current system prompt or equivalent instruction field, when present
- the most recent user turns from the normalized protocol request

The gateway may use an embedding model to search semantically similar entries through the configured memory store.

### 9.2 Ranking

Retrieval ranking combines:

1. tenant match as a hard boundary
2. namespace class priority: session, route-private, shared
3. semantic similarity
4. recency
5. confidence

Session continuity is high priority only when a resolved session identity exists.

### 9.3 Injection Format

Phase 1 uses a structured summary format only.

Example:

```text
[Context from memory]
User preferences:
- prefers concise code explanations

Project context:
- repository is agent-gateway
- config store backend is SQLite

Recent decisions:
- MCP tool policy is route-level, not service-level
```

The gateway does not inject raw timestamped memory rows by default.

### 9.4 Token Budget Enforcement

When retrieved entries exceed `token_budget`:

1. keep higher-priority namespace classes first
2. keep higher-confidence and higher-relevance entries first
3. compress by type summary when available
4. drop lowest-value entries last

Phase 1 does not require LLM-based compression on the request path.

## 10. Extraction Design

### 10.1 Supported Extraction Modes

Phase 2 supports:

- `rules`: low-cost pattern extraction for explicit preferences, corrections, and simple facts
- `async_llm`: asynchronous extraction from normalized request/response content using a secondary model

### 10.2 Inband Extraction Is Not The Default

`inband` extraction is explicitly not the recommended default.

Reasons:

- it changes the primary model instruction contract
- it can break strict JSON or tool-calling responses
- it is difficult to make transparent for streaming protocols
- it couples extraction success to primary model compliance

If a later phase supports `inband`, it must be opt-in and limited to request shapes where protocol output guarantees are not compromised.

### 10.3 Extraction Input

Extraction operates on normalized interaction content:

- route identity
- tenant identity
- resolved session identity, if any
- normalized request messages or instruction fields
- normalized final assistant response content when available

Extraction must not depend on replaying raw client-visible SSE bytes.

### 10.4 Entry Types

Extracted entries are typed:

- `facts`
- `preferences`
- `decisions`
- `corrections`

Each type is independently enabled by route policy.

### 10.5 Extraction Is Not Storage

Extraction produces candidate entries. Before write:

- deduplicate by a stable semantic key, not only `subject + type`
- compare confidence, recency, and supersession
- write to private and session scopes first
- promote to shared scopes only when confidence meets the shared threshold

The store must support version-aware updates so later corrections or changed decisions do not silently overwrite history without attribution.

## 11. Storage Model

This design extends the existing `pkg/llm/memory/` abstraction rather than creating a parallel top-level memory subsystem.

Phase 1 and 2 storage must evolve `pkg/llm/memory` to support:

- explicit tenant scope
- explicit namespace class and namespace ID
- optional session ID
- typed entries
- confidence
- last-seen or last-confirmed timestamps
- semantic search metadata

Example logical entry:

```json
{
  "id": "mem_01jwz...",
  "tenant_scope": "vk_123",
  "namespace_kind": "route-private",
  "namespace_id": "route:code-agent",
  "type": "preference",
  "key": "code_style.conciseness",
  "value": "prefer concise code and avoid premature abstraction",
  "confidence": 0.9,
  "source_route": "route:code-agent",
  "session_id": "sess_abc123",
  "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
  "embedding": [...],
  "created_at": "2026-05-21T10:30:00Z",
  "last_confirmed_at": "2026-05-21T10:30:00Z"
}
```

## 12. Route Model And Persistence

Memory policy is route-level, but route persistence is shared through `pkg/gateway/routecore/`.

That means the implementation must not assume that adding a field only to `pkg/gateway/llmroute/types.go` is sufficient.

Required design rule:

- the persisted route representation must carry optional LLM-only `memory_policy`
- MCP routes ignore the field
- LLM route CRUD must round-trip the field losslessly

Whether that is implemented by adding `MemoryPolicy` directly to `routecore.AgentRouteConfig` or by introducing a route extension payload is an implementation detail. The architecture requirement is that shared route persistence owns the field, while LLM route validation owns its semantics.

## 13. Admin API

The current stub `/admin/memory/...` family is replaced with namespace-oriented endpoints:

- `GET /admin/memory/namespaces`
- `POST /admin/memory/namespaces`
- `GET /admin/memory/namespaces/{id}`
- `DELETE /admin/memory/namespaces/{id}`
- `GET /admin/memory/namespaces/{id}/entries`
- `DELETE /admin/memory/namespaces/{id}/entries/{entry_id}`
- `POST /admin/memory/namespaces/{id}/search`

Namespace admin objects should expose:

- namespace class
- tenant scope policy
- retention or TTL
- embedding configuration
- entry counts and last updated metadata

The existing LLM route CRUD endpoints must accept and return `memory_policy`.

## 14. Observability And Audit

Memory events are part of the unified observability model from `observability-design.md`, not a separate disconnected telemetry system.

Every retrieval or extraction record must carry:

- `trace_id`
- `span_id`
- `parent_span_id`
- `route_id`
- `virtual_key_id` when present
- resolved `session_id` when present

Recommended event shapes:

```text
MemoryRetrievalEvent
  trace_id            string
  span_id             string
  route_id            string
  virtual_key_id      string
  session_id          string
  namespace_classes   []string
  query_tokens        int
  entries_retrieved   int
  entries_injected    int
  tokens_injected     int
  latency_ms          int

MemoryExtractionEvent
  trace_id            string
  span_id             string
  route_id            string
  virtual_key_id      string
  session_id          string
  extraction_mode     string
  entries_extracted   int
  entries_written     int
  promoted_shared     []string
  latency_ms          int
```

These may be emitted as extension events associated with the parent LLM interaction rather than as an unrelated event stream.

## 15. Package Structure

The design extends existing memory runtime code instead of creating a competing package tree.

```text
pkg/gateway/llmroute/
    memory_policy.go          MemoryPolicy and validation

pkg/llm/memory/
    store.go                  extended core interfaces and entry types
    namespace.go              namespace config and resolution
    retriever.go              retrieval pipeline
    extractor.go              async extraction pipeline
    formatter.go              memory-to-prompt formatting
    audit.go                  memory observability helpers

pkg/dispatcher/
    handler.go                route resolution only
    llm_executor.go           shared execute seam for LLM requests
```

The important architectural point is not the exact filenames. It is that memory extends the existing `pkg/llm/memory` area and integrates into a shared LLM execution seam rather than living as a dispatcher-only patch.

## 16. Integration Points

### 16.1 Dispatcher

`pkg/dispatcher/handler.go` continues to:

- resolve the matched route
- validate the VirtualKey
- select the LLM protocol handler

It should not own response parsing for memory extraction.

### 16.2 LLM Execution Seam

A shared LLM execution seam must:

- accept the normalized protocol request
- run retrieval and injection before provider execution
- call the routed provider
- return normalized completion state to the protocol encoder
- schedule async extraction when supported

### 16.3 Protocol Handlers

Protocol handlers remain responsible for:

- decoding wire-format requests
- encoding protocol-specific responses
- exposing enough normalized request and response structure for the execution seam

## 17. Implementation Order

### Phase 1: Identity And Retrieval

Goal: safe route-level retrieval and injection for chat-style requests.

1. define `MemoryPolicy` and validation for LLM routes
2. extend shared route persistence to round-trip `memory_policy`
3. extend `pkg/llm/memory` with tenant, namespace, typed entry, and search support
4. add request identity resolution for `trace_id`, `virtual_key_id`, and optional `session_id`
5. introduce a shared LLM execution seam
6. implement retrieval, ranking, formatting, and injection for OpenAI chat completions and Anthropic messages
7. emit memory retrieval observability records correlated to the parent LLM interaction

### Phase 2: Async Extraction

Goal: write high-value memory without changing the client-visible response contract.

1. implement rules-based extraction
2. implement async LLM extraction from normalized interaction content
3. implement deduplication, supersession, and confidence thresholds
4. write to private and session scopes
5. emit extraction observability records

### Phase 3: Shared Promotion And Admin Management

Goal: controlled multi-route shared memory.

1. implement shared promotion policy and confidence gates
2. add namespace admin CRUD and debug search endpoints
3. add shared namespace inspection and retention management

### Later Phase

- optional `Responses` API adapter support
- optional opt-in `inband` extraction for narrowly safe request classes
- richer tenant identity beyond `virtual_key_id`

## 18. Relationship To Existing Documents

`observability-design.md`:

- trace propagation and event correlation follow the same `traceparent` / `X-Trace-ID` precedence
- memory events extend the same interaction observability model rather than creating a separate telemetry silo

`mcp-gateway-architecture.md`:

- memory policy follows the same routecore and dispatcher layering principles
- shared route persistence is owned by `pkg/gateway/routecore/`
- protocol-aware runtime behavior belongs in runtime execution layers, not in transparent proxy-style hooks

`mcp-tool-policy-design.md`:

- memory policy follows the same route-level ownership model as MCP tool policy
- route policy controls what context the model receives automatically, just as MCP tool policy controls what capabilities the model can invoke explicitly
- both are gateway-level behavior layers that must respect shared route foundations and protocol-aware runtime boundaries

## 19. Value Proposition And Positioning

### 19.1 The Core Distinction

Gateway memory and SDK/framework memory (LangChain, Mem0, LlamaIndex, MemGPT, etc.) differ fundamentally in **who owns memory control**: the application developer or the infrastructure operator.

Gateway memory is infrastructure. It operates below the application layer and is invisible to agents. SDK memory is application logic. It is part of agent reasoning and agents can explicitly invoke, query, and modify it.

Neither model is universally superior. The right choice depends on the target scenario.

### 19.2 Where Gateway Memory Has An Advantage

**Zero-intrusion integration**

Any LLM traffic routed through the gateway automatically receives memory without modifications to agent code, without introducing SDK dependencies, and without requiring agents to be redesigned. This is particularly valuable when onboarding existing agent systems.

**Multi-agent shared memory without agent coordination**

Shared namespaces such as `shared:project-alpha` are managed entirely by the gateway. Multiple routes (`code-agent`, `review-agent`, `pm-agent`) can share the same context without implementing any inter-agent communication protocol. SDK-based approaches require the application layer to design its own coordination scheme.

**Tenant isolation as infrastructure**

The `virtual_key_id → tenant_scope` boundary is a gateway primitive, not a developer responsibility. SDK-based tenant isolation is an application concern and is only as correct as each individual integration.

**Policy decoupled from application code**

Memory policy lives in route configuration. Platform teams can adjust `token_budget`, `top_k`, or `extraction_mode` without modifying or redeploying application code. SDK-based policy changes require code changes and a release cycle.

**Native observability correlation**

Memory events carry the same `trace_id` as the LLM interaction that triggered them. No application-layer instrumentation is required.

### 19.3 Where Gateway Memory Has A Disadvantage

**Limited semantic depth**

The gateway is intentionally semantically thin. Extraction relies on generic LLM calls. Frameworks such as Mem0 and MemGPT support domain-specific extraction prompts, custom memory schemas, and purpose-built models. When extraction quality is business-critical, application-layer memory gives more control.

**Agents cannot reason over their own memory**

In SDK-based systems, agents can explicitly say "remember this preference" or "forget that decision" — memory is part of the agent's reasoning loop. Gateway memory is opaque to agents. An agent cannot query, reflect on, or correct its own memory state. This is a hard constraint in scenarios that require agent-driven memory management.

**Session identity requires explicit propagation**

The gateway must not infer session identity from prompt text. Callers must send `X-Session-ID` explicitly or session-scoped memory is skipped entirely. SDK-based systems can derive session identity from application context such as user ID or session token without any additional protocol coordination.

**Injection point constraints**

Phase 1 supports only `system_prompt_suffix`. SDK-based memory can inject context as user messages, tool results, structured context blocks, or dynamically within a conversation. The optimal injection point varies by model and task type, and gateway memory cannot adapt to those differences in phase 1.

**Async extraction means memory is not available within the same session**

Async extraction means that memory written during the current conversation is not available until the next request. SDK-based approaches support synchronous memory updates within a session when the use case requires it.

**No migration path from existing application state**

Historical conversation data or existing memory state held in an application database cannot be directly imported into gateway memory namespaces without a custom migration. SDK-based systems can initialize memory directly from existing data stores.

### 19.4 Positioning Summary

Gateway memory is best suited as a **shared working memory layer** across agents: project context, cross-session preferences, team-level decisions. It is not intended to replace fine-grained agent-driven memory control within a single agent's reasoning process.

The two models are not mutually exclusive. A recommended composition pattern is:

- gateway memory handles cross-agent shared context and route-level preference injection automatically
- application-layer agents use MCP tools for explicit, agent-driven memory operations when precision and agent awareness are required

## 20. Effectiveness Evaluation

Memory effectiveness cannot be measured by system metrics alone. The evaluation strategy covers three layers: retrieval quality, injection value, and extraction quality.

### 20.1 Retrieval Quality

**Offline evaluation with labeled datasets**

Construct a labeled dataset: given a conversation context, annotate which memory entries should have been retrieved. Evaluate against this ground truth.

| Metric | Meaning |
|---|---|
| Recall@K | Fraction of labeled entries found in the top K results |
| MRR (Mean Reciprocal Rank) | Mean of the reciprocal rank of the first correct result |
| NDCG@K | Normalized discounted cumulative gain at rank K |

**Online proxy metrics (no labeling required)**

- **Injection rate**: `entries_injected / entries_retrieved`. A persistently low ratio indicates that retrieved entries are low quality or the token budget is too tight.
- **Hit stability**: within a session, similar queries should consistently retrieve overlapping entries. High volatility suggests ranking instability.

### 20.2 Injection Effectiveness

Injection effectiveness is the hardest layer to measure directly. The primary method is controlled experimentation.

**A/B testing at the route layer**

Split traffic at the route level, for example by virtual key or request percentage:

```
memory_policy.retrieval.enabled = true  → group A
memory_policy.retrieval.enabled = false → group B
```

Compare:
- **Repetition rate**: how often does the model ask for information already provided in a prior turn
- **Task completion efficiency**: number of conversation turns required to complete the same goal
- **LLM-as-judge scoring**: use an independent model to score responses from both groups on relevance and coherence

**Token efficiency**

```
memory_value_ratio = tokens_injected / total_prompt_tokens
```

Combined with output quality scoring, this identifies the optimal `token_budget` range for a given route.

### 20.3 Extraction Quality

**Precision and recall over replayed conversations**

For a batch of historical conversations, run extraction offline and evaluate:
- **Precision**: what fraction of extracted entries are genuinely valuable (human or LLM-evaluated)
- **Recall**: what fraction of important information in the conversation was successfully extracted
- **Type accuracy**: whether `facts`, `preferences`, `decisions`, and `corrections` are correctly classified

**Deduplication and versioning consistency**

- Rate at which the same key is written multiple times (deduplication failure rate)
- Whether confidence for the same information converges monotonically across sessions

### 20.4 System-Level Metrics

These can be driven directly from `MemoryRetrievalEvent` and `MemoryExtractionEvent`:

```
retrieval latency P50 / P95 / P99 (latency_ms)
extraction latency (async, not on the critical path)
average tokens injected per request (tokens_injected)
memory write QPS vs read QPS ratio
shared promotion trigger rate (used to calibrate shared_promotion_min_confidence)
```

### 20.5 Minimum Viable Evaluation For Phase 1

Phase 1 delivers retrieval and injection only. The recommended starting evaluation set is:

1. **Offline Recall@5 baseline**: hand-label 50–100 test cases covering the primary routes
2. **Online injection rate monitoring**: track `entries_injected / entries_retrieved` per route
3. **A/B LLM-as-judge comparison**: score memory-on vs memory-off groups using an independent model

These three metrics answer the three foundational questions: did we retrieve the right entries, did we inject them, and did injection help.

## 21. Tradeoffs And Recommendations

### 21.1 Scenario Decision Matrix

| Scenario | Recommended Approach |
|---|---|
| Multiple agents sharing project context | Gateway memory — shared namespace, zero agent coordination |
| Cross-session user preferences for a single agent | Gateway memory — route-private namespace, session identity required |
| Agent that must reason over and update its own memory | SDK/framework memory — agent-driven tool calls |
| Domain-specific extraction requiring custom schemas | SDK/framework memory — higher extraction control |
| Onboarding existing agents without code changes | Gateway memory — zero-intrusion injection |
| Fine-grained memory within a single session | SDK/framework or hybrid — async extraction has inherent latency |
| Multi-tenant SaaS platform with operator-managed policy | Gateway memory — tenant isolation and policy ownership are infrastructure concerns |

### 21.2 Recommended Composition Pattern

Gateway memory and SDK/framework memory are not mutually exclusive. The recommended pattern for systems that need both is:

```
gateway memory layer
  — shared project context across agents
  — cross-session preferences injected automatically
  — operator-managed policy, no application code changes required

application memory layer (via MCP tools exposed through the gateway)
  — agent-driven explicit memory writes ("remember this decision")
  — domain-specific extraction with custom schemas
  — synchronous memory updates within a session
```

The gateway handles the background memory infrastructure. The agent handles deliberate, agent-initiated memory operations through explicit MCP tool calls.

### 21.3 Key Constraints To Accept In Phase 1

The following limitations are by design and should be accepted rather than worked around:

- **Session identity must be explicit**: do not attempt to infer `X-Session-ID` from prompt content; degrade gracefully to route-private retrieval only when session identity is absent
- **Injection point is fixed at system prompt suffix**: do not add injection modes until phase 1 retrieval quality is validated at production scale
- **Async extraction only**: do not introduce synchronous extraction on the critical path; the latency cost is not justified until extraction quality is proven
- **Generic extraction schema in phase 2**: resist domain-specific extraction customization until the base extraction pipeline is stable and quality is measured

### 21.4 When To Revisit These Tradeoffs

The following conditions should trigger a reassessment of the gateway memory approach:

- Injection rate consistently below 0.5 across production routes after token budget tuning, suggesting retrieval quality is insufficient for the use case
- A/B testing shows no measurable quality improvement from injection, suggesting the injection point or format needs to change
- Multiple application teams independently building application-layer memory that duplicates gateway memory state, suggesting the gateway is not covering the right scope
- Extraction precision below 0.7 in domain-specific routes, suggesting generic extraction is insufficient and a custom extraction path is needed
