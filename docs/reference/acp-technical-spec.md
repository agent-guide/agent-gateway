# ACP Runtime Technical Specification

ACP is an Agent runtime backend. It is not a public service or route family:
the Agent owns process configuration inline under `runtime.acp`, and a unified
AgentRoute targets the Agent by `agent_id`.

## Configuration Model

```yaml
agents:
  - id: codex-main
    runtime:
      type: acp
      acp:
        launcher: codex
        cwd: /workspace
        allowed_roots: [/workspace]
        max_instances: 4
        permission_mode: deny
    routes: {}
    resources: {}
    policy: {}

agentRoutes:
  - id: agent-codex
    protocol: at
    agent_id: codex-main
    match_policy: {path_prefix: /agents/codex}
    auth_policy: {require_virtual_key: true}
```

The persisted stores are `agents` and the shared `routes` store. Databases or
bundles containing pre-unification ACP service or runtime-specific route fields
are rejected with `legacy_agent_runtime_config`; export them with the old binary,
migrate the bundle offline, then apply it to a fresh current database. Existing
inline ACP definitions can use the [offline launcher migration](../guides/architecture-config-recovery.md#migrate-acp-launchers).

Supported ACP fields are `launcher`, `cwd`, `allowed_roots`, `default_model`,
`env`, `config_overrides`, `idle_ttl`, `max_instances`, and `permission_mode`.
Set `idle_ttl` as an integer number of nanoseconds. A positive value enables
idle expiration for pooled processes.
An empty permission mode defaults to `deny`. Auto-approval requires an attached
turn stream. Session listing and transcript replay cancel permission requests.

### Deployment launchers

Select a registered launcher by name. The defaults are `codex` (`codex-acp`) and
`opencode` (`opencode acp --cwd <cwd>`). Deployment-owned definitions are stored
in `acp_launchers` in the same SQLite database as the Agents. A definition
with a built-in name overrides that default.

Stop services using the database before registering or replacing a launcher.
Pass the executable's arguments after `--`, keeping each argument separate.

```sh
./scripts/repair-architecture-config --db ./data/configstore.db \
  --backup ./data/configstore.before-launcher.db \
  --set-acp-launcher coding-agent --acp-command /opt/agents/bin/coding-agent \
  --acp-cwd-flag=--cwd -- acp
```

Set `launcher: coding-agent` in the Agent definition. The gateway resolves
executables by name using its PATH and resolves explicit relative paths from
its own working directory. The host passes each argument directly. It sets the
Agent process working directory and sends it in ACP session
requests. `--acp-cwd-flag` adds the flag and effective working directory as two
arguments. Both hosts load the SQLite launcher registry at startup.

The process must negotiate ACP v1 over stdio using JSON-RPC 2.0.
Session listing and loading require the advertised capabilities. The agent
controls persistence and concurrent loading. A session may become available
for replay after the first completed turn or after its owning process exits. Model
selection applies at session creation when `default_model` or the turn
`model` is supplied. The host sets the advertised `select` option with
`category: model`, then applies explicit `config_overrides`. An explicit
model requires an advertised model selector.

## Dispatch

```text
POST /<agent-route>/turn
  -> resolve AgentRoute.agent_id
  -> read Agent from the in-memory definition snapshot
  -> select runtime.type = acp
  -> read the agent_id keyed ACP host configuration snapshot
  -> acquire/create a fingerprint-matching pooled process
  -> run the native ACP prompt
  -> emit the common Agent SSE envelope
```

The HTTP request uses the Agent Turn Protocol (`at`) envelope:

```json
{
  "input": "Fix the failing test",
  "session_id": "optional-native-session",
  "options": {
    "version": "v1",
    "runtime": {
      "thread_id": "thread-1",
      "cwd": "/workspace",
      "model": "optional-override",
      "fresh_session": false
    }
  }
}
```

Each event carries `agent_id`, `run_id`, and a monotonically increasing
`sequence`. ACP-specific session/thread values remain backend extension data.

## Pool And Snapshot Invariants

- Runtime configuration is prepared from `Agent.runtime.acp` and its resolved
  deployment launcher.
- Pool ownership is keyed by `agent_id`, never a service ID.
- A change to the effective host configuration fingerprint, disabling an Agent,
  switching runtimes, or deleting an Agent retires stale instances. Active turns
  drain before final cleanup.
- A request cannot repopulate an instance with an obsolete fingerprint after
  retirement.
- Session-addressed turns adopt an already-bound live instance when possible.
- Exact run cancellation sends native `session/cancel` for that run and does
  not close an unrelated thread or process.

## Permissions

ACP permission handling is fail-closed. Agent-bound requests are published to
the common permission broker and retain only an opaque backend continuation.
List and decide them through:

```text
GET  /admin/agents/{agent_id}/permissions
POST /admin/agents/{agent_id}/permissions/{request_id}
```

The data plane also accepts the common AgentRoute permission/resume flow. ACP
option IDs are preserved exactly; flat boolean approval payloads are invalid.

## Sessions And Transcripts

Consumer endpoints use the matched AgentRoute and its VirtualKey policy:

```text
GET /<agent-route>/sessions?cwd=...&cursor=...
GET /<agent-route>/sessions/{session_id}/transcript?cwd=...
```

Operator endpoints are capability-gated and Agent-scoped:

```text
GET /admin/agents/{agent_id}/sessions
GET /admin/agents/{agent_id}/sessions/{session_id}/transcript
```

The gateway canonicalizes the optional `cwd` filter and checks advertised ACP
capabilities before calling session list or load. The Admin capabilities
response reports `sessions.discovery_pending: true` until the current Agent
configuration completes an ACP handshake. A first session list or transcript
request can perform that handshake. Subsequent capability reads reuse the
recorded result. ACP load support does not certify durability of session state.

A full update buffer fails the turn or transcript read. A turn that has already
started streaming reports an `error` event. A transcript read returns an
error response in place of partial history.

## Runtime Operations

```text
GET    /admin/acp/runtime
GET    /admin/acp/runtime/inflight
DELETE /admin/acp/runtime/agents/{agent_id}/threads/{thread_id}
```

Thread close is an ACP-specific destructive pool-recovery operation. Ordinary
cancellation uses `DELETE /admin/agents/{agent_id}/runs/{run_id}`.

## Errors

- malformed or unsupported common options: `400`
- missing AgentRoute or Agent: `404`
- disabled/non-executable Agent: normalized runtime error
- VirtualKey rate limit: `429` with `Retry-After`
- unsupported session capability: `501`
- native process or transport failure before streaming, including update buffer overflow: `502`
- unavailable runtime/backend: `503`

See [acp-api.md](acp-api.md) for endpoint examples and
[../architecture/acp-architecture.md](../architecture/acp-architecture.md) for
the runtime design.
