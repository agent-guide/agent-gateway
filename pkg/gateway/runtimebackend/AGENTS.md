# Gateway runtime adapters

The root and parent Gateway rules apply. This package composes Agent contracts
with lower ACP, builtin and A2A runtimes. It must not import its parent Gateway
package; protocol packages must not import this package or Agent definitions.

`Assemble` owns the concrete ACP manager, builtin host, HTTP manager and their
definition listeners in one Set. Gateway owns the injected registries and drains
permission controls before closing that Set. Closed HTTP generations must reject
late prepared commits and stop retry scheduling.

ACP, builtin and HTTP adapters share injected RunRegistry, PermissionBroker and
CheckExecution admission. They do not create private copies of shared controls.
Agent definition listeners prepare off-lock, commit bounded in-memory changes,
and retire external resources in cleanup. Builtin definition commits claim only
old broker/decided continuations; delayed cleanup must preserve permissions from
new definitions. Registration uses the Host definition guard to prevent late
old turns from registering after retirement. HTTP common turns and native A2A proxy
share one HTTPRuntimeManager, Card generation and credential listener.

Trusted client scope includes Route, VirtualKey, Agent ID and runtime definition.
Do not derive ownership from caller-supplied session or permission identifiers.
ACP's process-local native ownership proof remains separate from HTTP context/task
claims and concurrent occupancy. Evicted or restart-lost ACP ownership fails
closed; HTTP claims preserve expiration and continuation rules. Capacity constants
are separately declared even while both currently equal 1024.

Preserve recursive builtin force/graceful cancellation, ACP permission continuation
and pagination, native HTTP proxy fidelity and definition-fingerprint retirement.
Read pkg/acp, pkg/agent/builtin and pkg/a2a AGENTS before changing behavior.
