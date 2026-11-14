# Development

This section is for contributor-facing workflow material.

Current development pages:

- [ai-assisted-refactor-collaboration-templates.md](ai-assisted-refactor-collaboration-templates.md)

## Release verification

Use two release forms: `vX.Y.Z-preview.N` for evaluation and `vX.Y.Z` for a
stable release. A stable version can follow any preview directly; alpha, beta
and rc stages are not required. Each tag runs the same automated release gates.

For each candidate, update the Community version in
`pkg/distribution/assembly.go` and its version test, the Makefile release
default and smoke-script defaults. Write `docs/releases/<exact-tag>.md` with
changes since the preceding published release, migration steps, known limits
and verification evidence; update the release index and remove those entries
from `unreleased.md`. Keep README language versions aligned.

Commit the candidate, run the checks below plus `make verify-adoption` and
`make verify-release-archive`, and validate the exact commit with the immutable
consumer gate. Push the version tag only when preparing to publish. The release
workflow validates the tag, checks manifests against it and requires matching
release notes. It publishes preview tags as GitHub prereleases with explicit
version/SHA image tags, without moving GitHub's latest release or either
container `latest`. Stable tags can advance `latest` after public verification.

Before tagging, also run `make verify-first-run-archive` with immutable
`GATEWAY_IMAGE_REF` and `FIXTURE_IMAGE_REF` values. Packaging pins those digests
even when the source walkthrough defaults to a different stable version.

Never move a tag that has become publicly downloadable. If the only failed
release step is first-run packaging, the Release workflow's manual dispatch can
resume a preview using the original run ID, tag and two verified image digests.
Run it from a revision containing the packaging fix. Recovery verifies the
original source/archive/image gates, retains the tagged runtime source and
artifacts, checks the public image identities, rebuilds only the first-run
archive, runs its full smoke, attests all assets and verifies public downloads
and first-run replay. It refuses stable tags, previously published releases,
other failed gates and changed source identities. It never promotes `latest`.

Run the same non-mutating release gates used by CI from a clean checkout:

```bash
make verify-release
```

This checks tracked Go files outside `third_party/` with `gofmt -l`, verifies
modules, runs the normal and race test suites, runs the independent extension
consumer race/build/process contracts, runs `go vet`, and builds
`agw`, `agwd`, and `agwctl`. The race suite temporarily excludes only
`pkg/llm/provider/anthropic` because the pinned `eino-ext/claude` dependency
has a known race in `ChatModel.Stream`.

To run only the non-mutating formatting gate:

```bash
./scripts/verify-release format
```

Run the process-level release smoke separately:

```bash
make smoke-release
```

The smoke builds all three shipped binaries, starts the local A2A fixture and
gateway on ephemeral loopback ports, applies an HTTP Agent through `agwctl`,
exercises common `/turn` plus native A2A Card, JSON, and SSE traffic, waits for
the matching persisted interaction, and then checks standalone `agwd`
readiness. Child processes and temporary SQLite files are cleaned up on every
exit path.


## External distribution contracts

```sh
make test-extension-consumer
make test-extension-consumer-candidate COMMUNITY_COMMIT=<exact-community-commit>
# After publishing the dependency tag declared by the consumer:
make test-extension-consumer-release
```

The first command checks the working checkout using a temporary modfile. The
candidate command archives the exact Git commit, so local edits cannot change
its evidence. Release mode downloads the consumer's pinned tag with fresh
module/build caches, disables workspaces and rejects checked-in replacements.
Candidate proof never satisfies the published-tag gate.

A separately maintained repository can instantiate public
`pkg/extension/contracttest`, `pkg/gateway/virtualkey/limiter/contracttest` and
`pkg/distribution/contracttest`. The integrated process suite takes external
assemblies, envelopes, command paths, a read-only POST probe and a JSONL sink
path variable. It runs both server modes, CLI configuration round trips, shared
Admin authentication/body policy, selected-backend admission, OpenAI/Anthropic/cc
batch/SSE, upstream cancellation, MCP tool execution, persisted LLM/MCP usage,
sanitized events and shutdown. The process helpers are test infrastructure,
not an additional production compatibility promise.

`AGW_RELEASE_BIN_DIR` lets the existing Agent/A2A release smoke exercise built
external entries named `agw`, `agwd` and `agwctl`, retaining the same harness.

## Extension foundation release closure

The [v0.7 release notes](../releases/v0.7.0.md) enumerate the exact designated
production surfaces. Verify both Community gates on the clean candidate, then
run the separate consumer's immutable candidate gate and the paired repository
candidate gate against that full commit. Publish Community before resolving the
released dependency in either consumer. Record the fresh-cache release results
and checksums; only then retire the paired execution plans and their indexes.
Local tags, temporary replacements and successful candidate builds do not prove
that the released dependency is downloadable.
