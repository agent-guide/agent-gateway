# ACP Technical Specification

This page specifies the current ACP object shapes and runtime contract exposed
by `agent-gateway`.

## Agent Types

Valid `agent_type` values:

- `codex`
- `opencode`

`opencode` launches:

```text
opencode acp --cwd <cwd>
```

`codex` defaults to adapter mode and launches `codex-acp`. The configured
`adapter_command`, when provided, must have basename `codex-acp`.

## ACP Service

ACP services are persisted in the `acp_services` config store and appear in
bundle YAML under `acpServices`.

```json
{
  "id": "codex-main",
  "name": "Codex",
  "agent_type": "codex",
  "cwd": "/tmp/acp-codex-test",
  "allowed_roots": ["/tmp/acp-codex-test"],
  "default_model": "",
  "config_overrides": {
    "model": "example"
  },
  "idle_ttl": 0,
  "max_instances": 4,
  "permission_mode": "auto_approve",
  "disabled": false,
  "description": "",
  "codex": {
    "mode": "adapter",
    "adapter_command": "codex-acp",
    "adapter_args": []
  }
}
```

Fields:

- `id`: required storage ID.
- `name`: required display name.
- `agent_type`: required; `codex` or `opencode`.
- `cwd`: required absolute working directory.
- `allowed_roots`: optional; defaults to `[cwd]`. Every effective turn `cwd`
  must be under one of these roots.
- `default_model`: optional model applied after session creation through
  `session/set_config_option` when supported by the agent.
- `config_overrides`: optional service-level ACP config option values.
- `idle_ttl`: optional Go duration value in JSON. Zero disables idle expiry.
- `max_instances`: optional per-service pooled process limit. Zero means no
  limit.
- `permission_mode`: optional; defaults to `deny`.
- `disabled`: prevents use when true.
- `codex`: optional Codex-specific settings.

The `codex` object has these fields:

- `mode`: `adapter` (default) or `app_server`. `app_server` is recognized but
  not implemented.
- `adapter_command`: optional; defaults to `codex-acp`. Its basename must be
  `codex-acp`.
- `adapter_args`: optional extra arguments for the adapter command.
- `app_server_command`, `app_server_args`, `default_profile`,
  `initial_auth_mode`, `trace_json`, `retry_turn_on_crash`: reserved for the
  deferred Codex app-server bridge and crash-retry work. They are accepted and
  normalized but not consumed by the current adapter runtime.

Validation:

- `cwd` and every `allowed_roots` entry must be absolute.
- `cwd` must be contained by `allowed_roots`.
- `codex.mode: app_server` is recognized but not implemented.
- arbitrary adapter commands are rejected.

## ACP Route

ACP routes are persisted in the shared `routes` store and appear in bundle YAML
under `acpRoutes`.

```json
{
  "id": "acp-codex",
  "kind": "acp",
  "protocol": "acp",
  "description": "",
  "disabled": false,
  "match_policy": {
    "path_prefix": "/acp/codex",
    "methods": ["POST"]
  },
  "auth_policy": {
    "require_virtual_key": true
  },
  "service_id": "codex-main"
}
```

Fields:

- `id`: optional on create; defaults to `acp:<service_id>:<path_prefix>`.
- `kind`: normalized to `acp`.
- `protocol`: normalized to `acp`.
- `match_policy.path_prefix`: route prefix removed before dispatch.
- `auth_policy.require_virtual_key`: when true, the dispatcher requires a
  VirtualKey in `Authorization: Bearer <key>` or `x-api-key`.
- `service_id`: required ACP service target.

Internally the persisted route target policy is:

```json
{
  "kind": "acp-service",
  "service_id": "codex-main"
}
```

The ACP route API and bundle format expose the expanded top-level `service_id`.

## Turn Request

`POST /<acp-route>/turn` accepts:

```json
{
  "thread_id": "t-demo-1",
  "session_id": "optional-existing-session-id",
  "input": "Reply with exactly one word: pong",
  "cwd": "/tmp/acp-codex-test",
  "model": "optional-model",
  "fresh_session": false,
  "config_overrides": {
    "model": "optional-model"
  }
}
```

Required:

- `thread_id`
- `input`

Behavior:

- `cwd` defaults to the service `cwd`.
- `cwd` must be allowed by the service.
- `session_id` loads or adopts an existing session where supported.
- `model` is applied through config options; it is not sent as a non-standard
  field on ACP `session/new` or `session/prompt`.
- `config_overrides` are applied through `session/set_config_option`.
- `fresh_session` creates a new pooled instance/session for the turn scope.

## SSE Events

The turn endpoint returns `text/event-stream`. Each event contains one JSON
object with this general shape:

```json
{
  "session_id": "session-id",
  "request_id": "permission-request-id",
  "text": "chunk text",
  "stop_reason": "end_turn",
  "message": "error text",
  "data": {}
}
```

Event names:

- `session`: session binding; includes `session_id`.
- `delta`: assistant text; includes `text`.
- `reasoning`: reasoning text; includes `text`.
- `content`: non-text content update; includes raw `data`.
- `plan`: plan update; includes raw `data`.
- `tool_call`: tool call or tool call update; includes raw `data`.
- `usage`: usage update; includes raw `data`.
- `available_commands`: slash/command metadata; includes raw `data`.
- `session_info`: session metadata; includes raw `data`.
- `mode`: current mode update; includes raw `data`.
- `config_options`: config option metadata; includes raw `data`.
- `permission`: interactive permission request; includes `request_id` and raw
  `data`.
- `done`: prompt completed; includes `stop_reason` when the agent provided one.
- `error`: gateway/runtime error; includes `message`.

## Permission Decision

Route-side decision endpoint:

```http
POST /<acp-route>/permission
```

Admin escape hatch:

```http
POST /admin/acp/runtime/permissions/{request_id}
```

Body:

```json
{
  "request_id": "perm-...",
  "outcome": "selected",
  "option_id": "allow_once"
}
```

Valid outcomes:

- `selected`: requires `option_id`.
- `cancelled`: rejects the request.

Unknown, expired, or already answered requests return `404`.

## Route-Scoped Sessions And Transcripts

Consumer-facing session APIs are exposed under each ACP route prefix:

```http
GET /<acp-route>/sessions?cwd=/tmp/acp-codex-test&cursor=...
GET /<acp-route>/sessions/{session_id}/transcript?cwd=/tmp/acp-codex-test
```

These endpoints use the matched route's VirtualKey policy and resolve the
target service from the route. They do not accept a caller-supplied
`service_id`. The optional `cwd` parameter is validated against the ACP
service's `allowed_roots`, matching the admin session and transcript behavior.

Error status codes are shared with the admin session and transcript endpoints:
`404` when the resolved service is not configured, `400` for a
client-correctable request (a disabled service, a `cwd` outside
`allowed_roots`, or a missing session id), and `502` for an upstream
agent/transport failure (capability not advertised, `initialize`/`session/load`
failure, or a dropped connection).

## Runtime State

`GET /admin/acp/runtime` returns:

```json
{
  "in_flight": [],
  "instances": [
    {
      "scope": "codex-main/t-demo-1/session-id",
      "session_id": "session-id",
      "alive": true,
      "active": false,
      "last_used": "2026-06-14T00:00:00Z",
      "idle_ttl": 0,
      "metadata": {
        "config_options": {},
        "available_commands": {},
        "session_info": {},
        "mode": {},
        "usage": {}
      }
    }
  ],
  "pending_permissions": []
}
```

`GET /admin/acp/runtime/inflight` returns an `items` array of active turns.

`DELETE /admin/acp/runtime/threads/{service_id}/{thread_id}` closes matching
pooled instances and returns:

```json
{ "closed": 1 }
```

## Session Listing And Transcript Replay

Admin session APIs remain service-scoped operator endpoints.

Session listing:

```http
GET /admin/acp/services/{id}/sessions?cwd=/tmp/acp-codex-test&cursor=...
```

Response:

```json
{
  "sessions": [
    {
      "session_id": "session-id",
      "cwd": "/tmp/acp-codex-test",
      "title": "Optional title",
      "updated_at": "2026-06-14T00:00:00Z",
      "_meta": {}
    }
  ],
  "next_cursor": ""
}
```

The runtime checks the agent's `session/list` capability before calling it.
`cwd` filtering is applied by the gateway after symlink-canonicalizing both
sides; it is not forwarded to the agent.

Transcript replay:

```http
GET /admin/acp/services/{id}/sessions/{session_id}/transcript?cwd=/tmp/acp-codex-test
```

Response:

```json
{
  "session_id": "session-id",
  "messages": [
    { "role": "user", "text": "hello" },
    { "role": "assistant", "text": "hi" }
  ]
}
```

Roles are `user`, `assistant`, or `reasoning`.
