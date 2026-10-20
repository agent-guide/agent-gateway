# Contributing to Agent Gateway

Thank you for improving Agent Gateway. GitHub Issues is the project's single
public support and planning destination. Search existing issues before opening
a focused defect, documentation, provider-compatibility, or feature report.
Security vulnerabilities belong in a private report described in
[SECURITY.md](SECURITY.md), not a public issue.

## Before changing code

- Read the root `AGENTS.md` and the nearest nested `AGENTS.md` for every
  subsystem you touch.
- Open an issue before a broad protocol, schema, compatibility, or architecture
  change so its scope can be agreed independently of an implementation.
- Keep feature claims aligned across code, examples, architecture, and both
  language versions of the product website.
- Do not add compatibility aliases unless the change explicitly requires them.

## Develop and verify

The project requires the Go version declared in `go.mod`.

```sh
make build
go fmt ./...
go vet ./...
go test ./...
```

Run the nearest package tests while iterating. Before requesting review, run
the repository release gates appropriate to the change:

```sh
make verify-release
make smoke-release
```

Container, first-run, and observability changes also have targeted Make goals:

```sh
make smoke-container
make smoke-first-run
make smoke-observability
```

Some gates require Docker and take longer than unit tests. State exactly which
commands ran and explain any omitted environment-dependent gate in the pull
request.

## Pull requests

Keep commits reviewable and narrowly scoped. A pull request should describe the
operator-visible result, compatibility impact, tests, and documentation changes.
Include regression coverage for defects. Do not commit credentials, generated
databases, local environment files, or release build output.

By submitting a contribution, you agree that it is licensed under the
repository's Apache License 2.0.
