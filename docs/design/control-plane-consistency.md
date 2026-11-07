# Control-plane reference consistency

Capability status: **Implemented** for reference coordination and transactional
Provider/ManagedModel deletion. The pending-recovery behavior specified under
"Pending generation recovery" remains **Proposed** until its implementation
and fault-injection gates land.

## References and deletion

| Relationship | Strength and mutation policy |
| --- | --- |
| LLM Route → Provider, including logical candidates | Strong. A write requires every referenced Provider; any static, dynamic or disabled reference blocks Provider deletion. References do not imply exclusive ownership. |
| ManagedModel → Provider | Owned configuration. Creation/update requires the Provider. Provider deletion atomically removes its overlays in the same SQLite transaction. Discovery cache retirement follows commit. |
| Provider credential → Provider | Strong ownership metadata. Credentials are never cascade-deleted. Provider deletion is blocked until they are explicitly removed/rebound. Provider type changes cannot strand mismatched credential ownership. Credential writes and Provider mutation share coordination. |
| Agent.Resources → Provider / MCP service / VirtualKey | Management declarations, not cross-runtime authorization. Retain declarations when those resources are removed; Admin inventory reports `exists=false`. Recreating the same ID reconnects that declaration, without granting access. Builtin definition checks remain in force. |
| Agent.Routes → LLM/MCP Route | Strong existence/kind dependency. Deletion is blocked while any Agent references the route. LLM dependencies remain shared; MCP exclusivity is unchanged by this contract. |
| AgentRoute → Agent | Strong existence dependency. Create/update and Agent deletion are serialized. A disabled/non-executable Agent can still be targeted by `protocol=agent`; native A2A keeps its additional readiness requirement. |
| HTTP Agent auth_ref → credential | Revocable execution dependency. Credential deletion/disablement is allowed and must fail closed through live credential lookup and existing readiness reconciliation. Retain the reference for operator repair; reappearance requires exact Agent ownership before execution resumes. |

Disabled resources retain their references. Static configuration participates
in checks and is read-only. No deletion silently removes an Agent, its declared
resources, bearer keys or upstream secrets. Ordinary configuration references
do not grant authorization or establish trusted usage identity.

## Service ownership and lock order

One process-local application coordinator owns cross-resource operations. HTTP
handlers parse, invoke manager/application operations, and map errors. Composed
managers apply the same guards to direct callers and Bundle application; routing
through Admin cannot be the only protection. The coordinator composes existing
managers rather than replacing their persistence and lifecycle responsibilities.

The fixed acquisition order is:

1. Agent mutation lock, when the operation changes Agent definitions or uses
   an existing Agent guarded-deletion callback.
2. Application coordinator lock.
3. Route mutation lock, when changing a route.
4. Short-lived manager/cache locks and SQLite transaction work.

An operation holding the coordinator must never acquire the Agent mutation
lock. Agent mutation enters coordination after acquiring its existing write
lock; route guards inspect immutable Agent snapshots without reentering it.
`DeleteUnreferencedLLMRoute` and its MCP counterpart may invoke route deletion
while retaining the Agent lock, which then enters the coordinator and route
lock in this order. No callback reacquires the Agent mutation lock. Credential
listeners schedule reconciliation asynchronously after their own state locks;
they must not synchronously wait for Agent mutation under coordination.

Provider/Route, Provider/ManagedModel and Provider/credential mutations use the
same coordinator. All reference checks occur inside its operation window, and
the matching route check remains inside the route manager's publication window.
Requests continue to read published snapshots without the coordinator or store.
Bounded Agent listener preparation can hold the configuration coordinator but
does not hold snapshot write locks; listener commit remains bounded in-memory
work, followed by detached cleanup. No network/process work belongs inside a
SQLite transaction.

## Persistence and publication

An optional generic transaction capability sits alongside existing ConfigStore
interfaces. It supplies schema-bound stores for a fixed set of already
registered schemas, with no domain SQL and no schema migrations inside the
transaction. Unsupported backends fail atomic operations explicitly; they must
not fall back to sequential deletion. Keep protected existing interfaces and
distribution entry signatures unchanged.

Provider deletion checks references, deletes the Provider and its complete
ManagedModel set in one transaction, then invalidates configuration/runtime and
discovery caches. A failed statement/readback rolls back the complete operation.
Discovery invalidation fences in-flight refresh so it cannot republish a retired
Provider generation. Credential deletion publishes memory removal and lifecycle
notifications only after persistence succeeds.
Do not implement rollback by compensating writes after commit.

## Pending generation recovery

The following behavior remains Proposed. It is not supplied by the transaction
or coordination implementation above.

Agent authoritative timestamp readback should run inside the persistence
transaction when supported. A committed write and runtime publication are
distinct facts. A post-commit readback/preparation/publication failure records
an operation/resource-specific pending recovery state and returns an identifiable
error. It retains the previous validated generation but blocks new execution
and conflicting writes for the affected resource. Unrelated resources continue.
Expose only operation identity, resource identity, action and bounded recovery
state; never configuration, secrets or raw listener diagnostics.

Recovery rereads persisted facts, validates and prepares a complete generation,
then publishes through normal listener commit/cleanup. Controlled Refresh/retry
clears the pending state only after success. Repeating a failed mutation must
recover its committed outcome before creating another write. Existing runs keep
their established cancellation/retirement lifecycle; the gate applies to new
execution, including callers holding a previously resolved definition.

Restart reconstructs published generations from SQLite and rejects invalid
configuration through offline recovery guidance. An in-memory pending registry
does not imply durable business workflow or multiprocess coordination. Two
processes modifying one SQLite file remain outside the consistency guarantee.

## Acceptance evidence

Implementation must cover concurrent deletion/reference creation, complete
rollback after any cleanup failure, transaction readback failure, committed
publication failure, observable pending state, blocked stale execution, caller
retry and restart. Existing Agent reference, credential revocation, session,
permission and cancellation behavior suites remain required. Documentation-only
agreement with this contract is not implemented consistency.
