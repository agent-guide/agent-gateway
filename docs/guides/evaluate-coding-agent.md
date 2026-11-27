# Evaluate a Codex or OpenCode ACP Agent

This optional evaluation runs an existing Codex or OpenCode ACP adapter behind
the governed AgentRoute surface. It covers a protected streaming turn, runtime
inspection, exact-run cancellation, conditional session replay, and interactive
permission handling. Set up the workspace and authenticate the coding agent
before sending a turn.

## Choose and prepare one adapter

- Codex: use the adapter bundled in the repository container build, or run
  `npm install -g @agentclientprotocol/codex-acp` for a binary deployment.
  Complete Codex authentication as the user running the gateway.
- OpenCode: use the `opencode` binary bundled in the repository container
  build, or run `npm install -g opencode-ai@1.19.6` for a binary deployment.
  Use the OpenCode runtime and environment from the per-Agent example below.
- Create the workspace and its `home` subdirectory for the selected Agent.
- Decide whether the workspace is disposable and which file or command actions
  may be approved during the evaluation.

The adapter executable and its authentication must be visible to the `agw` or
`agwd` process. Claude Code consumes the gateway through the `cc` Anthropic
Messages profile.

## Give each Agent its own workspace and login

Mount `/workspaces` in the gateway container. The
[ACP Bundle example](../../examples/gateway.bundle.acp.yaml) uses
`/workspaces/<agent-id>` as both the working directory and allowed root.
A workspace can be any directory. Set `runtime.acp.env.HOME` to its `home`
subdirectory so each Agent has a place to persist its own account and state.
Create the directories with access for UID/GID 10001.

The gateway and its ACP subprocesses share UID 10001 and its filesystem
access. An adapter may be able to read credentials under its own or another
Agent's `home`. Give route keys only to callers trusted with that data, and use
separate gateway deployments and volumes for separate trust boundaries. If
the workspace is a Git checkout, exclude `/home/` from version control using
`.git/info/exclude` before signing in.

For the login commands below, first start the
[Podman deployment](#use-the-gateway-container-with-podman).

For Codex, place user configuration in
`/workspaces/codex-main/home/.codex/config.toml`. Sign in from the running
gateway container with the same `HOME` and `CODEX_HOME` as the Agent:

```sh
podman exec agent-gateway install -d -m 0700 /workspaces/codex-main/home/.codex
podman exec -it \
  -e HOME=/workspaces/codex-main/home \
  -e CODEX_HOME=/workspaces/codex-main/home/.codex \
  -w /workspaces/codex-main agent-gateway codex-acp cli login --device-auth
```

For OpenCode, place user configuration in
`/workspaces/opencode-main/home/.config/opencode/opencode.json`. Use the
Agent's `HOME` and XDG directories for login:

```sh
podman exec agent-gateway install -d -m 0700 /workspaces/opencode-main/home
podman exec -it \
  -e HOME=/workspaces/opencode-main/home \
  -e XDG_CONFIG_HOME=/workspaces/opencode-main/home/.config \
  -e XDG_DATA_HOME=/workspaces/opencode-main/home/.local/share \
  -e XDG_STATE_HOME=/workspaces/opencode-main/home/.local/state \
  -e XDG_CACHE_HOME=/workspaces/opencode-main/home/.cache \
  -w /workspaces/opencode-main agent-gateway opencode auth login
```

OpenCode V1 stores its credentials in `opencode/auth.json` under its XDG
data directory. See the OpenCode V1 docs for
[configuration](https://opencode.ai/v1/docs/config/#global) and
[provider login](https://opencode.ai/v1/docs/cli/#login).

## Use the gateway container with Podman

Build the image from the repository root:

```sh
podman build -f Dockerfile -t localhost/agent-gateway:acp .
```

Use the new image in your Podman deployment. Reuse the SQLite volume and
authenticated Admin configuration. Replace `agent-gateway` in the `podman exec`
commands with your container name. Add a new writable named volume for the
workspaces and their home directories using this Podman run argument:

```text
-v agw-acp-workspaces:/workspaces:U
```

For a bind mount, grant the container's UID/GID 10001 access and use the
container paths in the Bundle.

For Codex shell tools, the Linux host and container security profile must
allow the sandbox to create user namespaces. Check the running container
before evaluating tool calls:

```sh
podman exec agent-gateway codex-acp cli sandbox linux -- /bin/true
```

If this reports a `bwrap` namespace error, check the host and container
sandbox configuration. See the
[Codex Linux sandbox prerequisites](https://learn.chatgpt.com/docs/sandboxing#prerequisites).

For the evaluation Bundle's default Codex runtime, sign in with a device code
and check the login state:

```sh
podman exec agent-gateway install -d -m 0700 /workspaces/coding-agent/home/.codex
podman exec -it -e HOME=/workspaces/coding-agent/home \
  -e CODEX_HOME=/workspaces/coding-agent/home/.codex \
  -w /workspaces/coding-agent agent-gateway codex-acp cli login --device-auth
podman exec -e HOME=/workspaces/coding-agent/home \
  -e CODEX_HOME=/workspaces/coding-agent/home/.codex \
  agent-gateway codex-acp cli login status
```

Enable device-code login in the ChatGPT account or workspace if required.
See [Codex authentication](https://learn.chatgpt.com/docs/auth#login-on-headless-devices).

To evaluate OpenCode instead, set `agent_type: opencode` in the evaluation
Bundle and replace its `env` with the OpenCode environment from the ACP Bundle
example, using `/workspaces/coding-agent` as the workspace in the environment
and login command.

With `AGW_ADMIN_BASIC_AUTH` exported on the host, apply the Bundle through
the container's Admin listener:

```sh
podman exec -i -e AGW_ADMIN_BASIC_AUTH agent-gateway \
  agwctl --admin-addr http://127.0.0.1:8019 apply -f /dev/stdin \
  < examples/gateway.bundle.acp-evaluation.yaml
```

Continue with [Inspect the Agent and retrieve its key](#inspect-the-agent-and-retrieve-its-key).

## Use the gateway binaries

Start `agw` or `agwd` with the Agent dispatcher enabled, a writable SQLite
store, and authenticated Admin access as described in the
[ACP quick start](../getting-started/quickstart-acp.md). If your host uses a
different writable root, replace `/workspaces/coding-agent` in the Bundle and
commands with that Agent's absolute workspace path. Create the workspace and
sign in with the environment in the Bundle. The example below uses Codex. For
OpenCode, use the OpenCode Bundle settings described above and run
`opencode auth login` with its `HOME` and XDG directories.

```sh
install -d -m 0700 /workspaces/coding-agent/home/.codex
HOME=/workspaces/coding-agent/home CODEX_HOME=/workspaces/coding-agent/home/.codex \
  codex-acp cli login
export AGW_ADMIN_BASIC_AUTH=admin:your-password

./agwctl validate -f ./examples/gateway.bundle.acp-evaluation.yaml
./agwctl apply -f ./examples/gateway.bundle.acp-evaluation.yaml
```

## Inspect the Agent and retrieve its key

The remaining commands use `./agwctl` for a binary deployment. For Podman,
replace it with `podman exec -e AGW_ADMIN_BASIC_AUTH agent-gateway agwctl --admin-addr http://127.0.0.1:8019`.
Send the turn from the host to the published data port.

```sh
./agwctl agent get coding-agent
./agwctl agent-route get coding-agent-turn
./agwctl agent capabilities coding-agent
./agwctl acp-runtime get
```

Check that `agent get` shows the intended adapter and workspace. A successful
first turn verifies that the adapter can start and authenticate upstream.

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
./agwctl agent sessions coding-agent --cwd /workspaces/coding-agent
./agwctl agent transcript coding-agent <session-id> \
  --cwd /workspaces/coding-agent
```

If a capability is absent, the expected result is a normalized unsupported
capability error; do not count that as a failed turn. Use
`agwctl acp-runtime close-thread coding-agent evaluation-1` only for explicit
pool recovery after the evaluation, not as a substitute for exact-run cancel.
