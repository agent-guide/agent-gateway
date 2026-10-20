# Evaluate a Codex or OpenCode ACP Agent

This optional evaluation runs an existing Codex or OpenCode ACP adapter behind
the governed AgentRoute surface. It covers a protected streaming turn, runtime
inspection, exact-run cancellation, conditional session replay, and interactive
permission handling. It is separate from the provider-credential-free first
run because the adapter and its upstream authentication are external.

## Choose and prepare one adapter

Before starting Agent Gateway, satisfy every prerequisite for the selected
runtime:

- Codex: install `codex-acp` (for example,
  `npm install -g @zed-industries/codex-acp`) and complete Codex authentication;
- OpenCode: install `opencode`, complete its authentication, and change
  `runtime.acp.agent_type` in the example Bundle to `opencode`;
- create `/tmp/agw-coding-agent`, or replace `cwd` and `allowed_roots` with an
  existing absolute workspace path the gateway process may access;
- decide whether the workspace is disposable and which file or command actions
  may be approved during the evaluation.

The adapter executable and its authentication must be visible to the `agw` or
`agwd` process. The official Community container deliberately does not install
these external tools, so use a platform archive or a separately maintained
custom image for this scenario. Claude Code is not an ACP backend; it consumes
the gateway through the `cc` Anthropic Messages profile.

## Start and configure the gateway

Start `agw` or `agwd` with the Agent dispatcher enabled, a writable SQLite
store, and authenticated Admin access as described in the
[ACP quick start](../getting-started/quickstart-acp.md). Then create the
workspace and apply the evaluation Bundle:

```sh
mkdir -p /tmp/agw-coding-agent
export AGW_ADMIN_BASIC_AUTH=admin:your-password

./agwctl validate -f ./examples/gateway.bundle.acp-evaluation.yaml
./agwctl apply -f ./examples/gateway.bundle.acp-evaluation.yaml
./agwctl agent get coding-agent
./agwctl agent-route get coding-agent-turn
./agwctl agent capabilities coding-agent
./agwctl acp-runtime get
```

Do not continue until `agent get` reports an executable runtime. A missing
adapter, unavailable workspace, or failed adapter authentication is a runtime
prerequisite failure rather than evidence about AgentRoute matching.

Retrieve the generated ingress credential:

```sh
CODING_AGENT_KEY=$(./agwctl --output json virtualkey get coding-agent-key \
  | jq -r '.key')
```

## Run a protected streaming turn

The example uses `permission_mode: interactive`, so choose a prompt that asks
before changing the workspace:

```sh
curl -N http://127.0.0.1:8080/agents/coding/turn \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $CODING_AGENT_KEY" \
  -d '{"input":"Inspect this workspace and propose one small change. Ask before writing files.","options":{"version":"v1","runtime":{"thread_id":"evaluation-1"}}}' \
  | tee /tmp/agw-coding-agent-turn.sse
```

The SSE objects carry `agent_id`, an exact `run_id`, and increasing `sequence`
values. A `session` event contains the `session_id` to use for continuation.

## Inspect and cancel an exact run

While a longer turn is active, inspect both the runtime-neutral run registry and
the ACP-specific diagnostic view from another terminal:

```sh
./agwctl agent runs coding-agent
./agwctl acp-runtime inflight
./agwctl agent cancel coding-agent <run-id> --mode force
./agwctl agent runs coding-agent
```

Cancellation is keyed by the exact `run_id`; thread-wide pool recovery is a
different operation. A `503` with `Retry-After: 1` means the live ACP session is
still binding, so retry the same exact-run cancellation rather than closing the
whole thread.

## Resolve an interactive permission

When the stream emits a `permission` event, inspect the bounded public request
and select one adapter-provided option ID:

```sh
./agwctl agent permissions coding-agent
./agwctl agent decide coding-agent <request-id> \
  --outcome selected --option-id <option-id>
```

Approve only the action you have reviewed. The gateway does not auto-approve a
timeout, a missing client, or an invalid option. To evaluate fail-closed mode,
change `permission_mode` to `deny`, reapply the Bundle, and repeat the turn.

## Inspect sessions and transcript when supported

Session listing and transcript replay are optional ACP capabilities. Check
`agent capabilities` first; when the selected adapter advertises them, run:

```sh
./agwctl agent sessions coding-agent --cwd /tmp/agw-coding-agent
./agwctl agent transcript coding-agent <session-id> \
  --cwd /tmp/agw-coding-agent
```

If a capability is absent, the expected result is a normalized unsupported
capability error; do not count that as a failed turn. Use
`agwctl acp-runtime close-thread coding-agent evaluation-1` only for explicit
pool recovery after the evaluation, not as a substitute for exact-run cancel.
