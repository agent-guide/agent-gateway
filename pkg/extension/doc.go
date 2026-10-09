// Package extension implements an explicit, immutable linked registry and
// reference-preserving module configuration. It never loads runtime code.
//
// The candidate protected value contracts are Descriptor, Error (Code, ModuleID,
// Field and Error), Manifest, Registration, Factory, Resource, Envelope,
// PreparedConfig, Snapshot, Lease, Persistence, Registry and Manager. Their
// exported fields and callback signatures form part of their contracts.
// The designated functions/methods are NewRegistry, NewManager;
// Registry.Manifest, Registry.Validate, Registry.ExpandConfig;
// Snapshot.Config, Snapshot.Resource, Snapshot.Context; Lease.Release;
// Manager.Acquire, Manager.List, Manager.Manifest, Manager.Put, Manager.Delete,
// Manager.Health, Manager.Close and Manager.SetPublisher. Other exported helpers are not designated.
// F5 designates StatefulResource and Snapshot.Sequence. Reconciliation only
// changes unpublished candidate identity; reset metadata is emitted after commit.
// F4 also designates AdminRoute, OwnedAdminRoute, AdminDependencies,
// AdminContributor, WriteAdminError, Registry.AdminRoutes, Manager.AdminRoutes and Manager.AdminPublisher.
// Factory.AdminRoutes is an immutable declaration set; only prepared resources
// handle requests, with leases and the same generation lifecycle.
// Release protection requires a published tag with the consumer contract gates
// described in docs/design/enterprise-extension-contract.md.
//
// A metadata-only registration has no Factory and cannot accept configuration.
// Validation-only factories may omit Prepare; a server rejects their envelopes.
// Validate and Dependencies receive a context and must perform strict module
// schema checks without resolving references or exposing arbitrary diagnostics.
// Prepare receives reference-preserving JSON, a separate resolved-secret map,
// and only declared dependency resources prepared for the candidate generation.
// Preparation never mutates the live generation. Its context has a five-second
// maximum; Snapshot.Context is the separate resource lifetime.
//
// Successful persistence precedes infallible publication. Requests retain their
// generation with Acquire/Release. Retirement closes resources in reverse order
// after drain or deadline. Close stops admissions and cancels ongoing preparation
// before waiting for a mutation. Already committed writes finalize and retire
// their generation without reopening admissions. Close shares its bounded deadline
// with all generations. Lifecycle errors and panics report bounded diagnostics.
// Callbacks must honor cancellation; a deadline cannot make invalid module code
// terminate. Such code is nonconforming and Close reports failure.
package extension
