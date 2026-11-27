# Unreleased

- The default AgentRoute ingress is now named **Agent Turn Protocol**, with
  `at` as its configuration value. Existing `protocol: agent` inputs and
  stored routes are read as `at`. New writes, Admin responses, bundle exports,
  and new usage events emit `at`. Clients continue to use the same HTTP
  endpoints, JSON requests, and SSE events.

- ACP Agents share one ACP v1 stdio client. Use `runtime.acp.launcher` and
  the built-in `codex` or `opencode` configuration, or register a third-party
  launcher in SQLite through the offline tool. Both gateway hosts load the
  registry from ConfigStore. Update source bundles and migrate old Agent
  definitions and previous external launchers with the
  [ACP launcher migration](../guides/architecture-config-recovery.md#migrate-acp-launchers).
  Explicit model selection now uses the model option advertised by the Agent.

- ACP session reads cancel permission requests. Prompt and transcript buffer
  overflow surfaces an explicit error. Optional session capabilities reflect
  the current Agent configuration's ACP handshake, with discovery marked
  pending before the first observation.

- Go integrations use `pkg/acp/launcher` for launch presets,
  `pkg/acp/client` for ACP client requests, and `pkg/acp/session` for the
  ACP session manager and config.
