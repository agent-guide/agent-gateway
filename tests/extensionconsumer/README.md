# External distribution consumer

This is a separate Go module, not a package in the Community module. Its three
thin mains own their imports and construct public Caddy, daemon and CLI entries.
It declares fixture edition metadata and an additional metadata descriptor;
actual module registration and extension behavior are added in F2–F6.

From the repository root, run:

```sh
make test-extension-consumer
```

The gate disables workspace resolution (`GOWORK=off`) and creates a temporary
modfile with a local replacement to the current Community checkout. It runs
external signature/assembly tests with the race detector, builds all three
binaries, checks Community dependencies for the known private module path, and
runs help, invalid-flag, dotenv and SIGINT/SIGTERM process contracts. The temporary
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
load dotenv files. Runtime manifest validation is an F2 contract, not inferred
from F1 metadata snapshots.
