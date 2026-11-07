package configstore

import (
	"context"
	"errors"
	"fmt"
)

var ErrTransactionsUnsupported = errors.New("config store transactions are not supported")

// TransactionalBackend is an optional capability. The callback receives only
// the requested registered stores and must not retain them or publish runtime
// state before commit. No schemas are registered or migrated in the callback.
type TransactionalBackend interface {
	Transaction(context.Context, []string, func(map[string]ConfigStore) error) error
}

// TransactionalCreator binds already registered schemas to one transaction.
// It leaves schema registration and domain rules with their existing owners.
type TransactionalCreator interface {
	Transaction(context.Context, []StoreSchema, func([]ConfigStore) error) error
}

func (b *Backend) Transaction(ctx context.Context, names []string, apply func(map[string]ConfigStore) error) error {
	if b == nil {
		return fmt.Errorf("config store backend is nil")
	}
	if apply == nil || len(names) == 0 {
		return fmt.Errorf("transaction requires stores and a callback")
	}
	creator, ok := b.creator.(TransactionalCreator)
	if !ok {
		return ErrTransactionsUnsupported
	}
	b.mu.RLock()
	schemas := make([]StoreSchema, 0, len(names))
	seen := map[string]bool{}
	for _, name := range names {
		schema, exists := b.schemas[name]
		if !exists {
			b.mu.RUnlock()
			return fmt.Errorf("%w: %s", ErrUnknownStoreName, name)
		}
		if seen[name] {
			b.mu.RUnlock()
			return fmt.Errorf("duplicate transaction store %q", name)
		}
		seen[name] = true
		schemas = append(schemas, cloneSchema(schema))
	}
	b.mu.RUnlock()
	return creator.Transaction(ctx, schemas, func(stores []ConfigStore) error {
		if len(stores) != len(names) {
			return fmt.Errorf("transaction store count mismatch")
		}
		scoped := make(map[string]ConfigStore, len(names))
		for i, store := range stores {
			if store == nil {
				return fmt.Errorf("transaction store %q is nil", names[i])
			}
			scoped[names[i]] = store
		}
		return apply(scoped)
	})
}

func cloneSchema(s StoreSchema) StoreSchema {
	s.PrimaryKeyColumns = append([]string(nil), s.PrimaryKeyColumns...)
	s.IndexColumns = append([]IndexSchema(nil), s.IndexColumns...)
	return s
}
