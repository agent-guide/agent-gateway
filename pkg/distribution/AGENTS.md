# Distribution entry packages

The root rules apply. These packages own reusable command construction and
execution, while `cmd/agw`, `cmd/agwd`, and `cmd/agwctl` own edition-specific
imports. Read `pkg/gateway/AGENTS.md` before changing command behavior involving
routes, Bundle workflows or server adapters.

The designated reusable surfaces are:

- `distribution.Edition`, `Assembly` and `Assembly.Clone`, `Assembly.Registry`, `Assembly.Manifest`, `Assembly.AttributionPolicy`, `MetricsConfig`, `OTLPConfig`;
- `caddycmd.Command`, `New`, `Command.Assembly`, `Command.Manifest`, `Command.Run`;
- `daemoncmd.Options`, `ServerOptions`, `NewCommand`, `Run`;
- `ctlcmd.Options`, `NewCommand`, `Run`.

Their release protection follows `docs/design/enterprise-extension-contract.md`.
Candidate consumer evidence does not declare the repository or an unpublished
tag Enterprise-ready. Community metadata helpers and command annotations are
assembly implementation details. `Assembly.Factories` binds declared module IDs
to separately held callbacks; registries reject undeclared or nil factory entries.
Read `pkg/extension/AGENTS.md` before changing registration or config behavior.
Manifest queries never invoke factories or reconstruct a serving registry.

Constructors create independent flags and own copies of mutable options. Do not
load dotenv, install signals, or call `os.Exit` during import or construction.
Daemon/CLI `Run` return process exit codes; Caddy's process adapter delegates to
upstream `Main` including its exit behavior. Keep output on each Cobra tree's
writers and avoid package-global flags, output state or command pointers.

Public signatures and their required transitive types must not expose Community
`internal/` types. Public metrics values translate through the internal adapter.
Run `make test-extension-consumer` when changing this boundary; root `go test
./...` alone cannot discover the separately rooted consumer module.

`AttributeKeys`, `AttributionProducer` and `EventSinks` are explicit code-owned
runtime inputs, excluded from manifests. Clone owns mutable slices; callbacks
remain distribution-owned. Sink factories create fresh instances for each server
lifecycle, never share closed sinks across Caddy reloads. Sink owners must be
linked and unique, and transport options must pass assembly validation.
