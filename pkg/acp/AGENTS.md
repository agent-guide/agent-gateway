# pkg/acp — AGENTS.md

Scope: native ACP runtime configuration and protocol integration. The root
`AGENTS.md` rules apply. Architecture, endpoint inventories, and implementation
status belong in `docs/architecture/acp-architecture.md`.

## Boundaries

- ACP is implemented natively in this repository; do not add a dependency on
  `github.com/beyond5959/ngent`.
- All deployment launchers use the same stdio ACP v1 client in
  `pkg/acp/client`. Require a JSON-RPC 2.0 envelope and ACP v1 handshake
  before any session operation. Shared update parsing belongs in
  `pkg/acp/session/acpupdate`.
- `pkg/acp/launcher` owns bundled per-agent launch presets and the persisted
  launcher record format. Launchers supply a trusted executable, arguments and
  optional cwd flag. `pkg/acp/session` applies the launcher to execution config.
  Both gateway hosts load the `acp_launchers` ConfigStore at startup and validate
  launcher references before Agent publication. Deployment operators manage
  the registry offline.
  Agent-owned settings supply cwd, environment and session options.
  Supply `agent_id` separately as the runtime owner key and keep Agent
  control-plane dependencies out of `pkg/acp`.
- Pooled instances record the config content fingerprint they were created
  under (`session.Config.Fingerprint` / the internal `configFingerprint`). A
  turn never reuses an instance whose fingerprint differs from the current
  config, and `Manager.RetireOwner(ownerID, keepFingerprint)` closes idle
  stale instances immediately while active ones drain their in-flight turn
  and are reaped by the janitor once idle. The manager also records the current
  accepted owner fingerprint and rechecks it before inserting a newly created
  process, so a pre-retirement request cannot repopulate the pool afterward.
  Never bypass the fingerprint check when adding pool creation, reuse, or
  adoption paths.

## Protocol invariants

- ACP permission handling is fail-closed. Interactive replies use the nested
  ACP outcome shape and preserve the exact option ids supplied by the agent;
  never reply with a flat `approved`/`declined` outcome.
- Do not add `model` or `modelId` to `session/new` or `session/prompt`. Apply
  model selection and other overrides through `session/set_config_option` using
  agent-advertised option IDs.
- Keep permission replies off the transport read loop. Missing streaming
  clients, timeouts, and transient session-list/transcript connections must
  not auto-approve requests.
- Continue draining prompt updates for the configured quiet grace period after
  the prompt result; real adapters may deliver final message chunks after the
  result.
- Keep raw wire session IDs and host ownership distinct. All launchers use
  the same ACP v1 session operations.
- Prompt and transcript subscribers must report buffer overflow as an error.
  The lifetime metadata cache uses a separate best-effort subscription. A
  prompt whose stream overflows must retire its process from reuse.

## Sessions and errors

- Check advertised ACP capabilities before calling session list or load.
  Cache handshake evidence for the current owner fingerprint and discard
  observations from retired configurations. Capability reads do not start
  subprocesses.
- Apply the optional session-list `cwd` filter in the gateway after
  symlink-canonicalizing both sides; never forward that filter to the agent.
- Preserve the shared HTTP error contract: missing Agent is `404`,
  client-correctable input represented by `acpsession.ErrInvalidRequest` is
  `400`, and agent/transport failure is `502`.
- Pool changes must preserve dead-instance eviction, idle cleanup, instance
  caps, and scope rebind: a session-addressed turn adopts the live instance
  already bound to that session rather than spawning a second process.
- Agent-bound turns register their exact common `run_id` in the native runtime.
  Force cancellation must send ACP `session/cancel` through that run's live
  instance and cancel only its context; never implement ordinary run cancel by
  closing an owner scope, thread, or pooled process. Before the live instance
  and protocol session id are bound, cancellation must return a retryable error
  and leave the run context alive; it must never silently skip the native frame.
  Agent deletion/runtime retirement is distinct from an operator exact-run
  cancel: it records a fail-closed cancellation request even in the pre-bind
  window, and native bind must send `session/cancel` before allowing the prompt
  loop to start. The common bulk-cancel path uses bounded retry for backends
  that cannot persist this pending state.
- ACP Agent permission decisions are claimed by the common Agent permission
  broker before the ACP waiter is resolved. The native waiter is continuation
  state only. Common records retain only an opaque continuation token; raw ACP
  permission params and the native request id remain in the ACP-owned store.
