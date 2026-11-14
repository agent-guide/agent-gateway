# Admin transport adapters

`client/` contains the Admin HTTP client (`package client`). Callers may import
it as `adminclient`. It shares server DTOs through explicit imports of `pkg/admin`;
keep server handlers independent of the client package.

Root rules and the affected subsystem AGENTS apply. Provider, VirtualKey,
credential, route, Agent and extension business invariants belong to their
application managers; every internal, HTTP and Bundle entry must share them.
Do not restore reference checking or multi-store mutations in HTTP handlers.

Routes() is only the endpoint registry. providers.go and virtualkeys.go own their
DTOs and HTTP adapters; metrics.go owns typed protocol query flows and
metrics_query.go owns common bounded parameters. Keep each endpoint's explicit
filter allowlist, typed result and error status. Common query/response helpers
must not turn different protocol contracts into untyped maps or arbitrary SQL
filters. The metrics summary's query error remains 500; detail query errors remain
400. Memory endpoints remain reserved 501 stubs in memory.go.

Preserve the shared full-body limit, compression rejection, authentication mount,
CORS, logging and JSON recovery boundaries. Pending mutation errors are projected
through controlplane.go with safe identities and bounded Retry-After metadata.

Model APIs use `/admin/llm/models/provider` for concrete compound identities and
`/admin/llm/models/logical` for independent logical IDs. Route model targets are
references only. Keep graph validation and deletion protection in composed managers,
including disabled references. ProviderModel and LogicalModel PUT identities must
match the path; no managed-model endpoint aliases remain.
