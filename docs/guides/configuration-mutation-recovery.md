# Recover an unconfirmed configuration mutation

A write can commit even when its HTTP request returns an error. Agent and
Provider mutations expose an operation identity when authoritative verification
or publication cannot be confirmed. The resource blocks new execution and
conflicting writes with `503`; unrelated resources continue using their valid
definitions. Do not automatically repeat the original write.

## Inspect the operation

Use the usual authenticated Admin mount. With curl, `-u admin` prompts for the
password:

```sh
curl -u admin http://localhost:8019/admin/agents/assistant/mutation
curl -u admin http://localhost:8019/admin/llm/providers/main/mutation
```

The response contains the resource ID and `pending_mutation`, or null when
there is no outstanding operation in this process. Metadata contains
`operation_id`, `resource_id`, `action`, `state`, and `started_at`. A failed
recovery attempt also supplies `recovery_attempts` and `retry_after`.

`verifying` means a mutation is verifying or publishing its result.
`pending_recovery` means an attempt failed after its outcome became uncertain.
Agent GET/list and Provider list views include this metadata where the resource
is still present. The dedicated endpoint works even after a committed deletion
has removed the persisted record.

## Recover without repeating the write

```sh
curl -u admin -X POST http://localhost:8019/admin/agents/assistant/recover
curl -u admin -X POST http://localhost:8019/admin/llm/providers/main/recover
```

Recovery rereads persisted facts, verifies configuration/references, and
publishes through the usual runtime lifecycle. It does not repeat the original
Create, Update, or Delete. Successful recovery is idempotent and clears pending
state. Failed recovery retains the operation ID and applies exponential retry
delays of 1, 2, 4, 8, 16, then at most 30 seconds. Respect `Retry-After` or the
`retry_after` timestamp before another recovery request. No worker automatically
replays writes.

After recovery, GET the resource itself: an existing record contains the
authoritative configuration; `404` confirms absence after deletion. Previously
captured Agent definitions cannot start new turns after replacement. Existing
runs retain their normal cancellation and retirement rules.

## Transaction failure and restart

SQLite Agent and Provider creates/updates verify authoritative metadata in the
same transaction.
Known callback/readback failure rolls back and leaves valid execution available.
An uncertain commit enters pending recovery. Provider deletion verifies both
its removal and ManagedModel cleanup before the shared transaction commits.
Backends without transactional readback can report a pending Agent outcome
after a successful write whose verification failed. Provider store-write errors
also retain a recovery gate when a nontransactional backend cannot prove rollback.

Operation identities are process-local. Restart reconstructs definitions from
persisted configuration. A clear/null pending status after restart is not
evidence that an earlier write failed: inspect the resource before issuing a
different write. Startup that rejects invalid stored data requires
[offline recovery](architecture-config-recovery.md), which does not depend on
online Admin.
