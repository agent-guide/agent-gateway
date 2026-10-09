# Public event transport

Read design section 11.4 before changes. Only typed limiter admission and
extension mutation DTOs are permitted; no arbitrary maps, diagnostics, prompts,
configs, credentials or secrets. Validate before enqueue, including encoded size.
Delivery is transport owned by the existing pipeline, not another event authority.
One worker per sink, bounded event and barrier queues, nonblocking publication,
drop-newest overflow, no retry/replay. Callbacks are serial, panic-isolated and
receive cancellation. Shutdown cancellation reaches active Write/Flush; Close
runs once within the first caller's common deadline. Shorter concurrent waiters
cannot abort shared drain. Never hide a stuck sink with a goroutine per write.
Run event/pipeline race tests and public external sink conformance suites.
