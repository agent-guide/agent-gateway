# ConfigStore Refactor Design

## 1. Scope

This document defines the recommended refactor for `pkg/configstore/`.

It describes:

- the target architecture
- the unified `ConfigStore` shape
- how current persisted object families map into that shape
- the implementation order for migrating the repository step by step

This document is intended to be implementation-oriented. It keeps only the final recommended direction.

## 2. Problem Summary

The current configstore layer has one typed store interface and one concrete SQLite store type for each persisted object family:

- provider config
- credential
- route
- virtual key
- managed model

This creates avoidable duplication:

- repeated CRUD wrappers
- repeated migration record structs
- repeated object encoding and decoding plumbing
- repeated store construction and wiring

At the same time, the current objects do not all have the same storage shape:

- `VirtualKey` has a primary key `id` and a unique secondary lookup key `key`
- `ManagedModel` uses a composite primary key `(provider_id, upstream_model)`
- some object families use tag-based listing while others do not

The refactor must remove boilerplate without losing those storage semantics.

## 3. Final Recommended Direction

The repository should move to a schema-driven configstore design with these properties:

- no per-object typed store interfaces
- no requirement that domain objects implement storage interfaces
- one generic `ConfigStore` interface for bound object stores
- one `StoreSchema` per persisted object family
- one codec plus one metadata extractor per persisted object family
- one backend implementation per storage backend and payload format combination

The first concrete target is:

- a generic SQLite JSON backend

Future backends may include:

- SQLite protobuf
- PostgreSQL JSON

The key rule is:

- `StoreSchema` is registered once during initialization
- runtime CRUD methods operate on a store instance already bound to that schema

Runtime code should not pass `StoreSchema` into each CRUD method call.

## 4. Core Abstractions

### 4.1 `ConfigObjectCodec`

`ConfigObjectCodec` owns serialization and deserialization for one persisted object family.

Illustrative shape:

```go
type ConfigObjectCodec interface {
	Encode(obj any) ([]byte, error)
	Decode(data []byte) (any, error)
}
```

Responsibilities:

- validate the concrete Go type expected by this object family
- encode that object into the backend payload format
- decode the stored payload back into the object

For the first phase, all codecs are JSON codecs.

### 4.2 `ObjectMetadata`

`ObjectMetadata` extracts storage metadata from one persisted object family.

Illustrative shape:

```go
type ObjectMetadata interface {
	PrimaryKey(obj any) ([]any, error)
	Tag(obj any) (string, bool, error)
	Indexes(obj any) (map[string]any, error)
}
```

Responsibilities:

- extract ordered primary-key parts from an object
- extract the optional tag value
- extract zero or more named secondary index values

Design rules:

- primary keys must support both single-key and composite-key objects
- tag support is optional
- secondary indexes are optional

### 4.3 `StoreSchema`

`StoreSchema` describes how one object family is persisted.

Illustrative shape:

```go
type StoreSchema struct {
	Name string
	Kind string
	Table string

	PrimaryKeyColumns []string
	TagColumn string
	DataColumn string

	IndexColumns []IndexSchema
	Timestamped bool

	Codec    ConfigObjectCodec
	Metadata ObjectMetadata
}

type IndexSchema struct {
	Name   string
	Column string
	Unique bool
}
```

Responsibilities:

- define the table name
- define primary-key columns
- define the optional tag column
- define the payload column
- define optional secondary indexes
- connect the backend to the correct codec and metadata extractor

`StoreSchema` belongs to initialization and backend construction, not to runtime CRUD calls.

## 5. Unified Store API

The generic store should be exposed as a schema-bound `ConfigStore`.

Illustrative shape:

```go
type ConfigStore interface {
	List(ctx context.Context) ([]any, error)
	ListByTag(ctx context.Context, tag string) ([]any, error)
	ListByTagPrefix(ctx context.Context, tagPrefix string) ([]any, error)

	Create(ctx context.Context, obj any) error
	Update(ctx context.Context, obj any) error
	Delete(ctx context.Context, keyParts ...any) error

	Get(ctx context.Context, keyParts ...any) (any, error)
	GetByIndex(ctx context.Context, indexName string, value any) (any, error)
}
```

Interface rules:

- `Create` and `Update` remain separate operations
- `Create` and `Update` derive keys, tag values, and indexes from `ObjectMetadata`
- `Get` and `Delete` accept ordered key parts so they work for both single-key and composite-key objects
- `GetByIndex` supports named secondary lookups such as `VirtualKey.key`
- `ListByTag` and `ListByTagPrefix` are only valid for schemas that define a tag column

The backend should return clear errors when:

- the schema does not define a tag column but a tag-listing method is called
- the number of key parts does not match the schema primary key
- an unknown index name is requested

## 6. Backend Registration And Store Construction

The generic backend should own schema registration and store construction.

Illustrative shape:

```go
type ConfigStoreBackend interface {
	Register(name string, schema StoreSchema) error
	Get(name string) (ConfigStore, error)
}
```

Expected usage:

```go
backend.Register("providers", ProviderConfigSchema)
backend.Register("credentials", CredentialSchema)
backend.Register("routes", RouteSchema)
backend.Register("virtual_keys", VirtualKeySchema)
backend.Register("managed_models", ManagedModelSchema)
```

Then runtime code retrieves bound stores:

```go
providers, err := backend.Get("providers")
virtualKeys, err := backend.Get("virtual_keys")
models, err := backend.Get("managed_models")
```

Design rules:

- schema registration happens once during configstore initialization
- `Get(name)` returns a store already bound to the requested schema
- store names should be centralized constants, not free-form strings scattered through the codebase
- registration state should belong to the backend instance, not to global package state

## 7. Schema Placement

Schemas should live in a dedicated package:

- `pkg/configstore/schema/`

That package should own:

- store name constants
- schema definitions
- codecs
- metadata extractors

This keeps storage-specific mapping logic together and avoids spreading schema details across runtime packages.

Domain packages should continue to own their domain types and business logic, but should not own configstore schema registration logic.

## 8. Object Family Mapping

### 8.1 ProviderConfig

- store name: `providers`
- table: `providers`
- primary key columns:
  - `id`
- tag column:
  - `tag`
- tag meaning:
  - provider type
- payload column:
  - `config`
- secondary indexes:
  - none
- timestamped:
  - yes

### 8.2 Credential

- store name: `credentials`
- table: `cliauth_credentials`
- primary key columns:
  - `id`
- tag column:
  - `tag`
- tag meaning:
  - provider type
- payload column:
  - `data`
- secondary indexes:
  - none
- timestamped:
  - yes

### 8.3 Route

- store name: `routes`
- table: `routes`
- primary key columns:
  - `id`
- tag column:
  - `tag`
- tag meaning:
  - route tag
- payload column:
  - `config`
- secondary indexes:
  - none
- timestamped:
  - yes

### 8.4 VirtualKey

- store name: `virtual_keys`
- table: `virtual_keys`
- primary key columns:
  - `id`
- tag column:
  - `tag`
- tag meaning:
  - key tag
- payload column:
  - `config`
- secondary indexes:
  - `key`
- timestamped:
  - yes

The `key` index must be unique and must support `GetByIndex(ctx, "key", value)`.

### 8.5 ManagedModel

- store name: `managed_models`
- table: `model_configs`
- primary key columns:
  - `provider_id`
  - `upstream_model`
- tag column:
  - none
- payload column:
  - `data`
- secondary indexes:
  - none
- timestamped:
  - no

This object family is the main reason the unified store API must support composite primary keys.

## 9. SQLite JSON Backend Requirements

The first implementation target is a generic SQLite JSON backend.

The backend must support:

- auto-migration for schema-defined tables
- single-column primary keys
- composite primary keys
- optional tag column
- optional secondary indexes
- optional timestamps
- ordered key-part lookup for `Get` and `Delete`
- named index lookup for `GetByIndex`
- JSON payload encode and decode through `ConfigObjectCodec`

The backend should also normalize the current error behavior:

- missing row on `Get` returns `configstoreintf.ErrNotFound`
- missing row on `Update` returns `configstoreintf.ErrNotFound`
- unknown store name on `Build` returns a deterministic backend error

## 10. Implementation Plan

The refactor should be implemented in phases so the repository stays testable after each step.

### Phase 1: Introduce New Shared Types

Add the new abstractions without removing current stores yet.

Create:

- `pkg/configstore/intf/configstore.go`
  - define the new generic `ConfigStore`
- `pkg/configstore/intf/backend.go`
  - define the backend registration and build interface
- `pkg/configstore/schema/`
  - add `StoreSchema`, `IndexSchema`, store name constants
  - add `ConfigObjectCodec`
  - add `ObjectMetadata`

Keep existing typed storer interfaces in place during this phase so the repository still compiles.

### Phase 2: Add Schemas For All Current Object Families

Create schema definitions and related codec and metadata implementations for:

- provider configs
- credentials
- routes
- virtual keys
- managed models

This phase should produce one canonical schema definition per object family.

### Phase 3: Implement Generic SQLite JSON Backend

Create a new SQLite backend implementation that:

- stores a schema registry on the backend instance
- supports `Register(name, schema)` and `Get(name)`
- returns schema-bound `ConfigStore` instances
- implements CRUD, tag listing, prefix listing, and named index lookups
- supports both single-key and composite-key schemas

Suggested files:

- `pkg/configstore/sqlite/backend.go`
- `pkg/configstore/sqlite/store.go`
- `pkg/configstore/sqlite/migrate.go`

The existing `sqliteJSONStore` may either be evolved into this backend or replaced by it.

### Phase 4: Register Schemas During ConfigStore Initialization

Update configstore initialization so the backend is created once and all schemas are registered immediately.

This includes updating the current configstore wiring that now exposes typed store factories.

The result of this phase should be:

- one initialized backend instance
- all current object schemas registered into that backend

### Phase 5: Migrate Lowest-Variance Object Families

Migrate these object families first:

- provider configs
- credentials
- routes

For each family:

- replace typed store construction with `Get(storeName)`
- adapt call sites to use the bound generic `ConfigStore`
- keep existing runtime behavior unchanged
- migrate and update tests

These are the safest first migrations because they already closely match the existing generic JSON-store pattern.

### Phase 6: Migrate VirtualKey

Migrate `VirtualKey` after the generic backend already supports named indexes.

Required checks:

- `Create` persists `id`, `key`, `tag`, and payload correctly
- `Update` preserves existing semantics
- `Get(id)` works through the primary key
- `GetByIndex("key", value)` works for request-time bearer-key lookup
- timestamp behavior remains unchanged

Update:

- runtime manager code
- admin handlers and tests
- gateway request-time lookup paths

### Phase 7: Migrate ManagedModel

Migrate `ManagedModel` after the backend already supports composite keys.

Required checks:

- `Create` persists the composite key correctly
- `Update` targets the composite key correctly
- `Get(providerID, upstreamModel)` works
- `Delete(providerID, upstreamModel)` works
- list ordering remains stable enough for tests

Update:

- modelcatalog services
- admin handlers and tests

### Phase 8: Remove Old Typed Store Layer

Once all object families use the generic backend:

- remove typed store interfaces under `pkg/configstore/intf/`
- remove typed SQLite store wrappers under `pkg/configstore/sqlite/`
- remove old construction methods that return typed stores
- simplify tests to use the generic backend and generic store contracts

This is the cleanup phase. It should happen only after all migrations pass.

### Phase 9: Final Cleanup And Documentation

After the code migration is complete:

- remove transitional compatibility code
- update `README.md` only if user-visible behavior changed
- update `docs/DESIGN.md` if configstore architecture is described there
- ensure all new store names and schemas are documented in code comments where helpful

## 11. Testing Strategy

Each migration phase should preserve test coverage for:

- create success
- update success
- update missing row
- get success
- get missing row
- delete success
- list ordering
- tag filtering
- tag-prefix filtering where applicable
- named index lookup where applicable
- composite-key lookup where applicable

Additional backend-focused tests should verify:

- schema registration errors
- duplicate registration errors
- unknown store name errors
- invalid key-part count errors
- unknown index-name errors

## 12. Expected End State

After the refactor:

- the repository has one generic configstore API
- each persisted object family is described by one schema
- SQLite JSON persistence is implemented once, not once per object family
- virtual-key secondary lookup and managed-model composite keys remain supported
- future backends and payload formats can be added without recreating object-specific store interfaces
