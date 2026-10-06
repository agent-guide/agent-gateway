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
modules, runs the normal and race test suites, runs `go vet`, and builds
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
