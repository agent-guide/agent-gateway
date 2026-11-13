# Runtime Modes

`agent-gateway` builds three binaries with different runtime roles.

## `agw`

- the main Caddy-based gateway runtime
- uses a Caddyfile plus the shared config store
- supports normal Caddy subcommands such as `run`, `reload`, `validate`, and `hash-password`

## `agwd`

- the standalone gateway daemon
- uses `--config-store`, optional `--static-config`, optional repeated
  `--provider-type`, `--credential-refresh-command`, and repeatable
  `--credential-refresh-arg` (together defaulting to `agw-auth refresh`)
- does not use a Caddyfile runtime
- if any `--provider-type` flag is set, only those provider types are enabled for the process
- `--max-llm-request-body-bytes` sets the LLM body limit in integer bytes;
  the default is 16,777,216 (16 MiB), `0` selects that default, and negative
  values are invalid. For example, `67108864` allows 64 MiB.

Static model configuration:

- `agwd --static-config` supports `providerModels`, `logicalModels`, and explicit route references
- static models are read-only through Admin; static/dynamic identity conflicts fail loading
- use the Admin API or online Bundle apply for dynamic models and routes

## `agwctl`

- the management CLI
- talks to the gateway Admin API or the Caddy admin API

Primary command families:

- gateway bundle operations and resource commands are available directly under
  `agwctl`
- `agwctl caddy ...`

Recommended workflows:

- use `agwctl apply/export/validate` for bundle-based configuration management
- use `agwctl credential ...` for remote gateway credential management
- use the external `agw-auth` tool for interactive OAuth login flows
- use `agwctl caddy ...` for direct Caddy admin API operations

Bundle YAML examples used by current workflows:

- `examples/gateway.bundle.llm.provider-model.yaml`
- `examples/gateway.bundle.llm.logical-model.yaml`

## Related Docs

- [../getting-started/quickstart-llm.md](../getting-started/quickstart-llm.md)
- [agwctl-reference.md](agwctl-reference.md)
- [caddyfile-reference.md](caddyfile-reference.md)
