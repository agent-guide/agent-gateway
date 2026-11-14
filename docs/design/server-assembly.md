# Server service assembly

Capability: Implemented for shared credentials, usage, host lifecycle and Caddy distribution assembly handoff.

Both Caddy and standalone call `internal/serverassembly`. Transport configuration,
provider type availability and static provider creation remain host responsibilities;
the protected distribution command signatures do not expose internal handles.

The package also carries the process-scoped Caddy distribution assembly. The
Caddy command entry calls `InstallCaddyAssembly` to validate and store a defensive
copy; the Caddy app and manifest command read independent snapshots through
`CurrentCaddyAssembly`, which defaults to the Community assembly. Configuration
cannot install an assembly. Standalone and CLI pass their assembly explicitly.

| Resource | Creation and owner | Retirement |
| --- | --- | --- |
| Configuration connection | Storage adapter, one handle per host lifecycle | Lifecycle closes last |
| Credential Manager and Scheduler | Shared constructor; host loads after static provisioning | Referenced only by that gateway lifecycle; no scheduler worker |
| Usage service and sinks | Shared constructor; fresh public sink factory calls per lifecycle | Lifecycle drains after runtime producers stop |
| Gateway and Agent runtime resources | Gateway Bootstrap delegates to runtimebackend.Assemble | Lifecycle calls Gateway.CloseContext first; its owned runtimebackend.Set retires concrete managers |
| Eino callbacks | Process-wide guarded registration | Stateless span tap registered once; exporter pointer replacement is fenced on exporter close |

The shared refresh-command normalization trims the executable, copies arguments,
and defaults to `agw-auth refresh` when omitted. Explicit arguments are retained.
The scheduler listener is installed before credentials load. Hosts retain a load
boundary because Caddy provisions static providers between construction and load.

`serverassembly.UsageConfig` converts public `distribution.MetricsConfig` into
internal usage configuration and copies mutable OTLP headers. Both servers use
this conversion for validation and service wiring, keeping internal types out of
the public distribution interfaces.

Usage borrows the generic Database connection; it never closes that connection.
Its SQL implementation lives in internal/observability/persistence/sqlite, and
generic ConfigStore has no usage-schema or observability-type dependency.
SQLite initialization or migration failure logs a warning and returns an in-memory
event pipeline. This fallback has no historical query service, Prometheus sink or
OTLP exporter. Exporter initialization failure alone leaves persistence and
Prometheus available. Public sinks still use the distribution's validated factories
and bounded transport contract. No serving lifecycle shares a sink instance with
its replacement.

After HTTP admission stops, the common lifecycle retires Gateway resources, drains
usage, then closes the owned storage adapter. Construction failure uses the same
sequence for resources already acquired. SQLite creator Close is idempotent and
closes only its own connection, even when a replacement opens the same file.
The lifecycle shares one shutdown attempt with a ten-second budget; usage and
extension teardown retain their own shorter budgets. A concurrent caller can stop
waiting at its own deadline without replacing the first caller's common budget.
Runtime, pipeline and storage errors are joined and retained for repeated callers.
The legacy Gateway Reset/Close methods discard teardown errors; hosts use the
context-aware method to preserve them. Native subprocess transports are synchronously
killed during Gateway retirement. Interface implementers must honor their lifecycle
contracts; a timed-out callback is not proof that all external work has stopped.

The host owns the storage Close hook because Caddy child-module cleanup ordering
must not close the shared database before its parent drains usage. The Caddy
storage adapter forwards optional transaction capabilities as well as store lookup.
No second database or changes to persisted schema names are introduced.

Concrete adapter ownership is separate from common control ownership. Gateway
creates one RunRegistry, PermissionBroker and runtime Registry and injects them
into `runtimebackend.Assemble`. That assembly creates the ACP process manager,
builtin host, and HTTPRuntimeManager; registers the adapters; and attaches each
ACP/HTTP definition listener once. It reclaims managers if registration fails.
The builtin host receives explicit model, tool and usage capabilities, without a
reverse import of Gateway. HTTP's single manager also receives credential changes
and serves both native proxy lookup and common turns.

Gateway retirement first drains pending permissions and discards decided builtin
continuations, then closes extensions and the owned runtime Set. HTTP closure stops
Card retry timers and withdraws its published execution/proxy generation. A late
prepared commit observes the closure fence, retires unused clients and cannot
republish ready execution. Trusted client scopes include the runtime definition;
ACP owner proof retains independent LRU capacity and HTTP context/task claims
retain occupancy and expiration. Neither becomes a generic session store.
