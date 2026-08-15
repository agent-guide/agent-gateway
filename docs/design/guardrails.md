# Community Guardrails Core

## 1. Status and purpose

This document defines the Community Guardrails Core for `agent-gateway`. It is
a design and implementation direction; the subsystem described here is not yet
implemented. Its integration points are aligned with the current unified
`kind=agent` runtime and LLM/MCP dispatcher; names in code blocks below are
proposed API shapes, not claims about existing exported Go types.

Community Guardrails provides a protocol-correct, auditable, extensible safety
checkpoint before a gateway-mediated LLM, MCP, or Agent operation reaches its
target. It supplies the enforcement kernel and useful baseline checks without
making basic security a paid feature.

The core answers four questions:

1. Which normalized content is about to leave a gateway-controlled seam?
2. Which policy and checks apply to that operation?
3. Must the operation be allowed, redacted, or blocked?
4. Can the decision be applied without losing or corrupting protocol data?

Advanced enterprise detectors, organization policy, compliance operations,
RAG, cost management, and durable human review are intentionally outside this
document. Their product boundary belongs in the separately maintained
`docs/design/enterprise-feature-roadmap.md`.

## 2. Scope

Community Guardrails includes:

- a runtime-neutral guardrail engine and check registry;
- normalized, typed inspection views for covered operations;
- validated, lossless request cloning and redaction;
- `allow`, `redact`, and `block` decisions;
- `monitor` and `enforce` policy modes;
- explicit `fail_open` and `fail_closed` behavior;
- bounded inspection, timeout, cancellation, and panic containment;
- route policy binding and target trust-zone validation;
- a deterministic baseline sensitive-data check;
- generic internal LLM-checker support with structured verdict validation;
- baseline topic classification as a reference model-backed check;
- operation coverage metadata;
- sanitized decision events, low-cardinality metrics, and trace correlation;
- Admin, bundle, and CLI surfaces needed to configure and inspect the core.

The Community implementation must be independently useful. An operator can
classify targets, bind a policy, detect common sensitive values, run a generic
checker through an internal LLM route, monitor decisions, and enforce safe
blocking or redaction without an Enterprise module.

## 3. Non-goals

Community Guardrails does not implement:

- enterprise identity, tenant directories, RBAC, ABAC, or SCIM;
- OPA/CEL policy-as-code, organization policy inheritance, approval, staged
  rollout, or policy conflict analysis;
- proprietary DLP models, industry entity packs, reversible tokenization,
  Vault/KMS/HSM integration, or a false-positive operations console;
- prompt-injection/jailbreak products or multi-detector risk scoring;
- response grounding, compliance reporting, WORM storage, or SIEM connectors;
- data-residency optimization or content-driven commercial routing;
- semantic cache, prompt slimming, budgets, chargeback, or billing;
- knowledge retrieval, reranking, answer synthesis, or citations;
- a durable human-review queue, assignment, notifications, SLA, or recovery;
- an ACP subprocess sandbox, host filesystem policy, or network firewall;
- model serving, vector storage, or a business workflow engine.

The core exposes stable seams that separate modules may implement. It does not
carry placeholder configuration for Enterprise features that it cannot execute.

## 4. Architectural boundary

Guardrails is a request-time cross-cutting layer, not a Provider, MCP service,
Agent runtime, or durable workflow. Enforcement is attached to the three
gateway-owned execution seams that already exist: `RoutedProvider`, MCP JSON-RPC
dispatch, and unified Agent ingress.

```text
authenticated, route-resolved operation
        |
        v
decode the complete typed request
        |
        v
resolve guardrail policy and target trust zone
        |
        v
build complete typed inspection view
        |
        v
GuardrailEngine.Evaluate
  -> checks
  -> combine verdicts
  -> validate rewrite
        |
        +---- block ----> protocol-shaped local response
        |
        +---- allow/redact ----> existing LLM/MCP/Agent execution seam
        |
        v
sanitized decision event + correlated interaction event
```

The intended runtime package is independent of Caddy:

```text
pkg/guardrail/
  engine.go       // engine interface and default evaluator
  types.go        // Input, Segment, Verdict, Decision, Action
  policy.go       // policy model, modes, limits, and failure behavior
  registry.go     // check factory registry
  coverage.go     // operation coverage metadata
  check/
    pii/          // deterministic baseline plus optional generic checker
    topic/        // generic internal-model topic classifier

pkg/gateway/
  guardrail_llm.go         // provider request projection and enforcement

pkg/dispatcher/
  guardrail_errors.go      // protocol-local rendering of typed block errors
  guardrail_mcp.go         // JSON-RPC method projections and enforcement
  guardrail_agent.go       // unified Agent turn projection and enforcement
```

Provider implementations, MCP transports, and Agent runtime backends must not
import `pkg/guardrail`. The gateway assembly, `RoutedProvider`, and dispatcher
compose it. Existing protocol adapters continue to own wire parsing and
rendering; dispatcher-side projectors operate on `PreparedLLMApiRequest`, MCP
`transport.Message`, and `agentruntime.TurnRequest` after those values pass their
normal validation.

## 5. Trust boundary

### 5.1 Target classification

`provider.ProviderConfig` and `mcp/service.MCPServiceConfig` gain an explicit
classification:

```text
trust_zone: internal | external | unclassified
```

An omitted value normalizes to `unclassified`, never `internal`.

The classification expresses policy intent:

- `internal`: the operator asserts that the endpoint is inside the permitted
  boundary;
- `external`: traffic crosses the protected boundary;
- `unclassified`: the gateway has no basis for treating the endpoint as safe.

`trust_zone=internal` is not proof of network isolation. DNS, routing, process
sandboxing, filesystem permissions, credentials, and egress firewall rules are
deployment responsibilities.

### 5.2 Checker dependencies

A model-backed check names a normal LLM route as `checker_route_id`. Every
provider reachable through that route's direct-provider or logical-model target
policy must be classified `internal` when the check can inspect protected
content. This validation includes every logical-model candidate that fallback
could select; one external or unclassified provider invalidates the enforcing
policy.

Checker execution resolves the LLM route from the in-memory route snapshot and
uses a normal `RoutedProvider`. A private, package-owned context marker bypasses
only the Guardrails evaluation for that internal checker call. This preserves
credential selection, candidate fallback, cancellation, and usage correlation
without exposing a client-controlled bypass or recursively checking the checker
call itself.

Policy validation rejects a self-reference in which the protected route is also
its own checker route.

### 5.3 External-target gate

The shared `routecore.AgentRouteConfig` envelope gains an optional
`guardrail_policy_id`, so expanded LLM, MCP, and Agent route models inherit one
binding mechanism. Gateway startup configuration may also enable a global
`require_guardrails_for_external` gate.

The initial global setting defaults to `false` for upgrade safety. Operators
classify targets, bind policies in `monitor`, inspect results, switch selected
routes to `enforce`, and then enable the global gate.

When the global gate is enabled:

- every enabled LLM route that can reach an `external` or `unclassified`
  provider and every MCP route targeting such a service requires a valid
  policy;
- logical-model validation covers every candidate fallback could select;
- a missing policy, missing checker, uncovered operation, or invalid enforcing
  dependency fails closed;
- retries cannot escape the decision because they reuse the validated original
  or rewritten request.

An AgentRoute targets an Agent definition, not an external network endpoint, so
the global egress gate does not infer its trust zone. Operators may bind an
ingress policy explicitly. Builtin model and MCP calls are independently
covered at their LLM and MCP execution seams. ACP subprocess filesystem access
and direct vendor traffic remain outside the gateway boundary.

## 6. Check SPI

Checks register through a runtime-neutral factory:

```go
type Check interface {
    Inspect(context.Context, *Input) (Verdict, error)
}

type CheckFactory func(CheckConfig, CheckDeps) (Check, error)

func RegisterCheckFactory(kind string, factory CheckFactory) error
```

`CheckDeps` exposes narrow interfaces such as `LLMRouteExecutor`; it does not
expose the complete gateway runtime. Built-in checks register through blank
imports in every binary that validates or executes policies.

Third-party and Enterprise checks use the same SPI. The core registry does not
know their license, implementation, model, or vendor.

### 6.1 Baseline sensitive-data check

The Community `pii_scan` check provides deterministic detection for a small,
documented set of common patterns. It supports:

- configurable regular expressions with bounded complexity;
- named entity categories;
- deterministic byte spans;
- `block` or irreversible mask replacement;
- optional invocation of an internal generic LLM checker;
- structured checker verdict validation.

A regex miss is not represented as proof that arbitrary content is safe. Policy
metadata distinguishes deterministic-only and model-assisted execution.

The baseline implementation does not ship proprietary entity models, industry
packs, customer dictionaries, or reversible tokenization.

### 6.2 Baseline topic check

The Community `topic_filter` is a generic reference check that calls an internal
LLM route and validates a bounded structured result:

```json
{
  "on_topic": true,
  "confidence": 0.92,
  "reason_code": "on_topic"
}
```

It operates only on the policy-declared user-content projection. It does not
silently inspect credentials, hidden metadata, or host files.

## 7. Typed inspection and rewriting

One `Input` carries exactly one operation-specific view:

```go
type Input struct {
    Protocol     string
    Operation    string
    RouteID      string
    AgentID      string
    VirtualKeyID string

    LLM     *LLMInput
    MCP     *MCPInput
    Agent   *AgentInput
}
```

Each view provides:

- `Clone`;
- `InspectableSegments`;
- `ApplyRedactions`;
- operation identity and capability metadata.

Segments carry a stable source field, role, ordinal, byte length, and rewrite
coordinates. Inspection never returns an implicitly truncated string.

`ApplyRedactions` returns a validated replacement of the same operation type.
It may not silently remove structured content, tool arguments, metadata,
working-directory settings, model options, tool definitions, or protocol fields.

A redaction span must satisfy all of the following:

- refer to an existing typed segment;
- use valid byte bounds and UTF-8 boundaries;
- match the exact cloned input generation;
- not overlap inconsistently with another replacement;
- produce a payload that the protocol adapter can validate and render.

If a violation cannot be rewritten completely and safely, the effective action
is `block`, never unmodified egress.

## 8. Policy model

A Community policy contains only executable core fields:

```jsonc
{
  "id": "external-egress-baseline",
  "version": 1,
  "mode": "monitor",
  "screening_timeout_ms": 3000,
  "input_limits": {
    "max_inspectable_bytes": 16384,
    "max_segment_bytes": 8192
  },
  "checks": [
    {
      "kind": "topic_filter",
      "checker_route_id": "internal-checker",
      "min_confidence": 0.75,
      "on_violation": "block",
      "fail_mode": "fail_open"
    },
    {
      "kind": "pii_scan",
      "checker_route_id": "internal-checker",
      "fast_filter": true,
      "on_violation": "redact",
      "fail_mode": "fail_closed"
    }
  ]
}
```

Check order is normalized and deterministic. Completion timing never changes
the combined result. Precedence is:

```text
block > redact > allow
```

Policy order breaks reason-code ties only.

`monitor` runs the same checks and records `proposed_action`, but it never
mutates, blocks, changes routing, or sends content to a dependency that is not
valid for the trust boundary.

## 9. Decision model

```go
type Action int

const (
    ActionAllow Action = iota
    ActionRedact
    ActionBlock
)

type Decision struct {
    Action         Action
    ProposedAction Action
    ReasonCode     string
    PublicMessage  string
    Rewritten      *Input
    Verdicts       []Verdict
}
```

Only `allow` and `redact` can reach the selected target. `block` produces a
local response in the caller's protocol.

`ReasonCode`, `PublicMessage`, and persisted verdicts never contain prompt
excerpts, matched secrets, raw classifier output, tool arguments, or file
content. Privileged diagnostics use bounded category identifiers and counts.

## 10. Bounded execution and failure handling

Every check has a timeout, input limit, and explicit failure mode. The engine:

- propagates caller cancellation;
- applies one cumulative screening deadline;
- recovers panics at the check boundary;
- rejects malformed structured checker output;
- reports oversized or incomplete inspection explicitly;
- never fabricates redaction spans after an infrastructure error.

Recommended baseline behavior:

| Condition | Effective behavior |
|---|---|
| confirmed violation with valid spans and `redact` | redact |
| confirmed violation without safe spans | block |
| violation configured as `block` | block |
| incomplete DLP view with `fail_closed` | block |
| checker timeout with `fail_closed` | block |
| checker timeout with `fail_open` | allow that check and audit the error |

An enforcing profile is workload-specific. Operators must run agentic and large
context traffic in `monitor`, measure checker throughput and tail latency, and
select a limit/deadline pair that can inspect the complete admitted view. Raising
only the byte limit is not valid tuning.

## 11. Operation coverage

Guardrails publishes exact capability metadata rather than claiming blanket
protocol coverage.

Coverage for an operation requires:

- a complete normalized input view;
- the declared inspection projection;
- lossless clone and rewrite support;
- a protocol-shaped local block response;
- cancellation and streaming tests;
- decision-event attribution.

An external route configured to require Guardrails rejects an unsupported
operation instead of bypassing the engine.

Initial coverage should start with the current provider-facing chat request used
by OpenAI Chat Completions, Anthropic Messages, and the CC profile. Later
Community phases add the current Responses and embeddings request types,
upstream MCP operations, and unified Agent turn input. Builtin model calls use
the same LLM request view because the eino bridge terminates in
`RoutedProvider`; they are not a fourth wire protocol.

## 12. Insertion points

### 12.1 LLM

The enforcement hook lives in `pkg/gateway.RoutedProvider`, immediately before
its first target/credential execution attempt. This is the common execution
boundary for HTTP LLM traffic and builtin eino model calls. It evaluates a
cloned `provider.ChatRequest`, `provider.ResponsesRequest`, or
`provider.EmbeddingRequest` and reuses the resulting original or rewritten
request for every fallback attempt.

A block returns a typed status error. The OpenAI, Anthropic, and CC handlers map
that error through their pre-stream HTTP error path; the protected target
provider is not resolved or called after the decision. Model listing is a
control/read operation with no prompt payload and is outside request-content
inspection.

### 12.2 MCP

MCP evaluation occurs after a complete `transport.Message` is decoded and
method parameters pass their existing typed validation, but before the service
manager makes an upstream call. `initialize`, upstream list/read/get/complete
operations, `tools/call`, and forwarded notifications each need explicit
coverage. Locally answered methods such as `ping` and `roots/list` do not cross
the MCP service boundary and must not be rejected merely because no inspection
view exists.

Route-level MCP tool allow/deny, aliases, and description rewriting remain in
the MCP tool policy subsystem. Guardrails inspects operation content; it does
not replace tool exposure policy.

### 12.3 Unified Agent ingress

`POST /<agent-route>/turn` input is evaluated after
`agentruntime.DecodeTurnRequest` and Agent/runtime resolution, but before
`agentruntime.NewTurnSequencer` starts a run or the selected backend executes it.
Permission continuations contain decisions rather than a new user prompt and
are not treated as fresh Guardrails input. The optional `/permission`,
`/sessions`, and transcript operations remain governed by their existing
runtime capability checks and authorization boundary.

For an ACP runtime this protects only the submitted instruction. Codex or
OpenCode subprocesses may later read local files or make vendor calls using
their own credentials. Those autonomous actions do not return through the
Gateway seam. Process isolation, filesystem policy, scoped credentials, and
network egress controls remain required.

### 12.4 Builtin model calls

Checking only the first Agent turn is insufficient because later model calls
can contain tool results, supervisor context, or subagent content. No separate
builtin-only wrapper is needed: `builtin.ChatModelResolver` already resolves an
LLM route through the eino provider bridge. Every `Generate` and `Stream` call,
including calls made through a model returned by `WithTools`, reaches the same
`RoutedProvider` hook described in §12.1. Tests must prove that this remains
true for nested topology nodes,
middleware-generated context, tool results, and resumed turns.

## 13. Streaming behavior

Request inspection completes before upstream response headers or stream data
start. A blocked LLM request returns the protocol handler's normal pre-stream
HTTP error response; a blocked MCP request returns a JSON-RPC error; and a
blocked Agent turn returns the runtime-neutral pre-stream error response. The
gateway does not start a synthetic SSE stream solely to report an admission
failure. Client disconnect cancels outstanding checks.

The Community core does not claim full streamed response inspection. It exposes
a future response-inspection seam, but implementations that buffer or mutate
streamed model output require a separate design with explicit latency, memory,
partial-delivery, and termination semantics.

## 14. Configuration and management

Guardrail policies are a first-class config-store and Gateway Bundle object.
The manager validates local policy shape and the complete reference graph before
publishing a new immutable runtime snapshot.

Required surfaces:

- policy apply/export/validate in Gateway Bundle;
- Admin CRUD under `/admin/guardrails/policies`;
- capabilities and sanitized recent-decision inspection under
  `/admin/guardrails/runtime/...`;
- `agwctl guardrails ...` read/validate commands, while declarative mutation
  continues to use `agwctl apply`;
- create/update/delete guards for route-policy and policy-checker references;
- bundle validation for route-policy, checker-route, target trust-zone, and
  logical-model candidate references.

Complex policies and bindings are not added to the initial Caddyfile grammar.
They follow the dynamic bundle workflow used by other cross-referenced objects.

The request hot path resolves policies from in-memory snapshots. It does not
query ConfigStore per request.

## 15. Observability

Every evaluated operation emits a sanitized decision event correlated to its
interaction span.

The event includes:

- event, trace, span, parent, route, Agent, run, and VirtualKey attribution;
- policy ID, version, and mode;
- protocol and operation;
- effective and proposed action;
- whether gateway-mediated egress occurred;
- bounded reason code;
- per-check category, outcome, latency, and error class.

It excludes raw prompts, matched values, rewritten payloads, tool arguments,
classifier output, and local file content.

Prometheus exposes only bounded labels such as protocol, operation, mode,
action, check kind, and outcome. Route, Agent, user, tenant, reason text, and
entity values are not default labels.

Decision events use the existing asynchronous event pipeline and retention
mechanism. They are operational events, not a financial ledger or immutable
compliance archive.

## 16. Community implementation order

### C0: enforceable chat boundary

- [ ] runtime package, engine, types, policy, registry, and coverage metadata;
- [ ] trust-zone fields and complete route-candidate validation;
- [ ] policy manager, ConfigStore schema, bundle object, and route binding;
- [ ] typed OpenAI/Anthropic/CC chat views and lossless rewrites;
- [ ] deterministic `pii_scan` and generic internal-model checker executor;
- [ ] baseline `topic_filter`;
- [ ] monitor/enforce, bounded input, deadlines, cancellation, and failure matrix;
- [ ] typed block errors and protocol-shaped pre-stream HTTP responses;
- [ ] sanitized decision events, metrics, and trace correlation;
- [ ] real client compatibility tests for blocked streaming responses.

### C1: operation-complete gateway coverage

- [ ] OpenAI Responses and embeddings views;
- [ ] upstream MCP method views and insertion points;
- [ ] unified Agent turn view and runtime-neutral insertion point;
- [ ] prove `RoutedProvider` coverage for builtin `Generate`/`Stream`, including
  supervisor, deep, plan-execute, tool-result, and resumed turns;
- [ ] operation capability endpoint;
- [ ] Admin runtime query and `agwctl` read surface.

### C2: hardening

- [ ] policy update concurrency and immutable snapshot tests;
- [ ] checker-route fallback, recursion, and trust-boundary tests;
- [ ] malformed verdict, invalid UTF-8 span, overlap, and rewrite tests;
- [ ] latency and throughput benchmarks for documented starter profiles;
- [ ] fuzzing of normalized views and protocol block renderers;
- [ ] Caddy and standalone assembly parity;
- [ ] upgrade guidance from the opt-in gate to enforced external-route policy.

## 17. Enterprise extension boundary

Enterprise modules may register checks and policy evaluators through the same
Community interfaces, but their product behavior is specified outside this
document.

The enterprise roadmap owns:

- advanced DLP, injection detection, response inspection, custom entity packs,
  reversible tokenization, KMS, data residency, and AI security operations;
- tenant-aware Policy as Code, approval, shadow comparison, staged rollout, and
  rollback;
- RAG retrieval and internal-answer short circuit;
- semantic cache, prompt slimming, budget enforcement, cost allocation, and
  billing;
- compliance reports, WORM/SIEM sinks, Eval, replay, and false-positive
  workflows;
- durable human review owned by an external workbench/workflow engine.

Community types must remain general enough for these modules without importing
Enterprise packages into lower protocol packages.

## 18. Related documents

- [Architecture Overview](../architecture/architecture-overview.md)
- [Agent Control Plane](agents-control-plane.md)
- [Builtin Agent Runtime](builtin-agent-runtime.md)
- [MCP Tool Policy](mcp-tool-policy.md)
- [Observability](observability.md)
- [Gateway Bundle YAML](gateway-bundle-yaml.md)
- `docs/design/enterprise-feature-roadmap.md` — maintained on the Enterprise
  product branch and available in the combined documentation tree
