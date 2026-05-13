package gateway

import (
	"context"
	"errors"

	"github.com/agent-guide/agent-gateway/pkg/configstore"
	configstoreschema "github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	virtualkeypkg "github.com/agent-guide/agent-gateway/pkg/gateway/virtualkey"
)

type testProvisionVirtualKeyStore struct {
	items map[string]*virtualkeypkg.VirtualKey
}

type testAppConfigStore struct {
	virtualKeyStore configstore.ConfigStore
}

func (s testAppConfigStore) Register(string, configstore.StoreSchema) error {
	return nil
}

func (s testAppConfigStore) Get(name string) (configstore.ConfigStore, error) {
	if name == configstoreschema.StoreVirtualKeys {
		return s.virtualKeyStore, nil
	}
	return nil, nil
}

func (s *testProvisionVirtualKeyStore) ListByTag(context.Context, string) ([]any, error) {
	return nil, nil
}

func (s *testProvisionVirtualKeyStore) List(context.Context) ([]any, error) { return nil, nil }
func (s *testProvisionVirtualKeyStore) ListByTagPrefix(context.Context, string) ([]any, error) {
	return nil, nil
}

func (s *testProvisionVirtualKeyStore) Create(_ context.Context, obj any) error {
	if unwrapper, ok := obj.(interface{ ConfigStoreObject() any }); ok {
		obj = unwrapper.ConfigStoreObject()
	}
	item, ok := obj.(*virtualkeypkg.VirtualKey)
	if !ok {
		return errors.New("unexpected type")
	}
	if s.items == nil {
		s.items = map[string]*virtualkeypkg.VirtualKey{}
	}
	cloned := *item
	s.items[cloned.ID] = &cloned
	return nil
}

func (s *testProvisionVirtualKeyStore) Update(_ context.Context, obj any) error {
	item, ok := obj.(*virtualkeypkg.VirtualKey)
	if !ok {
		return errors.New("unexpected type")
	}
	if s.items == nil {
		s.items = map[string]*virtualkeypkg.VirtualKey{}
	}
	if _, exists := s.items[item.ID]; !exists {
		return configstore.ErrNotFound
	}
	cloned := *item
	s.items[item.ID] = &cloned
	return nil
}

func (s *testProvisionVirtualKeyStore) Delete(context.Context, ...any) error { return nil }

func (s *testProvisionVirtualKeyStore) Get(_ context.Context, keyParts ...any) (any, error) {
	id, _ := keyParts[0].(string)
	item, ok := s.items[id]
	if !ok {
		return nil, configstore.ErrNotFound
	}
	return item, nil
}

func (s *testProvisionVirtualKeyStore) GetByIndex(_ context.Context, indexName string, value any) (any, error) {
	key, _ := value.(string)
	for _, item := range s.items {
		if item.Key == key {
			return item, nil
		}
	}
	return nil, configstore.ErrNotFound
}
