# Extension foundation

This package owns explicit linked metadata, registry validation, bounded module
configuration and prepared snapshot lifecycle. Read the root rules and design
section 11.2–11.3 before changing it. No dynamic code loading, entitlement state,
service locator, private implementation imports or request-time storage reads.

Factories are separate from serialized descriptors. A registry is immutable.
Metadata-only modules cannot accept envelopes. Validate is syntax/default/schema
validation without secret resolution. Factory secret fields use exact JSON
Pointers; duplicate JSON keys fail before module validation or expansion. Literal secrets and undeclared reference objects fail before ordinary
environment expansion. Factory diagnostics never become API errors.

Mutations serialize, prepare dependencies before dependents, persist, and publish
one complete generation. Preparation cannot mutate active resources. Snapshot
read handles are concurrency-safe; leases must be released after requests finish.
Close stops admissions and cancels mutations before waiting for their gate.
Already committed writes finalize into an immediately retiring generation.
Prepare/Health/Close honor cancellation; resources close in reverse dependency
order. New generations currently prepare every configured module, so callbacks
must not assume unrelated updates preserve resource identity. Shared request
policy/bucket preservation belongs to its domain owner, especially the F5 limiter.

Candidate surfaces are documented in doc.go and guarded by consumer contract
suites. Released-tag protection follows enterprise-extension-contract.md.
Run extension race tests, Bundle/SQLite tests and make test-extension-consumer.

PUT/DELETE emits sanitized public mutation events through the injected existing
pipeline publisher after commit, or a bounded reason on failure. Events contain
no module config, secret references/values or arbitrary callback diagnostics.
Committed mutation publication completes before releasing the mutation gate or
notifying shutdown waiters. The publisher only enqueues; no manager mutex is
held and no sink callback runs on this path.

Admin factories declare immutable method/path pairs with the `admin` role.
Only literal owner namespaces `/admin/extensions/<module_id>/...` are allowed;
module root CRUD and all Community prefixes (including the AgentRoute mux) are
reserved. Single-segment wildcards are supported; overlaps (including GET/HEAD)
fail registry assembly with `route_conflict`. Prepared resources must implement
AdminContributor. Handlers retain a generation lease, honor request/retirement
cancellation and use WriteAdminError. Dependencies contain owned reference config,
logger, attribution access and owner-scoped typed mutation events. Bind event
identity to the captured trusted request context, never a module-supplied Publish
context; the owner binder is resolved only during HTTP assembly. Never inject gateway,
raw store or resolved secrets. Read design section 11.5 and run Admin race and
external consumer/process contracts when changing this seam.

StatefulResource reconciles unpublished domain identity against the immutable
previous handle before persistence. Never mutate previous/live state. This is not
resource reuse: all handles still prepare/close once per generation. Removed or
changed state reports state_reset only after successful commit. Snapshot.Sequence
is a manager-local monotonic fence; failed candidates may leave gaps. Limiter
selection keeps unchanged policy identity across refreshes, including timeout or
failure-mode changes; backend authority/config changes reset identity.
