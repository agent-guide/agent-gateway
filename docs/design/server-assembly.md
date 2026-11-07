# Server service assembly

Capability: Implemented for shared credentials, usage and host lifecycle.

Both Caddy and standalone call `internal/runtimeassembly`. Transport configuration,
provider type availability and static provider creation remain host responsibilities;
the protected distribution command signatures do not expose internal handles.

| Resource | Creation and owner | Retirement |
| --- | --- | --- |
| Configuration connection | Storage adapter, one handle per host lifecycle | Lifecycle closes last |
| Credential Manager and Scheduler | Shared constructor; host loads after static provisioning | Referenced only by that gateway lifecycle; no scheduler worker |
| Usage service and sinks | Shared constructor; fresh public sink factory calls per lifecycle | Lifecycle drains after runtime producers stop |
| Gateway and Agent runtime resources | Host creates Gateway; Bootstrap currently composes backends | Lifecycle calls Gateway.CloseContext first |
| Eino callbacks | Process-wide guarded registration | Stateless span tap registered once; exporter pointer replacement is fenced on exporter close |

The shared refresh-command normalization trims the executable, copies arguments,
and defaults to `agw-auth refresh` when omitted. Explicit arguments are retained.
The scheduler listener is installed before credentials load. Hosts retain a load
boundary because Caddy provisions static providers between construction and load.

Usage borrows the configuration connection; it never closes that connection.
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
