# Enterprise Extension Contract

Capability status: **Implemented** for the repository ownership and protection
policy. Reusable entries and their external-consumer suite are **Implemented**
along with linked registries, manifests, configuration generations and public
attribution/events. Contributed Admin routes and the limiter SPI are **Implemented**
as candidate contracts.
Protection requires a released tag containing the designated APIs and tests.

Individual extension surfaces are protected only when explicitly designated
by released code and contract tests.

## 1. Purpose and status

This document defines the Community-side rules required for a separately
maintained Enterprise distribution to extend `agent-gateway` safely. It is not
an Enterprise product roadmap, module catalog, licensing design, or release
plan. Those concerns are owned by the private
`github.com/agent-guide/agent-gateway-enterprise` repository.

The interfaces described below are extension requirements, not claims that all
of them already exist. Code and the nearest `AGENTS.md` remain the source of
truth for implementation status. A surface becomes protected by this contract
only when its package documentation explicitly designates it as an Enterprise
SPI or reusable distribution entry point and a released Community tag contains
the corresponding contract tests.

The active v0.7 plan applies this contract to a common extension foundation:
reusable distribution entry points, a linked capability manifest, bounded
registered configuration, Admin route composition, public attribution/event
contracts, and the first domain-owned proof SPI (`LimiterBackend`). Guardrails
and other domain SPIs remain separate vertical slices and are not prerequisites
for that foundation.

## 2. Repository and dependency direction

The integration model is one-way Open Core composition:

```text
github.com/agent-guide/agent-gateway-enterprise
                  |
                  | Go module dependency
                  v
github.com/agent-guide/agent-gateway
```

The following rules are mandatory:

- Community packages never import Enterprise packages.
- Enterprise implementations import only public Community packages and never
  Go `internal/` packages.
- Enterprise is assembled through compile-time registration and explicit
  imports, not Go `plugin.so`, runtime source discovery, or downloaded native
  code.
- Community binaries remain independently useful and never contain commercial
  implementation code, license SDKs, or configuration they cannot execute.
- Enterprise licensing or entitlement must never restrict Community security
  fixes, baseline authentication, baseline logging, or any other capability
  shipped as part of the Community distribution.
- Cross-repository work adds a general seam to Community first and implements
  the commercial behavior in Enterprise after the Community change is released.
- The Enterprise repository consumes a released Community tag. Committed
  `replace` directives, floating branches, and a checked-in `go.work` are not
  release inputs.

### 2.1 Foundation versus domain capabilities

The shared foundation solves composition, not product policy. It has four
layers:

1. reusable entry packages and a compile-time linked capability manifest;
2. bounded module identity/configuration and Admin composition contracts;
3. trusted attribution and deliberately public event DTO boundaries;
4. reusable external-consumer contract suites and compatibility evidence.

Domain behavior remains in narrow SPIs introduced with real consumers. A
limiter backend, Guardrail check, identity resolver, policy evaluator, secret
resolver, retriever, and ConfigStore creator do not implement one generic
interface and do not gain access to the complete gateway. Shipping one of them
does not make the others Enterprise-ready.

The common foundation must be useful to Community custom distributions. It may
not contain private module IDs, commercial defaults, entitlement decisions, or
configuration that Community cannot validate and execute.

## 3. Protected public surfaces

Community does not preserve backward compatibility by default. The only
exception relevant to the Enterprise repository is a public package explicitly
designated as one of the following:

1. an Enterprise extension SPI;
2. a reusable distribution command entry package whose name has been fixed by
   an accepted design document under `docs/design/`;
3. a public contract-test package for either surface.

Designation must be explicit in the package documentation and the applicable
`AGENTS.md`; an exported symbol is not protected merely because Enterprise uses
it. A newly designated surface must define:

- input, output, error, timeout, cancellation, and concurrency semantics;
- initialization, health, and shutdown lifecycle;
- fail-open or fail-closed behavior;
- hot-path and ConfigStore access constraints;
- tenant, subject, secret, and sensitive-data boundaries;
- stable type/module identifiers and configuration validation behavior;
- observability and audit responsibility;
- a reusable Community contract suite.

Once released as Enterprise-ready, a breaking change to a protected surface
requires an explicit version boundary or versioned replacement package,
migration notes, and passing Community plus Enterprise compatibility builds.
All other Community APIs retain the repository's default incompatible-change
policy.

## 4. SPI and registry rules

Extension seams must be narrow, domain-owned interfaces. Do not introduce a
single `Plugin` interface that exposes the complete gateway or use
`AgentGateway` as a general service locator.

Candidate seams are introduced only when a concrete vertical slice needs them.
Examples include a distributed limiter backend, guardrail check factory,
ConfigStore creator, agent runtime, identity or policy evaluator, event sink,
secret resolver, and Admin API route contributor. This list is not a commitment
to implement all seams or a statement of Enterprise product scope.

`LimiterBackend` is the v0.7 proof slice because the current in-process
VirtualKey limiter provides an independently useful reference implementation
and its request-bound failure semantics exercise the distribution, manifest,
configuration, Admin, attribution, and event boundaries. This choice does not
make limiting the universal extension abstraction and does not make a
production distributed backend part of Community.

Registries exposed to external repositories must follow consistent failure
semantics:

- registration returns an `error`;
- empty names, nil factories, and duplicate stable IDs are errors;
- registration never silently ignores or overwrites an implementation;
- a blank-import module may panic from `init()` when registration fails;
- unknown configured types and missing declared dependencies fail validation or
  startup with a diagnostic containing the stable ID;
- registration occurs during process assembly, while request handling uses an
  immutable runtime snapshot and performs no module-directory or license lookup.

Existing registries do not automatically satisfy this contract. Their current
behavior must be assessed and, where needed, adapted before they are designated
as protected Enterprise SPI.

## 5. Distribution assembly

Enterprise binaries cannot import Community `package main` packages. Community
must expose reusable command construction and execution packages before the
corresponding Enterprise binary is implemented. The intended separation is:

- Caddy and standalone entry packages own shared server bootstrap behavior;
- the management CLI entry package owns client commands and local validation;
- final `cmd/.../main.go` files own explicit compile-time module imports;
- `cmd/agw/main.go` remains the definitive Community Caddy module list;
- Caddy and standalone distributions expose equivalent runtime capabilities;
- CLI validation links only the schema/factory subset it needs, and the server
  performs final validation when applying configuration.

Community and Enterprise assembly should be checked using code-generated or
code-owned manifests. Documentation must not copy the complete blank-import
list or become a second source of truth.

The manifest reports what is linked, not what is licensed for a customer or
healthy at a particular instant. Serialized entries contain only bounded stable
module identity, version, schema version, and capability metadata. Runtime
factory pointers, secrets, tenant data, and entitlement state never appear in
the manifest.

A package does not qualify as a reusable distribution entry point merely
because its import path is public. Before designation, its exported
configuration, constructors, callbacks, and return types must be usable from a
separate Go module without importing Community `internal/` packages. The
current `caddy/gateway`, `standalone/server`, and `pkg/gateway` APIs expose
`internal/observability/usage` types in fields or method signatures; extraction
of protected entry packages must first promote or wrap every such type that an
external distribution needs to construct, implement, or configure.

## 6. Admin and event extension boundaries

Enterprise Admin routes must compose under the same outer mount protection and
HTTP behavior as Community routes. A public route-contributor seam must define
method/path ownership, reserved prefixes, conflict failure, dependency
injection, CORS, panic recovery, request logging, audit hooks, and JSON error
shape. Authentication remains a mount-layer responsibility such as Caddy
`basic_auth`, mTLS, a trusted reverse proxy, or standalone authentication
middleware; the seam must not weaken that boundary.

External event sinks must not import or freeze
`internal/observability/usage`. If an audit or metering SPI is needed,
Community first defines a deliberately stable public DTO and an adapter between
that DTO and the internal event model. Public event evolution and redaction
rules are reviewed independently from the internal observability model.

The v0.7 shared attribution contract defines optional fields for authenticated
subject, tenant, route, Agent, run, VirtualKey, trace, span, and parent
correlation, plus bounded namespaced attributes with an explicit allowlist and
size limits. Identity fields are populated only when established by an existing
trusted boundary or a future designated resolver, and correlation fields remain
absent when the corresponding runtime does not provide them. A caller-provided
tenant header is not trusted by itself. The foundation transports attribution;
it does not implement enterprise authentication, tenant storage, or
authorization.

The v0.7 public event envelope covers only event families needed to prove the
foundation. Guardrail decisions, financial metering, compliance audit, and
policy decisions receive versioned public DTOs with their own vertical slices;
the foundation does not predeclare empty product interfaces for them.

## 7. Runtime invariants

Enterprise extensions must preserve the Community runtime invariants:

- no ConfigStore or license-server reads in per-request hot paths; managers and
  entitlement state are refreshed into immutable snapshots;
- no Enterprise conditionals or proprietary SDK calls in protocol handlers;
- no silent bypass when configured security, identity, policy, or backend
  modules are missing;
- no second copy or fork of dispatcher, gateway, or protocol core;
- no durable Project/Team workflow, scheduler, human-task center, or Temporal
  dependency in lower gateway packages;
- protocol correctness, cancellation, streaming finalization, and usage
  accounting remain covered across Community and extended execution paths.

## 8. Cross-repository workflow

For a change that needs a new or modified Community seam:

1. add the minimal public contract, implementation adapter, package docs, and
   contract tests on a Community feature branch;
2. validate the Enterprise implementation locally with an uncommitted Go
   workspace;
3. merge and release the Community change as a tagged version;
4. update the Enterprise `go.mod`/`go.sum` to that tag and remove temporary
   adapters;
5. run the Enterprise compatibility, isolation, upgrade, and distribution
   tests before merging the Enterprise change.

The Enterprise repository may run a non-blocking compatibility build against
the Community default branch (currently `main`), but a floating branch is never
a release dependency.

## 9. Community acceptance gates

A Community tag is Enterprise-ready for a designated surface only when:

- its protected packages and lifecycle semantics are documented;
- contract tests cover registration, duplicates, unknown types, missing
  dependencies, concurrency, cancellation, and declared failure behavior;
- Community reference implementations pass the same contract where applicable;
- Caddy and standalone assembly parity is verified;
- local CLI validation coverage is verified where configuration is shared;
- Community binaries are checked not to link known Enterprise import paths;
- public configuration and event schema changes are detected in CI;
- migration notes identify every intentional protected-surface change.

These gates protect the extension boundary; they do not make the rest of the
repository backward compatible or prescribe Enterprise features, packaging,
licensing, customers, or delivery dates.

## 10. Related Community designs

- [Community Guardrails Core](guardrails.md)
- [Architecture Overview](../architecture/architecture-overview.md)
- [Gateway Bundle YAML](gateway-bundle-yaml.md)
- [Request Pipeline](request-pipeline.md)
- [Observability](observability.md)

## 11. v0.7 foundation contract decisions

These decisions fix the implementation target. Entry construction, public
metrics values, linked registries/manifests, configuration and attribution/events
and Admin contribution/limiter contracts are implemented as candidates.
Consumer evidence is a candidate gate, not a released-tag
or paired-private-repository Enterprise-ready declaration. Changes to the target
must update this design and the active plan together.

### 11.1 Package ownership and public boundary

| Public package | Owner and intended protected surface |
| --- | --- |
| `pkg/distribution/caddycmd` | Caddy command construction/run adapter and assembly options |
| `pkg/distribution/daemoncmd` | Standalone command construction/run adapter and assembly options |
| `pkg/distribution/ctlcmd` | Management command construction/run adapter and local validation options |
| `pkg/distribution` | Edition metadata, public metrics configuration, and explicit assembly options |
| `pkg/extension` | Linked module descriptors, registry, manifest, configuration envelope and prepared snapshot lifecycle |
| `pkg/attribution` | Immutable request attribution values and context accessors |
| `pkg/event` | Versioned public envelopes, sink lifecycle and bounded delivery options |
| `pkg/extension` (Admin types) | Route declarations and narrow contribution dependencies |
| `pkg/gateway/virtualkey/limiter` | Backend, factory, policy snapshot, admission and lifecycle contracts |
| `pkg/extension/contracttest` | Registry/configuration, attribution, event and Admin body suites |
| `pkg/gateway/virtualkey/limiter/contracttest` | Backend lifecycle and admission suite |

Only symbols named in landed package documentation become protected. The table
does not protect all exports in those packages. Existing `standalone/server`,
`caddy/gateway`, `pkg/admin`, and `pkg/gateway` remain implementation surfaces
unless separately designated.

The new entry options use public value types; adapters translate public metrics
options into `usage.Config` internally. External assembly does not construct an
internal observer, query service or pipeline. Public signatures and their
transitive required types must expose no Community `internal/` package. Internal
implementation imports are allowed.

Command construction creates fresh command/flag state per invocation. Loading
`.env.local` then `.env`, signal ownership and `os.Exit` occur in execution,
never on importing a reusable package. Preserve current defaults, environment
precedence, flags, stdout/stderr and exit codes. Final main packages retain
edition-specific imports. Caddy continues to delegate standard command behavior
to Caddy rather than maintaining a second command implementation.

The landed reusable entry surface is `distribution.Edition`, `Assembly`,
`Assembly.Clone`, `MetricsConfig`, `OTLPConfig`; `caddycmd.Command`, `New`,
`Command.Assembly`, `Command.Run`; `daemoncmd.Options`, `ServerOptions`,
`NewCommand`, `Run`; and `ctlcmd.Options`, `NewCommand`, `Run`.
`extension.Descriptor` supplies the metadata value carried by Assembly.
Caddy `New` owns a defensive metadata snapshot; `Run` delegates the fresh command
tree and process lifecycle to upstream `Main`, including `os.Exit`. Daemon/CLI
`Run` return exit codes for main to use. Their constructors accept public values,
read current environment defaults, and leave dotenv loading to execution.

Both Community server mains pass `CommunityRuntime()` metadata, CLI passes
`CommunityValidation()`, and module imports remain explicit in mains. The entries retain
metadata snapshots in the Caddy adapter and daemon/CLI command annotations.
Annotations and Community metadata helpers are not independently protected APIs.
`Assembly.Factories` holds executable registrations separately from descriptors;
`Assembly.Registry`, `Assembly.Manifest` and `caddycmd.Command.Manifest` are
additional designated entry methods. `Assembly.AttributionPolicy` validates the
exact attribute allowlist; `AttributeKeys`, `AttributionProducer` and `EventSinks`
are explicitly injected assembly values, excluded from serialized manifests.
Each sink registration names a linked module and constructs a fresh sink for
each server lifecycle, including Caddy reloads. Duplicate/unlinked/nil sink
registrations and invalid transport options fail assembly. Assembly clone owns
the key and registration slices; factories remain module-owned callbacks. Registries own factory metadata copies,
reject undeclared factory IDs, and support schema-only CLI registrations.
`manifest` in each binary serializes canonical metadata; `agwctl manifest --remote`
and protected `GET /admin/manifest` report frozen server metadata.
The separate `tests/extensionconsumer` module builds all three entries and walks
their required public type closure. `make test-extension-consumer` runs this
candidate gate with a temporary replacement and `GOWORK=off`; no local
replacement or workspace file is committed. Release protection still requires
the tag-level gates in section 9.

### 11.2 Identity, manifest and registration

Capability status: **Implemented**, with candidate consumer contracts. The exact
extension symbol list is in `pkg/extension/doc.go`; reusable suite functions are
`contracttest.Registry` and `contracttest.Configuration`. Other package exports
are not implicitly protected.

Module and capability IDs use lower-case reverse-domain names, at most 128 ASCII
bytes, with nonempty dot-separated segments containing letters, digits or
hyphens. They are distinct from existing Caddy module IDs and provider type names;
no runtime identifier is renamed to satisfy this metadata format. Community IDs
use `online.agentguide.community.*`; the independent fixture uses
`example.fixture.*`. Private IDs belong only in the private repository.

Module versions are semantic-version strings. Schema versions are positive
integers; unsupported versions fail without implicit migration. Manifests sort
modules by ID and capabilities/dependencies lexically, reject duplicates, and
serialize roles from the fixed set `runtime`, `validation`, `admin`, `spi`.
Bound each descriptor to 64 capabilities, 64 dependencies and 64 KiB serialized
JSON; bound an assembled manifest to 256 modules. Exceeding these limits fails
assembly explicitly.
Runtime factories are held separately from serialized descriptors. Both server
entries receive the same runtime descriptors; CLI entries receive the declared
validation projection, not a claim to execute server modules.

Registry assembly returns typed errors with stable codes `invalid_registration`,
`duplicate_module`, `unknown_module`, `missing_dependency`, `dependency_cycle`,
and `schema_mismatch`. Dependency resolution is deterministic and topological;
simultaneously ready modules sort by ID, including newly unblocked modules.
Freeze the registry before serving. Factories are never invoked
by a manifest query or request-time directory lookup.

### 11.3 Configuration ownership, mutation and secrets

Capability status: **Implemented**, with candidate consumer contracts. Registries
validate locally without resolving secrets, and Manager prepares resources under
a separate generation lifetime. Prepare receives only declared dependencies.
The current implementation re-prepares configured modules for each mutation;
resource identity across unrelated mutations is not promised. Module owners
that require preserved domain state must own/ref-count that state independently
of preparation handles. The limiter contract below retains its explicit
unchanged-policy semantics.

The extension CRUD boundary has its own full-body and compression checks. The
shared policy covering every Community and contributed Admin route remains
Proposed in section 11.5. Public envelope mutation events are implemented;
contributed subroutes remain Proposed. Envelope CRUD is Community-owned.

Add `extensions` to GatewayBundle as a list of envelopes containing `module_id`,
`schema_version` and `config` (`json.RawMessage`). One envelope per module is
allowed. The common decoder rejects duplicate JSON object keys before secret classification
or environment expansion. Module-owned strict decoding validates its config; this envelope cannot
override providers, routes or protocol fields. Limit the list to 64 entries,
each config to 256 KiB of encoded JSON and nesting to 32 levels. Unknown configured
modules fail local validation when unavailable locally, and always fail on the
server when not linked. CLI validation cannot authorize server execution.

Persist dynamic envelopes in a schema-bound `extensions` store in the existing
ConfigStore backend. Static envelopes remain startup-owned, visible but read-only;
a static/dynamic duplicate ID is a startup error. Caddy JSON and its Caddyfile
adapter feed the same envelope validator as standalone static bundles. The
Caddyfile adds `extension <module_id> <schema_version> <config-file>`; the file
contains module-owned JSON and uses the same secret-reference handling.

Community owns `GET /admin/extensions` and
`GET|PUT|DELETE /admin/extensions/{module_id}`. List/get return reference-preserving
envelopes. PUT performs server validation, dependency checking and bounded
resource preparation before persistence. Preparation cannot mutate live state.
Persist the candidate, then atomically publish its prepared snapshot; publication
is infallible. Persistence failure disposes prepared resources and leaves the
old snapshot active. Serialize dependent mutations, and publish one generation
covering all affected dependencies. Requests retain their admitted generation;
retire resources after its in-flight users drain, with bounded shutdown.
Preparation and health calls receive contexts with a five-second maximum budget,
capped by the caller's deadline. Snapshot publication performs no I/O. Factories
must support concurrent snapshot reads and honor lifecycle cancellation. Module
callback errors and panics are converted to bounded codes. A callback that ignores
cancellation is nonconforming; the process can report failure at its deadline but
cannot force arbitrary module code to terminate.
DELETE rejects a module still required by another configured envelope with 409.

Bundle apply first applies extensions in dependency order, then follows the
existing resource order. It stops before dependent resources if extension apply
fails. The whole Bundle remains a sequence of resource mutations, with explicit
partial-result reporting and no cross-resource rollback promise. Export includes
the effective extension envelopes and their static/dynamic ownership. Existing
Bundle ownership semantics remain in force; no second configuration authority
or event store is introduced.

Factories declare secret fields using exact JSON Pointers. At those locations,
only an object of the form `{"$secret_ref":"env:NAME"}` is accepted for a secret;
literal secrets and undeclared `$secret_ref` objects are rejected. `NAME` follows
the existing environment-variable name grammar. v0.7 supports environment
references only; it adds no SecretResolver SPI. Walk and classify references
before generic `${NAME}` expansion. Local validation checks syntax but does not
resolve references; server preparation resolves from the server environment.
Missing references fail preparation with a bounded code and field path, never
the value. Persisted/exported config retains the reference object, including
when the environment variable is set. Resolved values exist only in owned
runtime resources; they never enter DTOs, events, diagnostics or manifests.
Environment rotation requires explicit reapply/reload and creates a generation.

### 11.4 Attribution and public events

Capability status: **Implemented**, with candidate external-consumer contracts.
Exact candidate symbol lists live in `pkg/attribution` and `pkg/event` package
comments. Public suites are `contracttest.Attribution`, `contracttest.EventSink`
and `contracttest.CheckSinkCancellation`. Released-tag and paired-repository
protection still require section 9's gates. Admin contribution hooks and alternate
limiter backends are owned by their later contracts.


The existing validated VirtualKey boundary establishes subject as
`virtualkey:<id>` and sets VirtualKey ID. It supplies no tenant. An explicitly
injected trusted attribution producer may provide subject/tenant; it is not
selected from client headers and receives no general gateway service locator.
Mount authentication alone does not synthesize a subject unless a trusted
adapter supplies it. Route/Agent/run fields come from the selected runtime;
trace/span/parent come from the existing validated tracing lifecycle. Missing
fields remain absent. A tenant header has no effect.

Identity/correlation fields are bounded to 256 UTF-8 bytes each (trace IDs retain
their existing format). Attributes are string values with exact keys allowlisted
by assembly; keys must begin with a linked module's ID plus `/`. Limit to 16
attributes, 128 bytes per key, 256 bytes per value and 4 KiB total. Copy values
on context insertion and retrieval. Invalid producer values return a typed
error; optional attribution must not replace valid core correlation or leak an
unbounded value into events.

The v1 event envelope contains schema version, event ID, family, UTC timestamp,
attribution, bounded outcome/reason codes and a typed family DTO. Protect only
`limiter.admission` (dimension, allow/deny/degraded outcome, retry-after and backend
ID) and `extension.mutation` (module ID, operation and success/failure outcome)
in this slice. Successful contribution mutations use the latter hook after
commit; failed mutations emit a bounded failure code. The mutation DTO permits a
boolean `state_reset` for a committed limiter policy/backend reset. Neither DTO
admits arbitrary
maps, error strings, prompts, tool arguments, bearer keys, secret references or
resolved values. IDs remain event fields, never metric labels. Encoded envelopes
are at most 16 KiB; reject invalid events before enqueue and count rejection.

The existing pipeline remains the single event authority. Domain adapters
construct allowlisted DTOs and its public publishing boundary validates them
before per-sink enqueue; it never forwards internal usage objects. No new
usage-accounting store or replay path is created. Each external sink has
a bounded queue (default 256 events, configurable 1–4096), one worker and FIFO
delivery for accepted events. Ingress never waits on a sink. Queue overflow drops
the newest event for that sink and increments its drop counter. Delivery is
best-effort, without retry or durable replay; a sink error increments its failure
counter and does not fail admission or another sink.

Sink `Write`, `Flush` and `Close` receive contexts. Default write budget is one
second; flush/close share a shutdown budget of five seconds, bounded by the
caller's deadline. Sinks must honor cancellation; no unbounded goroutine is
created per write to hide a non-cooperative sink. Flush waits for events accepted
before its barrier. Shutdown rejects new events, drains accepted events within
budget, counts undelivered queued events, and closes exactly once. Concurrent
flush/close and repeated close are defined and tested. Process teardown returns
at its deadline even if an invalid sink remains stuck; that violation is reported
as unhealthy and must fail its contract suite. No parallel usage-accounting
pipeline is created. Only a delivery worker, plus fixed shutdown coordination,
is created per sink. Flush control barriers have separately bounded capacity
and cannot consume event capacity. A shorter concurrent Close caller does not
shorten the first caller's shared drain budget. Shutdown cancellation reaches
an already-running Write or Flush. A callback failure or panic becomes a bounded
`sink_failure` or `sink_panic` code, with no callback diagnostic content.

The existing metrics summary exposes combined drop/failure counters plus
`pipeline.rejected_events` and `pipeline.public_sinks` health/counters in assembly
registration order. Successful extension PUT/DELETE events follow persistence
and snapshot publication; failed mutations expose only allowlisted reason codes.
Committed mutation events are enqueued before releasing the mutation gate and
notifying shutdown waiters, preserving commit order through pipeline admission.
Publication holds no manager mutex and performs no sink I/O.
Current Community in-process limiting emits allow/deny events for configured
policies. Unlimited admission emits no limiter event. Trusted subject/tenant and attributes
survive nested runtime correlation updates; selected route/Agent/run and validated
trace identities replace producer-supplied core fields. Invalid optional producer
values or panics are ignored in favor of bounded runtime correlation and the
validated VirtualKey subject fallback. The boundary omits existing identifiers
that exceed public bounds. Mount auth alone still adds no subject or tenant.

The public `event.Delivery` implements sink transport and its contract fixtures;
server adapters construct it only through their existing pipeline. It is not an
independent event producer or an alternate usage-accounting authority. Legacy
internal usage sinks retain their serial processing semantics; the pipeline closes
those sinks once and bounds the wait without introducing a goroutine per write.
Standalone reports a sink shutdown failure as an execution error; Caddy returns it
from app Stop.

### 11.5 Admin ownership and body policy

Capability status: **Implemented**, with candidate contracts. Released-tag
protection still requires the published-tag and paired-repository gates.
`Factory.AdminRoutes` declares immutable `AdminRoute` method/path pairs; the
registration descriptor supplies their owner and must include the `admin` role.
`Registry.AdminRoutes` and `Manager.AdminRoutes` project `OwnedAdminRoute` values.
Prepared resources implement `AdminContributor.ServeAdminHTTP`; missing handlers
fail preparation before publication. `AdminDependencies` carries a cloned envelope,
logger, attribution accessor and owner-scoped event publisher. `WriteAdminError`
encodes stable, bounded codes. Exact symbols are designated in `pkg/extension/doc.go`.

Supported patterns consist of literal segments and single-segment `{name}`
wildcards; host patterns, subtree patterns, multi-segment wildcards, trailing
slashes and module roots are rejected. Supported methods are GET, HEAD, POST,
PUT, PATCH and DELETE. GET also owns HEAD. OPTIONS belongs to shared CORS
preflight. Even containment overlaps are conflicts when methods intersect.
The literal owner namespace separates contributions from every Community family
and the AgentRoute mux; no module wildcard can cross that boundary. The whole
immutable declaration set is checked at registry construction before handlers
are registered, without using a ServeMux panic as validation.

Admin dispatch retains the active generation lease for the entire call. The
handler context combines request cancellation with the generation lifetime;
retirement first drains leases, then cancels at its five-second deadline and
closes resources in reverse dependency order. Resource preparation and health
hooks reuse F2; there is no parallel contributor lifecycle. Unconfigured owners
respond with `extension_unavailable`/503. Trusted attribution is installed by the
same explicit producer used on data-plane ingress, without treating headers or
mount authentication alone as identity. The publisher accepts only the owning
module's typed `extension.mutation` events, stamps trusted request attribution,
and delegates validation/delivery to the existing pipeline. `Manager.AdminPublisher`
binds the validated owner during HTTP assembly and returns a request binder.
The HTTP adapter invokes that binder with the trusted request context without
registry/config reads. Each resulting publisher captures that request's identity;
module-supplied publication contexts affect cancellation/delivery only, never
actor identity or correlation. Publication snapshots the injected pipeline under
only the manager mutex and never takes the Gateway lock, so a draining callback
cannot deadlock Gateway shutdown.

Contributions own only `/admin/extensions/<module_id>/...`; Community envelope
CRUD owns the module root. Reserve all other Community prefixes, including
`/admin/memory`. Route declarations specify method, pattern and owner. Reject
duplicate, overlapping or ambiguous patterns at assembly with typed
`route_conflict`, including conflicts across the existing AgentRoute mux boundary.
Check the entire declaration set before registering handlers; do not expose a
ServeMux panic as the public conflict API.

Dependencies consist of immutable module config, logger, attribution access,
public event publisher and module-owned health/lifecycle hooks. They do not expose
AgentGateway, raw ConfigStore, credentials or unrestricted route replacement.
Contributed routes run through the existing Admin mount authentication, CORS,
panic recovery and logging. Initialization failure prevents startup; readiness
reports module health. Stop admitting requests before draining handlers and
closing module resources in reverse dependency order.

All Admin request bodies are limited to 4 MiB (4,194,304 bytes), measured as
received bytes; compressed request bodies are unsupported and return 415. The
shared boundary reads at most limit+1 before invoking a mutation handler, including
unknown-length/chunked bodies, so an oversized trailing body cannot commit a
mutation first. Oversize returns 413 with exactly
`{"error":"request body exceeds 4194304 bytes"}` and no payload echo. Error JSON
is bounded to 1 KiB with stable codes in new extension errors; existing supported
success payloads and existing error shapes remain unless explicitly changed.
Panic recovery emits a generic bounded JSON 500. The same policy covers existing
Community and contributed routes. Bundle apply is per-object HTTP: the 4 MiB limit
applies to each serialized resource, not to the total local YAML file.

### 11.6 Limiter backend semantics

**Implemented candidate.** The domain owns
`pkg/gateway/virtualkey/limiter`; `doc.go` lists the exact candidate protected
symbols: `Dimension` and its constants, `Policy`, `Clock`, `LimiterRequest`,
`LimiterDecision`, `LimiterBackend`, `LimiterProvider`, `Config`, `ModuleID`,
`MemoryBackendType`, `Factory`, `NewMemoryBackend`, reference backend lifecycle
methods and the bounded unavailable/retired sentinels. Backend provider lookup
runs during module preparation. The prepared selection retains only typed backend
handles and immutable settings; the request path acquires the current generation
and never resolves factories or reads ConfigStore.

`LimiterBackend.Admit(ctx, request)` consumes atomically; `Retire(ctx, generation)`
fences late operations; `Health(ctx)` and `Close(ctx)` use the normal prepared
resource lifecycle. Request policy fields are values, with no bearer credential.
Reference bucket initialization is lazy; policy/configuration publication is
owned by the VirtualKey/extension managers. Each admission uses the current
prepared connection handle while retaining an unchanged quota generation.
Generation-scoped storage must survive connection-handle refresh. A production
remote operation may consume before losing its reply; the gateway never retries.
The SPI does not promise exactly-once remote delivery or token transfer and
includes no production distributed implementation.

`extension.StatefulResource.StateResetComparedTo(ctx, previous)` lets an unpublished
candidate retain an unchanged domain identity before persistence. It cannot
mutate the previous/live resource. Removal or changed identity marks the committed
mutation's `state_reset`; failed preparation/persistence never reports a successful
reset. `Snapshot.Sequence()` provides a manager-local monotonic generation fence
with possible gaps from failed candidates. This preserves policy identity while
all prepared handles still close once according to the normal generation lifecycle.

The backend receives a context, immutable policy generation, VirtualKey ID and
one of `llm`, `mcp`, `agent`. Admission is one atomic consume-or-deny operation
per key/dimension, consuming exactly one request token on allow and none on deny.
No multi-dimension transaction or global ordering is promised. Retry-after is a
nonnegative duration, rounded up to at least one second when writing a denied
HTTP response. The gateway exposes only bounded diagnostic codes, regardless of backend callback
diagnostics.

Keep the existing process-local backend as the default. Configure an alternate
backend through its registered extension and the Community-owned envelope
`online.agentguide.community.limiter`, schema 1. Its config contains
`backend_module_id`, `backend_type`, `timeout_ms` and `failure_mode`
(`closed` or `open`); defaults select the linked in-process implementation,
100 ms and `closed`. This selection declares a dependency on the selected backend
module and is prepared with that module in the same mutation generation;
never fall back to a local bucket silently. Admission defaults to a 100 ms
budget, configurable 1 ms–1 s and capped by the request deadline. Caller
cancellation terminates admission without fail-open handling. A retired-generation
admission must return `ErrLimiterRetired` (possibly wrapped); this fence is never
treated as backend unavailability and never fails open. On timeout or
backend failure, a configured hard limit defaults to fail-closed (HTTP 503 with
a bounded `limiter_unavailable` error); explicit fail-open permits the request
with a degraded event/counter. Exhaustion remains HTTP 429. No configured policy
means unlimited and does not call the backend.

An uncertain remote operation may have consumed a token: the gateway never
automatically retries admission. Backend implementations must document this
possibility and honor cancellation. Health cannot bypass a configured hard limit.
Initialize and prepare policy snapshots before activation, publish atomically,
and reject unknown or unavailable selected backends at startup/mutation.

Policy generations scope all bucket operations. Existing admitted requests may
finish against their generation; new requests use the committed generation.
Update/delete/reset retires affected bucket state. Unchanged policies preserve
their buckets; changed policies start full buckets, matching current Community
behavior. A backend switch resets state;
v0.7 promises no cross-backend token transfer. Report resets through the
`extension.mutation` DTO's `state_reset` field. Remote implementations must fence
retired generations so late operations cannot recreate active old buckets.
Shutdown stops new admissions, drains/cancels in-flight calls and closes once
within the common five-second module budget. Counters label only dimension,
outcome and bounded backend type, never VirtualKey, route, tenant or subject IDs.
Backend type uses a 32-character identifier, with at most 32 distinct type labels
per gateway and `other` overflow. `limiter/contracttest.Limiter` and `LimiterRefill`
run identically against Community memory and an independent fixture backend.
Real Caddy/agwd consumers prove refresh preservation, switches, 429/503, ambiguous
consumption, timeout, explicit degradation, reset events, bounded labels and shutdown.

### 11.7 Evidence and release sequence

Create `tests/extensionconsumer` as a separate Go module in F1 and build all
three consumer commands immediately. Development tests may inject a temporary
absolute replacement for the checked-out Community tree; do not commit that
replacement or a `go.work`. The fixture is independently maintained test code,
not a copy of Community commands or a mock of their assembly.

Grow its evidence with each surface: manifest projection, reference-preserving
config, protected Admin routing, attribution/events and the deterministic limiter.
Run both Community and fixture backends through the public limiter suite,
including race, cancellation and ambiguous-error cases. Compare process flags,
environment precedence, exit codes, cancellation, streaming and usage evidence
before removing old command assembly. Perform a transitive public-signature
check and private-import isolation check, not merely a text search of main files.

Candidate compatibility validates an exact commit; Community release gates do
not require the not-yet-existing release tag. After Community tagging, private
release gates consume that tag with `GOWORK=off` and no local replacement.
Private-repository evidence is a separate required deliverable and cannot be
claimed from Community fixture results. Publish the exact protected symbol list
only after these gates pass.
