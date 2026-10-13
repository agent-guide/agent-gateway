// Package extensionstore adapts the existing generic ConfigStore, keeping its
// implementation types out of the protected extension lifecycle contract.
package extensionstore

import (
	"context"
	"fmt"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	"github.com/agent-guide/agent-gateway/pkg/extension"
)

type Store struct{ store configstore.ConfigStore }

func New(backend configstore.ConfigStoreBackend) (*Store, error) {
	s, err := backend.Get(schema.StoreExtensions)
	if err != nil {
		return nil, err
	}
	return &Store{s}, nil
}
func (s *Store) List(ctx context.Context) ([]extension.Envelope, error) {
	items, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]extension.Envelope, 0, len(items))
	for _, item := range items {
		switch e := item.(type) {
		case *extension.Envelope:
			out = append(out, *e)
		case extension.Envelope:
			out = append(out, e)
		default:
			return nil, fmt.Errorf("invalid stored extension")
		}
	}
	return out, nil
}
func (s *Store) Put(ctx context.Context, e extension.Envelope, create bool) error {
	if create {
		return s.store.Create(ctx, &e)
	}
	return s.store.Update(ctx, &e)
}
func (s *Store) Delete(ctx context.Context, id string) error { return s.store.Delete(ctx, id) }
