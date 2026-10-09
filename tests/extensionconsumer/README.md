# External distribution consumer

This is a separate Go module, not a package in the Community module. Its three
thin mains own their imports and construct public Caddy, daemon and CLI entries.
It declares fixture edition metadata and two independently owned modules with
strict validation schemas, secret-reference fields, dependency declarations and
prepared runtime resources. CLI factories contain only validation callbacks.

From the repository root, run:

```sh
make test-extension-consumer
```

The gate disables workspace resolution (`GOWORK=off`) and creates a temporary
modfile with a local replacement to the current Community checkout. It runs
external signature/assembly tests with the race detector, builds all three
binaries, checks Community dependencies for the known private module path, and
runs help, invalid-flag, dotenv and SIGINT/SIGTERM process contracts. It also
starts both server modes, checks manifest parity, static read-only envelopes,
dynamic PUT/apply/export/delete and token preservation without setting the
secret environment variable in the CLI process. The temporary
replacement and build outputs are removed on exit. Neither `go.work` nor a local
replacement belongs in this module's checked-in files.

The checked-in dependency declares the target Community release `v0.7.0`.
Until that tag is published, run the development gate above, which tests the
current checkout through a temporary replacement. The fixture cannot pin the
hash of the same commit that contains its go.mod. This is checkout-based
development evidence, not a released-tag or paired-private-repository gate.
F7 verifies the consumer against the published tag without a replacement.

`entry_test.go` walks the transitive public signature types, verifies defensive
metadata snapshots and the CLI validation projection, rejects direct imports
of Community `internal/` and `cmd/` packages, and verifies construction does not
load dotenv files. `extension_test.go` runs the public registry/configuration
contract suites, checks canonical manifest projections and factory ownership.
`events_test.go` runs public attribution and independent sink conformance suites,
including a stalled implementation that must fail. The process suite uses actual
consumer binaries and an independently implemented JSONL sink to prove mutation
success/failure, secret/config redaction and shutdown drain in both server modes.
Runtime factories also declare namespaced status/reset Admin routes, handled by
independently prepared resources through public AdminDependencies. The process
suite protects both modes with Basic Auth, rejects anonymous contribution calls,
and runs the reusable AdminBody suite against Community and contributed writes:
known-length/chunked oversize and compression fail before mutation, supported
writes retain shared CORS, and Bundle apply/export still work. It proves the
contributed reset publishes an owner-scoped public event. The alternate domain
limiter proof remains later work.
