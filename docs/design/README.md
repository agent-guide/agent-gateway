# Design

This section is for durable design decisions and technical contracts. A design
explains why the system has a particular shape and which boundaries,
invariants, and tradeoffs must remain true.

Each design declares its capability status as `Implemented` or `Proposed`.
Capability limits such as an unsupported protocol binding belong in the
design; completed phases, commit lists, rollout checklists, and file-by-file
implementation tasks do not. Active execution work lives in
[`../plans/`](../plans/README.md), current runtime behavior lives in
[`../architecture/`](../architecture/README.md), and delivered version history
lives in [`../releases/`](../releases/README.md).

Current design pages:

- [agents-control-plane.md](agents-control-plane.md)
- [builtin-agent-runtime.md](builtin-agent-runtime.md)
- [http-agent-runtime.md](http-agent-runtime.md) — `runtime.type = "http"` design: A2A Protocol 1.0 JSON-RPC, shared `pkg/a2a`, Path B translating backend then Path A governed proxy
- [guardrails.md](guardrails.md) — Community Guardrails Core: policy/check SPI, typed inspection, safe rewriting, enforcement, and decision events
- [enterprise-extension-contract.md](enterprise-extension-contract.md) — Community-side SPI, compatibility, assembly, and cross-repository rules for separately maintained distributions
- [model-first-routing.md](model-first-routing.md)
- [route-target-policy.md](route-target-policy.md)
- [provider-type-startup-policy.md](provider-type-startup-policy.md)
- [virtual-key-rate-limiting.md](virtual-key-rate-limiting.md)
- [gateway-bundle-yaml.md](gateway-bundle-yaml.md)
- [mcp-tool-policy.md](mcp-tool-policy.md)
- [memory.md](memory.md)
- [observability.md](observability.md)
- [external-agent-observability-correlation.md](external-agent-observability-correlation.md)
- [protocol-support-strategy.md](protocol-support-strategy.md)
- [anthropic-protocol-fidelity.md](anthropic-protocol-fidelity.md) — target architecture for shared Anthropic/CC protocol handling, native-state fidelity, stream encoding, and dialect capabilities
- [anthropic-stream-transition-table.md](anthropic-stream-transition-table.md)
- [eino-reuse.md](eino-reuse.md)
- [request-pipeline.md](request-pipeline.md)
- [zhipu-vision-model-routing.md](zhipu-vision-model-routing.md)
