# Community Domain Extension Catalog

Capability status: **Proposed** for future domain integration. The v0.7
foundation and limiter proof are implemented as candidate contracts. Other
integration proposals in this catalog are not implemented or protected SPIs
merely because the foundation exists. Released protection still requires a
published tag and independent consumer acceptance.

## 1. Purpose and scope

This document inventories existing Community modules and potential Gateway
domains that could reuse the extension foundation. It covers models and
protocols, tools and Agents, identity and security, quotas and costs,
observability, storage, retrieval and synchronous request composition. It does
not commit to implementing every candidate or assign delivery versions.

Domain integration means reusing compile-time identity, manifests, configuration
validation, dependencies, prepared-resource lifecycles, Admin subroutes and
public event boundaries. Execution interfaces remain domain-owned. Packages do
not need to move into `pkg/extension`, and Manager must not become a general
service locator, protocol dispatcher or arbitrary request interceptor.

Checked-in code defines current behavior. This catalog describes integration
candidates and constraints. Additional domains must establish their ownership,
configuration boundaries and independent consumer evidence; empty registered
interfaces do not establish an implemented capability.

## 2. Shared integration principles

### 2.1 Three layers of ownership

| Layer | Responsibility | Examples |
| --- | --- | --- |
| Common foundation | Module identity, dependencies, configuration and resource lifecycle, Admin composition, trusted attribution and bounded event delivery | `pkg/extension`, `pkg/distribution`, `pkg/attribution`, `pkg/event` |
| Domain interfaces and core | Execution semantics, candidate eligibility, input/output contracts, security checks, cancellation, errors and accounting; explicit integration of implementations | Provider, llmapi, limiter, Agent backend |
| Module implementation | Concrete providers, backends, policies or connectors, with their schemas and resources | Community reference implementations and independently maintained distribution modules |

Existing Go registries do not necessarily need replacement by the common
Registry. Integration must define module-ID-to-domain-type bindings, validate
duplicates, dependencies and capability declarations during assembly, and use
prepared domain snapshots during requests. `Factory.Prepare` must not register
new process-global factories as a side effect of configuration mutation. Code
registration and configuration activation are separate stages. Deleting
configuration retires resources; it does not unload linked binary code.

Both server modes must consume the same domain assembly contract. The CLI links
only the schema/factory subset required for local validation; it does not open
connections or create server resources. Candidate interfaces require an audit
of their transitive types so independent consumers do not need Community
`internal/` imports or copies of host implementations.

### 2.2 Configuration and bootstrap ownership

Internal `extensions` use ConfigStore as their only serving configuration source.
Admin API or Bundle apply performs mutations. Bundle validation does not activate
resources, and export preserves secret references. Caddyfile owns Caddy host
wiring and bootstrap connections; it cannot supply internal extension envelopes.
Standalone static Bundles cannot supply those envelopes either.

Module configuration is separate from business resources. Provider instances
remain in providers, Agent definitions in agents, MCP services in mcp_services,
and routes, VirtualKeys and credentials in their existing resource families.
Extension configuration must not duplicate those objects, create parallel CRUD
or bypass their validation.

Bootstrap dependencies such as the ConfigStore backend must be created before
Manager loads envelopes. They may use public domain factories, descriptors and
contract tests, but an envelope stored in a database cannot determine how to
open that same database. Connection configuration is an explicit bootstrap
input, not a reason to restore static extensions. This catalog does not change
existing static configuration rules for other server resources; extending the
configuration principle to those resources requires a separate decision.

### 2.3 Common constraints

- Link code at compile time; no downloaded code, `plugin.so` or per-request
  module discovery.
- Validate configured modules explicitly. Missing security modules must not
  silently bypass enforcement.
- Do not read ConfigStore, manifests, module directories or license services
  on request hot paths.
- Prepare configuration changes, persist them, then publish a complete
  generation. Failures preserve the old generation; requests retain leases.
- Domain owners define state preservation and isolation across generations.
  Manager currently re-prepares every configured module on mutation.
- Define timeouts, cancellation, concurrency, fail-open/closed behavior and
  uncertain outcomes for operations with side effects.
- Reuse the existing event and usage authority. New DTOs require separate
  review. Do not propagate credentials, prompts or arbitrary callback errors,
  or put identities into high-cardinality metric labels.
- Community owns foundational interfaces and general reference implementations.
  Independent modules may provide commercial policies and connectors. Community
  capabilities do not depend on Enterprise licenses, and exported symbols are
  not automatically protected.

## 3. Existing Community domain candidates

Existing capability means a runtime or registration mechanism is present, not
that it has already been integrated with the common extension foundation.
First priority indicates a recommendation for evaluation, not an execution plan.

### 3.1 Models, protocols and routing

| Domain | Existing capability and entry points | Candidate module responsibilities | Core ownership and integration gaps |
| --- | --- | --- | --- |
| LLM provider (first priority) | [Provider registry](../../pkg/llm/provider/registry.go) and provider implementations; compile-time factory and type-capability registration | Custom upstream adapters, module-level connection resources, capability and schema declarations | Provider instances remain provider resources. Core owns credentials, candidate eligibility and execution governance. Audit factory signatures, duplicate registration, dependencies, cache invalidation and shutdown; the existing registry is not automatically protected |
| LLM ingress / llmapi (first priority) | [LLMApiHandler](../../pkg/dispatcher/common.go); Caddy loads protocol modules, while standalone explicitly constructs three built-in handlers | Protocol handler factories, available types and schemas, host-independent assembly | Core preserves shared admission, execution lifecycle and usage. The current [type registry](../../pkg/dispatcher/registry.go) records names without construction factories. Unify both host entry points and audit public types |
| Model discovery and catalog | `pkg/gateway/modelcatalog`, provider `ListModels`, managed models | Upstream-specific discovery, model metadata mapping and external catalog synchronization adapters | Core owns managed-model CRUD, availability and protocol eligibility. Discovery results must not directly grant routing eligibility; no per-request remote catalog lookup |
| Candidate selection and routing policy | [Route target policy](route-target-policy.md), logical-model candidates and auto/priority/weighted selection | Region, cost or preference selection within the core-approved candidate set | Core owns route matching, VirtualKey constraints, model rewriting and protocol filtering. Policies cannot return arbitrary providers or replace the dispatcher |
| Model high availability | Bounded `RoutedProvider` fallback and credential feedback; complete health/circuit behavior is not implemented | Local or shared health, circuit state and candidate decisions | Core must own a unified deadline and attempt budget, cancellation, stream commitment and usage. Current stream fallback covers opening only. Complete execution feedback before defining a narrow SPI; never splice another model after output commitment |
| Result processing and protocol fidelity | Public provider DTOs, ProtocolState and shared Anthropic Messages core | Reviewed domain transformations or adapters for specialized model capabilities | Native relay, tool history, signed thinking, usage and SSE state follow protocol contracts. Arbitrary map hooks must not bypass fidelity validation or introduce a second protocol core |

Every currently linked provider is within the first row's evaluation scope.
OpenAI, Anthropic, Claude Code, Codex, Gemini, Ollama, OpenRouter, DeepSeek, Qwen,
Zhipu and Grok do not need separate extension mechanisms. Shared wire helpers
and SDK bridges normally remain ordinary libraries.

### 3.2 MCP, Agents and remote execution

| Domain | Existing capability and entry points | Candidate module responsibilities | Core ownership and integration gaps |
| --- | --- | --- | --- |
| MCP transport / service | Existing connection and invocation implementations in `pkg/mcp/service` and `pkg/mcp/transport` | New transports, connection factories, vendor authentication adapters and pooled resources | Existing MCP service and route families own configuration. Core owns discovery, execution, cancellation and usage. Define transport factory contracts; `pkg/mcp/runtime.Registry` tracks requests and is not an extension factory registry |
| MCP tool policy | Filtering, aliases and description rewriting in the [tool policy design](mcp-tool-policy.md) | Tool policies, input/output checks and declared transformations | Core validates exposure, name conflicts and execution targets. Discovery, listing and invocation must use the same policy generation; a hidden tool must not remain executable |
| Agent runtime backend (evaluate after first-priority domains) | [Backend](../../pkg/agent/runtime/types.go) and [Registry](../../pkg/agent/runtime/registry.go), with ACP/builtin/HTTP adapters | New runtime backends and capability implementations | Core retains AgentRoute, run sequencing, permission brokerage and the control plane. Assess Agent schema/validation integration too; registering a Backend does not by itself make definition APIs support a new runtime |
| Builtin custom Agent factory | [builtin.RegisterFactory](../../pkg/agent/builtin/factory.go) and eino ADK host | Custom Agent construction, required resources and factory declarations | Agent definitions and LLM/MCP resource references retain their ownership. Core owns sessions, permissions, cancellation and usage. Audit factory dependency types and independent consumer assembly; do not copy the host |
| ACP implementation adapter | `pkg/acp/agentspi`, currently Codex/OpenCode Agent adapters | Linked ACP launch and compatibility adapters | Retain shared wire parsing, process pools, fingerprint fences, permission and transcript semantics. ACP execution configuration remains in `Agent.runtime.acp`; do not restore a separate ACP service store |
| HTTP Agent / A2A southbound | `pkg/a2a`, `pkg/gateway/http_backend.go` and HTTP runtime manager | Future HTTP dialect clients and bounded authentication/transport adapters | Core retains Agent Card validation, same-origin and credential scope rules, Path A/B, claims/bindings, cancellation and readiness. A new dialect must not fork A2A or add request-time configuration discovery |
| Agent/tool permission decisions | Existing VirtualKey constraints, Agent policy and permission broker | Synchronous tool permissions, resource access policies or external authorization adapters | Core owns one-shot decisions, expiry, continuations and execution outcomes. Long-lived human approvals and business tasks across restarts belong upstream; do not introduce another permission broker |

### 3.3 Credentials, limiting, observability and storage

| Domain | Existing capability and entry points | Candidate module responsibilities | Core ownership and integration gaps |
| --- | --- | --- | --- |
| Request limiter backend | `pkg/gateway/virtualkey/limiter`; v0.7 domain proof and public contract suite | Other atomic consume backends and shared-state connectors | Core retains VirtualKey policy, admission timing, generation fences, 429/503, timeouts and explicit fail-open semantics. The foundation is reusable now; no production Redis backend is provided |
| Credential selection and refresh policy | `pkg/credential`, managed credential state, scheduler and external refresh command | Generic selection, rotation or external refresh transport adapters when real demand exists | Credential scope, expiry, failure feedback and persistence remain domain-owned. Provider-specific OAuth, interactive login and token exchange remain in `agw-auth`, not Gateway |
| Public operational event sink | `pkg/event`, integrated with the existing pipeline; two event families | Sink resource configuration, connections and delivery implementations | Public transport owns DTO validation, queue isolation, redaction, drops, flush and close. Existing sinks do not constitute complete audit or billing records |
| Usage / trace / metrics exporter | `internal/observability`, typed usage, OTLP, Prometheus and einotap | Future public usage projections, exporters or external monitoring connectors | Core retains one accounting authority. Do not export internal types. Complete public DTOs, delivery and lifecycle need separate design; existing event envelopes cannot directly carry usage rows |
| Usage/event storage and query backend | Typed SQLite usage storage and Admin queries | Future event storage/query adapters and retention implementations | Define separately from ConfigStore. Stable fields, pagination, retention, drops and query permissions belong to the domain contract. Do not put usage into extension configuration tables or claim multiple backends already exist |
| ConfigStore backend (bootstrap domain) | [Creator factory](../../pkg/configstore/factory.go), schema-bound stores, currently SQLite | Persistence backends, migration/transaction adapters and bootstrap connection assembly | Open before Manager; extensions cannot bootstrap their own store. Audit schema, CRUD, concurrency and failure contracts and support both hosts. Backend extensibility does not establish multi-replica consistency |

## 4. Potential domain inventory

These candidates introduce domain capabilities rather than rename v0.7
interfaces. All are Proposed. Real implementations and consumers are required
before defining public SPIs. Enterprise examples indicate possible module
implementations, not shipped capabilities or mandatory commercial dependencies.

### 4.1 Identity, security and governance

| Candidate domain | Possible module contribution | Required core contract |
| --- | --- | --- |
| Identity / authentication | OIDC, JWT validation, service identity or IdP adapters | Trusted principals, validation failures, expiry, caching and ingress integration. Admin mount authentication and data-plane identity are distinct; raw tenant headers are untrusted |
| Tenant resolution / isolation | Identity-to-tenant resolution and organization directory adapters | Tenant ownership across storage, query, cache and resource boundaries. Attribution transports identity; core must enforce isolation |
| Authorization / policy | RBAC/ABAC or OPA/CEL-style decision implementations | Subject/resource/action inputs, rule ordering, deny, timeouts, cache invalidation, versions and enforcement points. Never bypass VirtualKey or resource constraints |
| Guardrails / DLP / AI Firewall | Content checks, redaction, rule packs or remote detectors | [Guardrails contract](guardrails.md), inspection timing, streaming/content lifecycle, rewriting and fail-closed behavior. Inspected content must not enter generic event DTOs |
| Outbound access and network policy | Destination allowlists, proxy selection and enterprise network connectors | DNS/IP, redirects, credential forwarding, destination ownership and connection boundaries. Adapters must not obtain unrestricted HTTP clients or weaken existing SSRF defenses |
| Secret resolution | Vault/KMS connectors, caching and rotation resources | Reference syntax, resolution lifecycle, non-exportable values, failure and rotation semantics. Current references are env-only; bootstrap secrets cannot depend on an uncreated store/resource |
| Compliance audit | External audit delivery and WORM/SIEM connectors | Separately reviewed decision/audit DTOs, durability, acknowledgment, ordering, idempotency, retention, permitted content and query permissions. Best-effort operational events do not establish reliable audit |

### 4.2 Quotas, cost and shared state

| Candidate domain | Possible module contribution | Required core contract |
| --- | --- | --- |
| Concurrency and connection quotas | Shared semaphores and tenant/provider concurrency backends | Acquire/release, leases, disconnects, expiry and state fences. Request-rate token buckets do not replace concurrency quotas |
| Token / cost budget | Reservation, settlement and shared-budget backends | Reserve/settle/refund, actual usage, cancellation, uncertain execution, duplicate costs, precision and idempotency. Core owns execution boundaries and settlement authority |
| Pricing / cost allocation | Price catalogs, currency/unit conversion and allocation adapters | Model price versions, caching, unknown prices, usage mapping and rounding. Customer billing, invoices, payments and financial systems belong to external products |
| Multi-replica health and policy state | Shared health/circuits, quota state and region/tenant SLA policies | Consistency, freshness, disconnection behavior and fences. Coordinate with core attempt budgets and candidate eligibility; no hot-path module discovery |
| Cluster configuration distribution | External configuration synchronization, release versions and notification adapters | ConfigStore commit entry points, validation, consistency model, versions, drift and recovery. Do not create another authority that bypasses Admin/domain validation |

### 4.3 Data augmentation and synchronous composition

| Candidate domain | Possible module contribution | Required core contract |
| --- | --- | --- |
| Memory backend / policy | Session/long-term memory storage and extraction policies | [Memory design](memory.md), tenant/Agent/session scope, capacity, retention, deletion, permitted content and execution lifecycle. `/admin/memory/...` currently returns 501 |
| Retriever / knowledge retrieval | Vector, keyword or enterprise knowledge connectors | Tenant/ACL filtering, result provenance, bounds, timeouts, permitted content and empty-result semantics. Ingestion, OCR and knowledge operations remain external |
| Embedding / reranking adapter | Vector generation, reranking implementations or service connectors | Evaluate reuse of provider capabilities first. Define dimensions, batching, precision, errors and accounting; avoid a parallel model execution system |
| Response / semantic cache | Cache backends, keys and semantic lookup implementations | Identity/tenant/model/protocol isolation, versions, invalidation, streaming, hit accounting and sensitive-content rules. Never cache native state across sessions or bypass governance |
| Transform / prompt policy | Declared input/output transformations and template resolution | DTOs, allowed field changes, ordering, validation and attribution. Cross-protocol transformations retain provider/llmapi contracts; no arbitrary handler middleware |
| Synchronous Request Pipeline step | Domain implementations of `llm`/`mcp`/`transform` steps | [Pipeline design](request-pipeline.md), request-bound deadlines, cancellation, budgets, errors and usage. No `agent` step or durable workflow state |
| Online routing feedback / quality signals | Bounded execution-quality signals and evaluation-service adapters | Sampling, permitted content, feedback DTOs, latency budgets and availability. Offline evaluation, experiment management, training and long-running batch processing remain external |

## 5. Treatment of the seven current Caddy modules

The current `agw` [main](../../cmd/agw/main.go) explicitly links its modules.
The following recommendations define ownership boundaries; unified provider and
llmapi domain assembly is not yet implemented.

| Caddy module ID | Recommended treatment |
| --- | --- |
| `agent_gateway` | Retain the Caddy app for host bootstrap. Assemble domain implementations through shared assembly/runtime contracts rather than converting the app into a generic extension |
| `http.handlers.agent_route_dispatcher` | Retain the Caddy HTTP adapter and shared dispatcher. Domain extensions do not replace core governance or dispatch |
| `http.handlers.agent_gateway_admin` | Retain the Admin mount adapter and public Admin Handler. Modules contribute subroutes only within their owned namespaces |
| `agent_gateway.config_store_backends.sqlite` | Retain the Caddy bootstrap adapter. Use the public domain factory underneath and align with standalone; Manager does not create this backend dynamically |
| `agent_route_dispatcher.llm_apis.openai` | Evaluate a Caddy adapter over a shared domain handler factory; standalone uses the same implementation |
| `agent_route_dispatcher.llm_apis.anthropic` | Apply the same approach while retaining the shared Messages core and one protocol lifecycle |
| `agent_route_dispatcher.llm_apis.cc` | Apply the same approach while preserving cc ingress-profile differences and avoiding a parallel Messages core |

Standard Caddy TLS, listeners, authentication and reverse_proxy remain host
capabilities. Project providers currently use Go factory registration, not Caddy
modules. HTTP Agent/A2A is an existing domain runtime, not an eighth project-owned
Caddy module.

## 6. Integration priorities and completion criteria

First validate provider and llmapi integration for independent distributions,
then assess actual gaps in Agent backends/builtin factories, MCP, observability
exports and ConfigStore. Existing limiter and event sink contracts do not need
another registration mechanism just to make the catalog uniform. Introduce
Identity, Policy, Guardrails, availability, Secret and Memory interfaces through
real feature slices. Enterprise module development need not wait for a complete
Community rewrite. These are priority recommendations; versions, tasks and
execution order belong in a separately established active plan.

A completed domain integration requires evidence of:

- A domain owner, module/type bindings, instance configuration ownership and
  exact public symbols.
- A Community reference implementation and an independent Go module consumer.
  The consumer imports neither internal packages nor package main and copies no
  protocol, dispatcher, Admin or command implementations.
- Shared execution contracts for Caddy and standalone, with an accurate CLI
  validation subset.
- Coverage of duplicate/unknown modules, missing dependencies, configuration
  and persistence failures, startup recovery, update/delete/retirement,
  concurrency, timeouts, cancellation and required state preservation.
- Domain execution correctness, such as protocol stream state, Agent
  continuations or MCP tool consistency, beyond adding a manifest entry.
- Preserved ingress authentication/admission, credential scopes, event
  redaction and a single usage authority; explicit degradation and failure
  semantics for operations with side effects.
- A designated published tag and passing external consumer gates before
  declaring released protection. Candidate proof does not replace publication.

## 7. Responsibilities that should remain outside extension management

| Responsibility | Ownership and rationale |
| --- | --- |
| Gateway protocol dispatch, core route matching and shared execution governance | Retain domain cores with bounded policy/adapter interfaces; modules cannot replace the entire request path |
| Generic ConfigStore schemas, CRUD, Agent definitions and VirtualKey resource models | Retain domain ownership. Extensions manage module configuration, not all business resources |
| Protocol parsing/encoding helpers, SDK bridges, ordinary models and value types | Keep ordinary Go packages when independent configuration/lifecycle has no real benefit; avoid unnecessary registration and lease overhead |
| Caddy TLS, listeners, HTTP middleware and host operational commands | Retain Caddy modules and reusable command entries; standalone owns its host layer |
| Durable Project/Team workflows, Agent handoffs, schedules, human tasks and business-workflow recovery across restarts | Upper-layer workbench and external engine ownership. Gateway exposes normal Agent/LLM/MCP invocation APIs |
| Document ingestion, knowledge operations, training, GPU scheduling and payment systems | External platforms; integrate necessary calls through narrow connectors rather than copying those products into Gateway |
| Commercial license/entitlement, Enterprise console, customer delivery and packaging | Independent distribution ownership. Community adds no commercial SDK, private module ID or per-request license lookup |

Related contracts: [Enterprise Extension Contract](enterprise-extension-contract.md),
[custom distribution guide](../guides/custom-distribution.md), and
[current architecture](../architecture/architecture-overview.md).
