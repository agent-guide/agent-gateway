# pkg/credential — AGENTS.md

Scope: the cross-cutting managed-credential domain. Paths are repository-root
relative; the root `AGENTS.md` rules apply.

This package owns credential models, persistence, selection state, expiry
detection, and the generic external refresh-command protocol. It is not an LLM
package because credentials may also serve MCP, ACP, and future upstream
resources.

Current credential types are `api_key` and `oauth_token`. OAuth credentials
carry access/refresh token material and request-time refresh metadata.

HTTP Agent execution reuses this store rather than creating a second secret
model. Its P0 upstream credentials use the dedicated non-provider scope
`http-agent:<normalized-agent-id>` and leave `provider_type` / `provider_id`
empty. `Credential.Validate` continues requiring both provider fields for
ordinary provider scopes and permits them to be empty only for this recognized
scope; generic unbound credentials remain invalid. Admin create/update must
accept the HTTP-Agent scope without looking for an LLM provider config, and
bundle/CLI surfaces retain the same shape. An HTTP Agent's `auth_ref` must
resolve to an enabled `api_key` or `oauth_token` with its exact scope and must
not borrow an LLM-provider or another Agent's credential. This identifies a
southbound credential owner; it does not create an Agent callback principal or
enforce Agent resources.

HTTP execution snapshots store only the credential id. Their live transport
calls `GetCredential` and, for OAuth, `RefreshCredentialIfNeeded` on every
southbound operation before attaching Bearer auth. Never bake a resolved access
token into a prepared client, send it on the public Agent Card fetch, or carry
it across redirects.

The design-only `HTTPRuntimeManager` registers a
`CredentialLifecycleListener` and maintains its dependency index from the
committed Agent generation. Credential callbacks must occur after store/state
mutation and must be safe for the listener to schedule (not synchronously
re-enter) one coalesced `AgentManager.Recommit` when a referenced credential's
eligibility changed. Unrelated ids and same-eligibility updates schedule
nothing. Only Agents whose `auth_ref` matches the changed credential are
affected. Its eligibility fingerprint is
secret-free: credential id, existence, type, normalized owner scope, disabled
state, and usable-secret presence (`strings.TrimSpace(APIKey()) != ""`; OAuth
expiry alone does not make it ineligible). Token bytes, expiry/refresh metadata,
and `UpdatedAt` are excluded, so secret/OAuth refresh rotation is observed by the
live transport without Card refetch or session-binding retirement. Changes to
existence/type/owner/disabled/usable-secret state recompute readiness from the
cached Card candidates and still fail closed at request time if a race occurs.

Provider-specific OAuth endpoints, client IDs, token exchange rules, retries,
interactive browser flows, and device flows belong to the independent
`agw-auth` project. Do not add provider-specific refresh implementations to
Agent Gateway.

Request-time refresh uses `metadata.expired` and the optional
`metadata.refresh_expiry_delta`; a missing, invalid, or zero delta uses the
30-second safety window. The gateway executes the configured executable
and its static arguments exactly as configured, writes one credential JSON
object to stdin, reads one updated credential JSON object from stdout, then
persists the result before continuing the upstream request. Provider-specific
metadata such as `refresh_name` is opaque to the gateway and is interpreted by
the external tool. Refresh output metadata is merged over the stored metadata
so omitted, unchanged opaque fields remain available for later refreshes.

Keep refresh-token rotation serialized so concurrent requests cannot reuse a
rotating refresh token. A refresh failure is memoized per credential for 30
seconds so queued and immediately subsequent requests fail over without
re-running the failing external command.
