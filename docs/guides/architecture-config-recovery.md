# Inspect and repair gateway configuration offline

Use the repository tool `scripts/repair-architecture-config` with the Go
toolchain to inspect an existing SQLite configuration database and the static
configuration that will be loaded with it. It does not start the gateway, call
Admin, perform Agent Card requests, or run schema migrations. Inspection opens
SQLite read-only and reports resource IDs, fields, and repair actions as JSON.
An empty array and exit code zero indicate a successful recheck.

```sh
scripts/repair-architecture-config --db /absolute/path/gateway.db \
  --static-config /absolute/path/gateway.static.yaml
```

Omit `--static-config` if there is no static configuration. Omit `--db` when
inspecting a standalone static file. Supply the complete configuration: repairs
check resource references against the union of both sources, including empty
families. Route and Agent IDs must be explicit. Use concrete values for fields
containing environment substitutions when preparing inspection inputs.
Static input is also checked against startup restrictions: LLM routes must expose
registered concrete or logical model references, and standalone static bundles cannot contain
`virtualKeys` or `extensions`. Database-backed logical-model
routes and those persisted resource families remain supported.

Agent `resources` declarations may retain deleted Provider, MCP connector, or
VirtualKey IDs. HTTP Agent `auth_ref` may likewise retain a revoked credential;
runtime readiness and execution enforce credential eligibility. These retained
links do not block offline repair. Agent route dependencies and ingress targets
must still exist and have the expected kind; builtin definition checks remain
in force.

For Caddy, adapt the Caddyfile without provisioning the server, then inspect
the resulting JSON:

```sh
./agw adapt --config /absolute/path/Caddyfile --adapter caddyfile \
  > /absolute/path/caddy.json
scripts/repair-architecture-config --db /absolute/path/gateway.db \
  --static-config /absolute/path/caddy.json --caddy-json
```

For a Caddyfile source, edit the reported route declarations explicitly and
repeat adaptation and inspection. The tool can repair the adapted JSON if
that JSON is the configuration you actually run. It does not rewrite the
Caddyfile from adapted JSON.

## Migrate ACP launchers

Export a repair batch from the SQLite database that holds the old Agent settings.

```sh
./scripts/repair-architecture-config --db ./data/configstore.db \
  --export-repairs ./repairs.json
```

Stop all services that use the database before applying the batch.

```sh
./scripts/repair-architecture-config --db ./data/configstore.db \
  --repair ./repairs.json --backup ./data/configstore.before-acp.db
./scripts/repair-architecture-config --db ./data/configstore.db
```

The tool derives launcher names and settings from the stored Agents. It keeps
the original executable, argument order and working-directory flag. Built-in
settings reuse `codex` and `opencode` when they match. The launcher definitions
and Agent references are committed in the same transaction.

SQLite `acp_launchers` is the sole configuration source for custom launchers.
Register a launcher with `--set-acp-launcher` before referencing it from an
Agent. SQLite backups contain both the definitions and Agent references.

Supply static input on export, apply, and recheck when the deployment uses
static configuration. For a legacy Agent that needs explicit changes beyond
launcher conversion, supply a complete `replace` operation with the intended
launcher reference when exporting. When the database still has the legacy MCP
service table, include `--convert-connectors` on export and apply. Update source
Bundles to use `runtime.acp.launcher`. Review
explicit `default_model` values before turning on Codex execution because model
configuration now uses the ACP model option it advertises.

## Generate a repair batch from SQLite

Run the exporter against the old database from the repository root. It opens
SQLite read-only, validates the prospective configuration in memory, and writes
the operations to a new file with owner-only permissions.

```sh
./scripts/repair-architecture-config --db /absolute/path/gateway.db \
  --export-repairs ./repairs.json
```

The exporter extracts stored inline logical models, removes unsupported Agent
budgets, and derives ACP launcher definitions and references from legacy Agent
settings. Review the generated operations before applying them with `--repair`.
The export already includes the ACP launcher and Agent operations derived from
SQLite, so apply it with the normal `--repair` command.

Include the deployment's `--static-config` on export, apply, and recheck when
applicable. For an old `mcp_services` table, include `--convert-connectors` on
export and apply. A successful export always creates the specified file. When
there are no record changes, the file contains `[]`. Applying an empty batch
performs the read-only recheck. If connector table conversion is the only
required change, apply the batch with `--convert-connectors` and `--backup`.

Some repairs need a deployment decision. For a legacy Provider-only route,
specify the allowed upstream models explicitly. Repeat `--allow-model` to expose
more than one model or to select models for other routes.

```sh
./scripts/repair-architecture-config --db /absolute/path/gateway.db \
  --export-repairs ./repairs.json --allow-model chat-prod=gpt-4.1
```

For other explicit choices, such as which competing route to keep, save the
chosen operations in `choices.json` using the format below. Rerun export with
`--repair ./choices.json` to include them in the validated batch. The tool
reports remaining issues and only writes a file after the full recheck passes.
Choose an unused output filename.

## Prepare explicit repair operations

Stop every service using the database or static file before repairing it.

Write a JSON array of operations. This example converts one direct target,
deletes an ambiguous route selected by the operator, and explicitly removes an
unenforced Agent budget:

```json
[
  {"family":"routes","id":"chat","action":"convert-target","data":{"models":[{"name":"chat","upstream_model":"gpt-4.1","capability_overrides":{"streaming":true,"tools":true}}]}},
  {"family":"routes","id":"duplicate-chat","action":"delete"},
  {"family":"agents","id":"assistant","action":"remove-budget"}
]
```

Supported families are `routes`, `agents`, and database-backed `connectors`,
`provider_models`, `logical_models`, and `acp_launchers`. ACP launcher operations
use `create` for new definitions and `replace` for existing definitions.
Use the legacy `mcp_services` family with `--convert-connectors` when repairing
rows during the stopped-service conversion described below. `replace` supplies
a complete raw record in `data`, with the same ID. Route replacement uses the persisted
envelope (`kind`, `protocol`, `match_policy`, `auth_policy`, and `target_policy`),
including `target_policy.connector_id` or `target_policy.agent_id` for those
route kinds. Omitted record fields are not merged with old values.

`convert-target` classifies the legacy Provider ID before normalization, rejecting
conflicting outer/inner IDs. Its `data.models` must explicitly enumerate allowed
route names and upstream model names. It never infers access from a Provider's
default or catalog discovery. Optional `data.default_model` names an exposed
entry; a single entry becomes the default automatically.

The converter creates missing ProviderModels and replaces the route with explicit
references in the same SQLite repair transaction. Existing concrete records are
reused without overwriting their facts. Supply `capability_overrides` in new model
entries or refresh discovery through Admin before requests needing those features.
Static repair likewise writes model declarations into the chosen static file.

Empty values, non-string IDs, and invalid target shapes fail conversion.
Conversions never pick a winner between conflicting providers or routes.
Budgets, including an empty object, require explicit removal or replacement.
One failing operation or any remaining reported hazard prevents the entire
batch from being committed. Deleting an Agent or route still referenced by
other resources also fails the full recheck.

## Apply and verify

Choose an unused backup filename; the tool never overwrites a backup.

```sh
scripts/repair-architecture-config --db /absolute/path/gateway.db \
  --repair /absolute/path/repairs.json --target db \
  --backup /absolute/path/gateway.before-repair.db
scripts/repair-architecture-config --db /absolute/path/gateway.db
```

Supply `--static-config` on both commands when the service also loads static
configuration. SQLite backup uses `VACUUM INTO`, including committed WAL
contents, with private file permissions. The repair transaction stages the
whole batch, validates references, writes, reads back, and rechecks before
commit. Failure rolls back the batch; its pre-mutation backup remains available.
Readback compares the complete stored configuration with the staged result,
including ProviderModels (identified by provider and upstream model) and extension
envelopes, so silently ignored writes and cross-table trigger changes roll back.
Extension checks validate stored envelopes without starting module factories or
resolving secrets.

To repair the static file separately, use `--target static` with
`--static-config`, `--repair`, and `--backup`. Supply `--db` when you need
read-only reference context. Static repair saves a private
backup and atomically replaces that file after rechecking the emitted bytes.
Database and static repairs are separate operations, not a shared transaction.
When both sources need repair, prepare and inspect repaired static input first
without changing the running service's input, then perform the stopped-service
repair with the complete prospective configuration.

After a clean inspection, restart the service with the repaired files and
verify its normal health and route behavior. To restore, stop the service,
restore the pre-repair database as a standalone SQLite file (do not retain
stale `-wal`/`-shm` companions), or restore the static backup, and inspect again
before restarting. Backups can contain credentials; retain them privately.

## Convert the legacy MCP service layout

Stop all processes using the SQLite file. Keep the backup private: legacy MCP
inline authentication and environment values may contain secrets. Run:

```sh
scripts/repair-architecture-config --db /path/gateway.db --convert-connectors \
  --backup /path/gateway.before-connectors.db
```

The conversion validates the prospective configuration, atomically renames the
physical table to `connectors`, nests MCP connection fields, and rewrites stored
Agent and MCP-route references and target discriminators. It preserves IDs,
metadata and inline auth/env/auto_auth without creating credentials. Failure
rolls back the complete conversion. A pre-existing target table is a conflict;
inspect and explicitly repair it before converting. Startup/Refresh never
perform this conversion or read old formats as aliases.

For damaged legacy rows, add `--repair /path/operations.json` to the same
conversion command. Explicit `replace`/`delete` operations address the old
`mcp_services` family; repairs, reference conversion, validation and table cutover
then share one transaction and backup. No partially repaired layout is published.

## Extract legacy inline logical models

The current model catalog uses `provider_models` for the concrete records already
stored in SQLite `model_configs`, plus a separate `logical_models` table. Current
Bundles use `providerModels` and `logicalModels`. Rename the former
`managedModels` Bundle field explicitly when editing your deployment files.

The exporter selects persisted routes with inline `model_targets[].candidates`.
Use the following operation to select one manually.

```json
[
  {"family":"routes","id":"chat-prod","action":"extract-models"}
]
```

Run the normal stopped-service repair command with this operation file, an unused
backup path, and all applicable static configuration. Extraction creates a stable
route-scoped LogicalModel ID for each old target, preserves route-visible names,
ProviderModel bindings, effective strategy and fallback, and replaces the route
policy with `type: models` references. The operation inserts independent models
and updates routes in one SQLite transaction, checks the complete graph, and
reads it back before commit. Missing concrete models or conflicting generated IDs
require explicit repairs. Inspection reports old inline fields without requiring
normal startup decoding. After extraction, restart and use Admin/Bundle to manage
or consolidate shared definitions. There is no automatic runtime migration.

To replace a damaged ProviderModel, use family `provider_models` with its compound
storage ID encoded as a JSON tuple string, for example `"id":"[\"p\",\"m\"]"`.
The replacement `data` must contain matching `provider_id` and `upstream_model`,
and an explicit `enabled` boolean. Stored records with omitted or null `enabled`
are rejected and reported by inspection; replace them with `enabled: true` to
retain an enabled legacy binding, or explicitly choose `false` to disable it.
Extraction does not guess the intended value.
LogicalModel replacement uses family `logical_models` and its ordinary `id`.
Both replacements participate in the normal backup, graph validation and readback
transaction; delete is available after releasing all references.

## Model route constraints

ProviderModel routes use one Provider and real upstream model names. Mixed target
kinds, concrete aliases and multiple direct Providers are rejected at startup and
on refresh/writes. `inspect` reports these policies without starting the host.
Use a `routes` `replace` operation to keep only one kind and one direct Provider;
set each concrete target name and `default_model` to the chosen upstream name.
If cross-provider selection or an alias is required, create a LogicalModel with
explicit candidates and replace the route with logical references. Alternatively
split routes across distinct paths. `convert-target` also requires each selected
model's `name` to equal `upstream_model`.
