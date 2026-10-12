# Build a Custom Distribution

Use a separate Go module that imports public Community packages. Keep explicit
module/provider/runtime imports in your three `package main` entries. The
[separate-module consumer](../../tests/extensionconsumer/) demonstrates a complete
assembly; its command imports are examples of the baseline linkage to retain.

## Assemble the commands

Supply one runtime `distribution.Assembly` to both server entries. Build the CLI
assembly as a validation-role projection: retain only modules that declare
`validation`, preserving their IDs, versions, schemas and dependencies, and set
each retained descriptor's roles to `["validation"]`. Keep the corresponding
local schema factories; omit runtime-only Prepare/Admin callbacks and sink
construction. The CLI must not advertise `runtime`, `admin` or `spi` roles.

Capability declarations must describe local validation too. For the Community
gateway descriptor, `distribution.CommunityValidation()` replaces the runtime
capabilities with `online.agentguide.community.bundle-validation`; use this
baseline rather than copying `CommunityRuntime()` unchanged. Apply the same
ownership rule to your modules' capability metadata. See the consumer's separate
`Runtime()` and `Validation()` assemblies for the exact projection.

```go
// Caddy main, after explicit linked-module imports:
func main() { caddycmd.New(runtimeAssembly()).Run() }

// Standalone main:
func main() {
    os.Exit(daemoncmd.Run(daemoncmd.Options{Assembly: runtimeAssembly()}))
}

// Management CLI main:
func main() {
    os.Exit(ctlcmd.Run(ctlcmd.Options{Assembly: validationAssembly()}))
}
```

The functions above are distribution-owned assembly builders. Declare linked
module descriptors and factories explicitly; use `Assembly.Registry()` to check
them. Metadata does not load code. Constructors keep mutable command flags local;
execution owns dotenv, signals and process exit behavior. Use the public
`MetricsConfig`/`OTLPConfig` values for metrics configuration.

## Register configuration and resources

A descriptor identifies the module, schema and roles. A separate factory owns
strict validation, secret field declarations, configured dependencies and
preparation. Runtime resources implement Health and Close. Validation callbacks
must not resolve secrets; preparation receives reference-preserving JSON,
resolved secrets separately and only declared dependency resources.

Configure a linked module through a Bundle envelope:

```yaml
extensions:
  - module_id: example.organization.module
    schema_version: 1
    config:
      message: hello
```

Replace the example schema with the linked factory's actual fields. Unknown
modules fail validation. Secret fields declared by that factory accept only
`{"$secret_ref":"env:NAME"}`; persisted and exported configuration retains the
token. Use `agwctl validate`, `apply` and `export` as described in the
[Bundle guide](bundle-yaml.md). Apply performs extension writes in dependency
order and reports partial results; it is not a whole-Bundle transaction.

Dynamic configuration uses `/admin/extensions/{module_id}`. Static envelopes
are startup-owned and read-only. Mutations prepare and persist before publishing
one generation. Request code holds a snapshot lease; resources honor
cancellation and retire in reverse dependency order. Preserve domain state
explicitly when unrelated writes replace prepared handles.

## Contribute Admin routes and request services

Declare `Factory.AdminRoutes` under `/admin/extensions/<module_id>/...` and
implement `AdminContributor` on the prepared resource. Module root CRUD and all
Community namespaces are reserved. Use the supplied dependencies and
`WriteAdminError`; retain the mount authentication and shared 4 MiB body boundary.
Request attribution and the module-scoped publisher are supplied by assembly.

To supply VirtualKey admission, implement `LimiterProvider` on a prepared module
resource and return its `LimiterBackend`. Configure that module and the
`online.agentguide.community.limiter` selection envelope together. See the
[limiter configuration example](../../README.md#limiter-backend-extension).
Registration alone does not install request interception; each runtime domain
needs its own injection contract.

## Add attribution and event delivery

Inject trusted attribution producers and exact namespaced attribute allowlists
through assembly. Raw caller headers do not establish trusted identity. Sink
registrations construct a fresh sink for every server lifecycle and receive only
sanitized typed operational events. Write/Flush/Close honor context cancellation;
delivery is bounded, asynchronous, best effort and has no retry or durable replay.

## Verify the distribution

Instantiate `pkg/extension/contracttest` (including its Attribution and EventSink
suites), `pkg/gateway/virtualkey/limiter/contracttest` and
`pkg/distribution/contracttest` with independent module implementations. The
integrated suite accepts three built binaries, runtime/validation assemblies,
envelopes, a read-only POST probe and a JSONL event-file variable. Check public
signature closure, private-import isolation and baseline module imports too.

Compare `agw manifest` and `agwd manifest`; inspect the CLI validation subset with
`agwctl manifest` and the server projection with `agwctl manifest --remote`.
Use the [development verification workflow](../development/README.md#external-distribution-contracts)
for exact-commit and published-tag checks. Release builds consume a pinned
published dependency without checked-in replacements or workspaces.
