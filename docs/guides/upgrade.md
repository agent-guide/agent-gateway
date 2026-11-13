# Upgrade guide

Read every section between the installed version and the target version. Back
up the SQLite store and retain the current binaries or image digest before
starting a new release.

## Upgrading to v0.7.3

v0.7.3 requires explicit repair of configurations that use any of these shapes:

- Provider-only LLM targets (`direct-provider`, `provider_target` or outer
  `target_policy.provider_id`). Register explicit ProviderModels and expose
  their route-visible names; conversion requires a chosen model allowlist.
- Agent `policy.budget` objects, including empty or all-zero objects. Budgets
  are not enforced; the field must be absent or null.
- Equal-score intersecting route matches across LLM, MCP and Agent routes,
  including disabled routes and static/dynamic ties. Static routes no longer
  take precedence. Native A2A matching covers every HTTP method.

These checks apply to persisted and static configuration. An invalid stored
record can prevent startup; Admin cannot repair a server that cannot start.
The historical `cliauth_credentials` table is unchanged.

1. Retain the existing binaries or image digest. Back up the SQLite store
   consistently and preserve the original static files before changing either.
2. From the v0.7.3 source checkout, run
   `scripts/repair-architecture-config --db /absolute/path/gateway.db`, adding
   `--static-config` for the complete static configuration. Inspection opens the
   database read-only and does not start the gateway. For Caddy, adapt the
   Caddyfile without provisioning and inspect the JSON with `--caddy-json`.
3. Follow the [offline recovery guide](architecture-config-recovery.md) to
   prepare explicit conversions, budget removals and route replacements or
   deletions. Stop all services using the files before applying repairs; supply
   an unused backup path and `--service-stopped`. Conflicting Provider targets
   require an explicit choice. Database and static-file repairs are separate
   operations, so validate the complete prospective configuration together.
4. Repeat inspection until it reports an empty array with exit code zero.
   Update Bundle sources and other configuration templates to the same shapes,
   then start v0.7.3 and verify the manifest version, health and expected routes.
5. If rollback is needed, stop v0.7.3 and restore the retained binaries or image
   together with the matching pre-upgrade database and static files. Restore
   SQLite as a standalone file without stale `-wal`/`-shm` companions; do not
   assume a binary-only rollback reverses configuration repairs.

Agent and Provider mutations whose persistence outcome cannot be confirmed now
block new execution and conflicting writes for that resource. Inspect
`GET /admin/agents/{id}/mutation` or
`GET /admin/llm/providers/{id}/mutation`; their matching `POST .../{id}/recover`
endpoints verify stored facts without replaying the write. See
[mutation recovery](configuration-mutation-recovery.md).

Provider deletion now rejects remaining Provider credentials as well as LLM
route references. Remove or reassign those dependencies explicitly before
retrying deletion; credentials are never cascade-deleted.

HTTP/ACP common-turn session ownership is scoped by route, VirtualKey, Agent
and runtime configuration; sharing a VirtualKey shares that client identity.
Clients must retain their own returned IDs. ACP ownership is process-local and
bounded; restart or eviction can require a new session. LLM ingress still
defaults to a 16 MiB JSON body limit; larger
requests require explicit Caddy `max_llm_request_body_bytes` or standalone
`--max-llm-request-body-bytes` configuration.

## Upgrading to v0.7.2

v0.7.2 adds no protocol or persisted-resource migration. It corrects metrics
aggregation and filtering, adds the unified interaction timeseries endpoint,
and updates Eino from v0.9.15 to v0.9.22.

Builtin Agent definitions that set
`runtime.builtin.middlewares.agentsmd.max_total_bytes` now receive a strict
cumulative content cap: a document that crosses the remaining budget is
truncated and later documents are omitted. Definitions that leave the value at
zero remain unlimited.

## Upgrading to v0.7.1

v0.7.1 changes distribution and onboarding, not gateway protocols or persisted
resource schemas. Verify the new archive with `SHA256SUMS`, run `manifest` on
all three binaries, and keep the runtime (`agw`, `agwd`) and validation
(`agwctl`) manifest projections distinct. Follow the
[release installation guide](install-release.md) for mounts, Admin auth, and
rollback preparation.

## Upgrading to v0.7.0

### Admin request boundaries

All Admin routes validate the complete received body before dispatch. A body
over 4 MiB returns bounded JSON `413`; compressed request bodies return `415`.
For Bundle apply, that HTTP boundary applies to each resource sent to the
server, not to the total local YAML file. Generic panic recovery returns bounded
JSON `500` without exposing arbitrary panic diagnostics.

### Extension configuration and consumer APIs

Internal extension configuration moved out of Caddyfile, Caddy JSON, and
standalone static Bundles. Put extension envelopes in a Bundle and apply them
through an authenticated Admin mount. Startup and reload do not import them
into ConfigStore.

The Caddyfile extension directive, Caddy app JSON `extensions` field, and static
bootstrap behavior were removed. External extension consumers must also account
for removal of `BootstrapOptions.StaticExtensions`, `Envelope.Source`, and
`GatewayBundle.ValidateForStaticConfigWithRegistry`; `extension.NewManager`
accepts only its current context, registry, and persistence dependencies.

### Secret references

Secret-bearing extension fields use declared reference objects in this exact
form:

```json
{"$secret_ref":"env:NAME"}
```

Literal secrets and undeclared reference objects fail validation. Local
validation checks syntax without reading the environment. Server preparation
resolves the value separately, while persistence and Bundle export retain the
reference token rather than the resolved secret.

### Limiter selection and generations

Limiter selection schema 1 chooses a linked, configured backend with
`backend_module_id` and `backend_type`. `timeout_ms` accepts 1–1000 and defaults
to 100; `failure_mode` defaults to `closed`. Exhaustion returns `429` with
`Retry-After`; backend failure or timeout returns a bounded `503`. Explicit
`open` mode admits degraded traffic, but cancellation, shutdown, and retired
policy errors never fail open. A consume is not retried because its outcome may
be ambiguous.

Policy or backend identity changes reset and fence the affected generation.
Unchanged dimensions preserve quota state, as do unrelated prepared-handle and
connection refreshes. Request admission always uses the current prepared
handle.

## Stores from before the v0.5.0 CLI-auth cutover

This older migration applies only when a store has not
completed the external CLI-auth cutover documented since v0.5.0. The historical
table name remains `cliauth_credentials`, but current credential and route
values use `oauth_token`.

Stop the gateway, take a consistent database backup, and inspect the affected
rows before applying:

```sql
UPDATE cliauth_credentials
SET data = json_set(data, '$.type', 'oauth_token')
WHERE json_extract(data, '$.type') = 'cliauth_token';

UPDATE routes
SET config = replace(config, '"cliauth_token"', '"oauth_token"')
WHERE config LIKE '%"cliauth_token"%';
```

Install the external refresh executable and configure
`credential_refresh_command` in Caddy mode, or
`--credential-refresh-command` plus repeatable
`--credential-refresh-arg` flags in standalone mode. Refresh is request-driven;
the first request after an idle period can wait up to 30 seconds. A failed
refresh rejects that credential for the current attempt.
