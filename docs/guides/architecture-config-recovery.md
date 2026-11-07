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
Static input is also checked against startup restrictions: LLM routes must use
direct Provider targets, and standalone static bundles cannot contain
`virtualKeys`, `managedModels`, or `extensions`. Database-backed logical-model
routes and those persisted resource families remain supported.

Agent `resources` declarations may retain deleted Provider, MCP service, or
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

## Prepare an explicit batch

Stop every service using the database or static file before repairing it.
`--service-stopped` records this operational prerequisite; it is not a lock or
a guarantee of coordination between processes.

Write a JSON array of operations. This example converts one direct target,
deletes an ambiguous route selected by the operator, and explicitly removes an
unenforced Agent budget:

```json
[
  {"family":"routes","id":"chat","action":"convert-target"},
  {"family":"routes","id":"duplicate-chat","action":"delete"},
  {"family":"agents","id":"assistant","action":"remove-budget"}
]
```

Supported families are `routes` and `agents`. `replace` supplies a complete
raw record in `data`, with the same ID. Route replacement uses the persisted
envelope (`kind`, `protocol`, `match_policy`, `auth_policy`, and `target_policy`),
including `target_policy.service_id` or `target_policy.agent_id` for those
route kinds. Omitted record fields are not merged with old values.

`convert-target` classifies the original raw fields before normalization:

| Existing direct target | Result |
| --- | --- |
| Outer `provider_id` only | Move to `provider_target.provider_id` |
| Inner value only | Keep inner value and remove an empty outer field |
| Both nonempty, equal after trimming | Keep one canonical inner value |
| Both nonempty and different | Fail; replace with an explicitly chosen target |

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
  --backup /absolute/path/gateway.before-repair.db --service-stopped
scripts/repair-architecture-config --db /absolute/path/gateway.db
```

Supply `--static-config` on both commands when the service also loads static
configuration. SQLite backup uses `VACUUM INTO`, including committed WAL
contents, with private file permissions. The repair transaction stages the
whole batch, validates references, writes, reads back, and rechecks before
commit. Failure rolls back the batch; its pre-mutation backup remains available.
Readback compares the complete stored configuration with the staged result,
including ManagedModels (identified by provider and upstream model) and extension
envelopes, so silently ignored writes and cross-table trigger changes roll back.
Extension checks validate stored envelopes without starting module factories or
resolving secrets.

To repair the static file separately, use `--target static` with
`--static-config`, `--repair`, `--backup`, and `--service-stopped`; optionally
supply `--db` as read-only reference context. Static repair saves a private
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
