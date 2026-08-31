# Enterprise Extension Contract

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
