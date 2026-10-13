# Development

This section is for contributor-facing workflow material.

Current development pages:

- [ai-assisted-refactor-collaboration-templates.md](ai-assisted-refactor-collaboration-templates.md)

## Release verification

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
