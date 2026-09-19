# Documentation

This directory holds the detailed documentation for `agent-gateway`. The repository root `README.md` is intentionally limited to project overview and quick start; detailed operational, architectural, and design material belongs here.

## Sections

The documentation is organized into these categories:

- `getting-started/`
  - first-run setup for `agw`, `agwd`, and `agwctl`
  - first route, first VirtualKey, first successful request
- `guides/`
  - task-oriented usage guides such as providers, routes, OAuth credentials, bundle YAML, and MCP operations
- `reference/`
  - Caddyfile syntax, Admin API endpoints, CLI command reference, provider options, and runtime mode reference
- `architecture/`
  - current implemented system architecture, ownership, and request flow
- `design/`
  - durable decisions, contracts, invariants, tradeoffs, and rejected alternatives
- `plans/`
  - time-bound execution plans tied to a version line; deleted once their
    work lands and the permanent documents describe the result
- `releases/`
  - durable release notes describing user-visible changes between tags
- `development/`
  - contributor-facing process or collaboration material

Category index:

- [getting-started/](getting-started/README.md)
- [guides/](guides/README.md)
- [reference/](reference/README.md)
- [architecture/](architecture/README.md)
- [design/](design/README.md)
- [plans/](plans/README.md)
- [releases/](releases/README.md)
- [development/](development/README.md)

## Current Documents

Primary detailed documents:

- [architecture/architecture-overview.md](architecture/architecture-overview.md): current repository architecture overview
- [architecture/configstore-architecture.md](architecture/configstore-architecture.md): ConfigStore architecture and persistence contract
- [architecture/mcp-architecture.md](architecture/mcp-architecture.md): current MCP gateway architecture
- [architecture/acp-architecture.md](architecture/acp-architecture.md): ACP gateway architecture and implementation status
- [architecture/http-agent-architecture.md](architecture/http-agent-architecture.md): current HTTP Agent Path A and Path B architecture
- [getting-started/quickstart-acp.md](getting-started/quickstart-acp.md): ACP gateway quick start
- [reference/acp-api.md](reference/acp-api.md): ACP dispatcher and Admin API reference
- [reference/acp-technical-spec.md](reference/acp-technical-spec.md): ACP service, route, runtime, and event specification
- [design/agents-control-plane.md](design/agents-control-plane.md): shared Agent identity, resources, runtime contracts, and the external Business Workflow boundary
- [design/request-pipeline.md](design/request-pipeline.md): synchronous Gateway Request Pipelines and upper-layer durable orchestration through Temporal or another external engine
- [design/builtin-agent-runtime.md](design/builtin-agent-runtime.md): builtin ADK host, schema, lifecycle, permissions, and implementation status
- [design/http-agent-runtime.md](design/http-agent-runtime.md): HTTP agent runtime, A2A Protocol 1.0 JSON-RPC, shared `pkg/a2a`, Path B then Path A
- [design/guardrails.md](design/guardrails.md): Community Guardrails Core and external check extension boundary
- [design/enterprise-extension-contract.md](design/enterprise-extension-contract.md): Community-side SPI, compatibility, assembly, and cross-repository rules for separately maintained distributions
- [design/gateway-bundle-yaml.md](design/gateway-bundle-yaml.md): bundle YAML architecture and workflow
- [design/mcp-tool-policy.md](design/mcp-tool-policy.md): MCP tool policy design
- [design/model-first-routing.md](design/model-first-routing.md): model-first routing architecture
- [design/route-target-policy.md](design/route-target-policy.md): route target policy architecture
- [design/memory.md](design/memory.md): memory subsystem design
- [design/observability.md](design/observability.md): observability design
- [design/protocol-support-strategy.md](design/protocol-support-strategy.md): durable protocol-family and extension policy
- [development/ai-assisted-refactor-collaboration-templates.md](development/ai-assisted-refactor-collaboration-templates.md): contributor collaboration templates

## Notes

- the root `README.md` is intentionally limited to overview and quick start
- `design/` explains why the system has a shape and which rules must remain
  true; it does not retain completed implementation phases
- `architecture/` describes how the checked-in system currently runs; proposed
  behavior belongs in a design document marked `Proposed`
- `plans/` describes work that is actively being prepared or executed; Git
  history, rather than completed plan files, preserves the implementation trail
- `releases/` records what changed in a released version and is the source for
  the corresponding hosted release description
