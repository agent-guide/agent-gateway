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
