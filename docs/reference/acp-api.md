# ACP API Reference

All Admin API endpoints below are under the path where
`agent_gateway_admin` is mounted. They require
`Authorization: Bearer $TOKEN`.

Dispatcher endpoints are under the configured ACP route prefix and require a
VirtualKey when the route has `auth_policy.require_virtual_key: true`.

ACP dispatcher JSON request bodies are limited to 4 MiB.

## Dispatcher

### Start Or Continue A Turn

```http
POST /<acp-route>/turn
Content-Type: application/json
Accept: text/event-stream
Authorization: Bearer <virtual-key>
```

Request:

```json
{
  "thread_id": "t-demo-1",
  "session_id": "optional-session-id",
  "input": "Reply with exactly one word: pong",
  "cwd": "/tmp/acp-codex-test",
  "model": "optional-model",
  "fresh_session": false,
  "config_overrides": {}
}
```

The response is SSE:

```text
event: session
data: {"session_id":"..."}

event: delta
data: {"text":"pong"}

event: done
data: {"stop_reason":"end_turn"}
```

### Resolve Interactive Permission

```http
POST /<acp-route>/permission
Content-Type: application/json
```

Request:

```json
{
  "request_id": "perm-...",
  "outcome": "selected",
  "option_id": "allow_once"
}
```

Response:

```json
{ "status": "resolved" }
```

Use `outcome: "cancelled"` to deny the request.

### List Route Sessions

```http
GET /<acp-route>/sessions?cwd=/tmp/acp-codex-test&cursor=...
Authorization: Bearer <virtual-key>
```

Response:

```json
{
  "sessions": [
    {
      "session_id": "sess-...",
      "cwd": "/tmp/acp-codex-test",
      "title": "Fix ACP",
      "updated_at": "2026-06-09T10:11:12Z"
    }
  ],
  "next_cursor": "optional-cursor"
}
```

This is the consumer-facing form of session discovery. It resolves the ACP
service from the matched route instead of accepting a `service_id` in the URL.

### Replay Route Session Transcript

```http
GET /<acp-route>/sessions/{session_id}/transcript?cwd=/tmp/acp-codex-test
Authorization: Bearer <virtual-key>
```

Response:

```json
{
  "session_id": "sess-...",
  "messages": [
    { "role": "user", "text": "hello" },
    { "role": "assistant", "text": "pong" }
  ]
}
```

## ACP Services

### List Services

```http
GET /admin/acp/services
```

Response:

```json
{
  "items": [
    {
      "id": "codex-main",
      "name": "Codex",
      "agent_type": "codex",
      "cwd": "/tmp/acp-codex-test",
      "allowed_roots": ["/tmp/acp-codex-test"],
      "max_instances": 4,
      "permission_mode": "auto_approve",
      "source": "config_store",
      "read_only": false
    }
  ]
}
```

### Create Service

```http
POST /admin/acp/services
```

```json
{
  "id": "codex-main",
  "name": "Codex",
  "agent_type": "codex",
  "cwd": "/tmp/acp-codex-test",
  "env": { "CODEX_HOME": "/tmp/acp-codex-test/.codex" },
  "max_instances": 4,
  "permission_mode": "auto_approve"
}
```

Returns `201 Created` with the service view. `env` sets environment variables on
the spawned agent process (merged over the gateway's environment); use it to
give the CLI agent a per-service home directory.

### Get, Update, Delete Service

```http
GET /admin/acp/services/{id}
PUT /admin/acp/services/{id}
DELETE /admin/acp/services/{id}
```

Updating or deleting a service closes any pooled ACP agent processes for that
service; the next turn starts a process with the current service config.

Delete response:

```json
{ "status": "deleted" }
```

`created_at` and `updated_at` are server-managed and must be omitted from create
and update request bodies.

### List Agent Sessions

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
      "title": "Optional title"
    }
  ],
  "next_cursor": ""
}
```

### Replay Transcript

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

## ACP Routes

### List Routes

```http
GET /admin/acp/routes
```

Response:

```json
{
  "items": [
    {
      "id": "acp-codex",
      "kind": "acp",
      "protocol": "acp",
      "match_policy": { "path_prefix": "/acp/codex" },
      "auth_policy": { "require_virtual_key": true },
      "service_id": "codex-main",
      "source": "store",
      "read_only": false
    }
  ]
}
```

### Create Route

```http
POST /admin/acp/routes
```

```json
{
  "id": "acp-codex",
  "service_id": "codex-main",
  "match_policy": {
    "path_prefix": "/acp/codex"
  },
  "auth_policy": {
    "require_virtual_key": true
  }
}
```

Returns `201 Created` with the route view.

### Get, Update, Delete Route

```http
GET /admin/acp/routes/{id}
PUT /admin/acp/routes/{id}
DELETE /admin/acp/routes/{id}
```

`created_at` and `updated_at` are server-managed and must be omitted from create
and update request bodies.

## Runtime

### Overview

```http
GET /admin/acp/runtime
```

Response:

```json
{
  "in_flight": [],
  "instances": [],
  "pending_permissions": []
}
```

### In-Flight Turns

```http
GET /admin/acp/runtime/inflight
```

Response:

```json
{ "items": [] }
```

### Close Thread

```http
DELETE /admin/acp/runtime/threads/{service_id}/{thread_id}
```

Response:

```json
{ "closed": 1 }
```

### Resolve Permission As Operator

```http
POST /admin/acp/runtime/permissions/{request_id}
```

Request:

```json
{
  "request_id": "perm-...",
  "outcome": "selected",
  "option_id": "allow_once"
}
```

Response:

```json
{ "status": "resolved" }
```

## agwctl Commands

```bash
./agwctl gateway acp-service list
./agwctl gateway acp-service get <id>
./agwctl gateway acp-service delete <id>
./agwctl gateway acp-service sessions <id> [--cwd <cwd>] [--cursor <cursor>]
./agwctl gateway acp-service transcript <id> <session-id> [--cwd <cwd>]

./agwctl gateway acp-route list
./agwctl gateway acp-route get <id>
./agwctl gateway acp-route delete <id>

./agwctl gateway acp-runtime get
./agwctl gateway acp-runtime inflight
./agwctl gateway acp-runtime close-thread <service-id> <thread-id>
./agwctl gateway acp-runtime resolve-permission <request-id> --outcome selected --option-id <option-id>
```
