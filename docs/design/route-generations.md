# Route representations and generations

Capability: Implemented.

`routecore.RouteConfig` is the single persisted envelope for every route kind.
Its wire fields, stored kinds, route IDs, URLs and VirtualKey allowlists are
independent of Go naming. `RouteConfigManager` owns management mutations and
publishes whole validated generations.

| Representation | Responsibility |
| --- | --- |
| RouteConfig | Persisted raw target envelope and common metadata |
| RouteMetadata | Target-free shared fields, timestamp initialization and defensive boundary copies |
| LLMRoute | Explicit concrete/logical model references; independent catalog owns scheduling |
| MCPRoute | Typed service ID |
| AgentRouteConfig | Typed Agent ID for both common and native A2A ingress |
| Private routeTable | Immutable common matching metadata, precomputed effective conditions/scores and ID lookup |

The typed forms serve both management and execution. There is no separate empty
runtime State or forwarding Config object. Raw target JSON is never embedded beside
a writable expanded target. Kind-specific target codecs and validation remain
separate; common metadata conversion and timestamps are shared without reflection.
Admin DTOs remain separate because they add source, read-only and runtime status.

Successful static initialization, Refresh and mutations compile a new match table
under the manager mutation lock, then replace its reference under a short lock.
Get and Match read the published table; List remains a management store query and
cannot publish records. Failed generation validation retains the previous table.
Startup explicitly loads the dynamic configuration before serving. Request matching
performs no store reads, copies no full map/slice and returns a defensive copy only
of the selected envelope. Disabled routes and effective A2A matching retain the
shared determinism contract, including ties returning errors.

Typed target compilation is lazy per content fingerprint in each kind's resolver.
Keeping that compilation at the kind layer avoids a routecore dependency on its
consumers. Full-field hashing handles custom stores with colliding timestamps and
caller-modified detached envelopes. Resolver-owned cached targets remain private;
returned metadata, policies and nested candidates are copied before normalization,
validation or execution. Cached compiled targets contain no external resources.
Manager mutations invalidate changed/deleted IDs in all attached kind caches,
including direct internal manager calls. Manager Reset also evicts old IDs.
Materializer epochs prevent late builds from undoing eviction or Reset. Callers
already holding a generation may finish against their captured copy.

Provider materialization remains separate: ProviderManager owns provider config
snapshots and the provider resolver's instance cache. Provider mutation invalidates
that ID, deletion retires configuration/instance/discovery state after verified
commit, and Reset discards the lifecycle's caches. Discovery epochs separately
fence in-flight discovery after deletion. Route type cleanup does not remove
provider instance lifecycle or native protocol state.

The ordered match table remains a linear scan. A specialized route index was not
needed to remove the measured per-request full-snapshot allocation. Benchmarks at
10/100/1000 routes compare the same last-route match; target hashing, cloning and
provider execution are outside that benchmark, so the results do not establish
end-to-end request latency improvements.

Representative Darwin/arm64 Apple M3 Pro measurements (three runs per size) for
10/100/1000 routes reduced median Match latency from 671/4950/56507 ns to
97/454/5506 ns. Allocations changed from 6/6/8 and 2600/24072/259504 bytes to
two allocations and 96 bytes at every size. These figures are a local comparison,
not a throughput guarantee or a measurement of typed/provider execution.
