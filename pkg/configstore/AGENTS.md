# pkg/configstore — AGENTS.md

Scope: the generic config store and its backends. Paths are repository-root
relative; the root `AGENTS.md` global rules apply. Broader context:
`docs/architecture/configstore-architecture.md`.

Important packages:

- `pkg/configstore/`: generic store/backend interfaces, shared schema primitives, backend factory, and backend registration
- `pkg/configstore/schema/`: store names and built-in business schemas for persisted config object families
- `pkg/configstore/sqlite/`: SQLite JSON backend implementation
- `caddy/configstore/sqlite/`: SQLite backend Caddy adapter

The top-level storage interface is `ConfigStoreBackend`, which registers and returns schema-bound generic stores:

- `Register(name string, schema StoreSchema) error`
- `Get(name string) (ConfigStore, error)`

Current store names:

- `providers`
- `credentials`
- `routes`
- `connectors`
- `agents`
- `virtual_keys`
- `managed_models`
- `extensions` — reference-preserving module envelopes; the sole source of internal extension configuration

Current persisted backend:

- `sqlite`

The host lifecycle owns the optional backend Close hook. SQLite creators close
their own connection once, after all configuration/usage borrowers stop. Caddy
adapters forward Close and transactions; child-module cleanup must not close
the shared connection before the parent drains usage.

`TransactionalBackend` is optional; existing ConfigStore/Backend interfaces do
not change. `Backend.Transaction` binds requested registered schemas through a
`TransactionalCreator`. SQLite supplies stores on one transaction without
migrations. Callback failure rolls back all writes. Publish runtime snapshots
only after commit, never retain transaction stores, and never substitute
sequential writes when transactions are unsupported.

`TransactionalStore` supplies the corresponding single-family callback for
authoritative write/readback (notably Agents). A successful callback and a
successful transaction return are separate facts; consumers treat uncertain
commit errors as pending recovery rather than blindly replaying writes.

`internal/configrepair` and `scripts/repair-architecture-config` provide offline
raw-record inspection and repair without schema registration or data-plane
startup. Repairs require stopped services, back up committed SQLite contents,
validate the prospective full configuration and references, and perform one
transaction with readback before commit. Do not turn this recovery-only reader
into a runtime dual-read compatibility path.

The optional Database() handle is generic borrowed SQL access. Usage-specific
migrations/writes/queries/cleanup live in internal/observability/persistence/sqlite.
Do not import observability types or reintroduce a UsageDB capability here.
