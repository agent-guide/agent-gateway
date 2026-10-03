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
