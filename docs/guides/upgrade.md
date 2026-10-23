# Upgrade guide

Read every section between the installed version and the target version. Back
up the SQLite store and retain the current binaries or image digest before
starting a new release.

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

This is not a v0.7.0, v0.7.1, or v0.7.2 migration. It applies only when a store has not
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
