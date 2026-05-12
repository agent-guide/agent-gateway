package gateway

import (
	"context"
	"errors"

	configstoreintf "github.com/agent-guide/agent-gateway/pkg/configstore/intf"
	virtualkeypkg "github.com/agent-guide/agent-gateway/pkg/gateway/virtualkey"
)

type testProvisionVirtualKeyStore struct {
	items map[string]*virtualkeypkg.VirtualKey
}

type testAppConfigStore struct {
	virtualKeyStore configstoreintf.VirtualKeyStorer
}

func (s testAppConfigStore) GetCredentialStore(context.Context, configstoreintf.ConfigObjectDecoder) (configstoreintf.CredentialStorer, error) {
	return nil, nil
}

func (s testAppConfigStore) GetProviderConfigStore(context.Context, configstoreintf.ConfigObjectDecoder) (configstoreintf.ProviderConfigStorer, error) {
	return nil, nil
}

func (s testAppConfigStore) GetVirtualKeyStore(context.Context, configstoreintf.ConfigObjectDecoder) (configstoreintf.VirtualKeyStorer, error) {
	return s.virtualKeyStore, nil
}

func (s testAppConfigStore) GetRouteStore(context.Context, configstoreintf.ConfigObjectDecoder) (configstoreintf.RouteStorer, error) {
	return nil, nil
}

func (s *testProvisionVirtualKeyStore) ListByTag(context.Context, string) ([]any, error) {
	return nil, nil
}

func (s *testProvisionVirtualKeyStore) Create(_ context.Context, id string, _ string, obj any) error {
	item, ok := obj.(*virtualkeypkg.VirtualKey)
	if !ok {
		return errors.New("unexpected type")
	}
	if s.items == nil {
		s.items = map[string]*virtualkeypkg.VirtualKey{}
	}
	cloned := *item
	s.items[id] = &cloned
	return nil
}

func (s *testProvisionVirtualKeyStore) Update(_ context.Context, id string, obj any) error {
	item, ok := obj.(*virtualkeypkg.VirtualKey)
	if !ok {
		return errors.New("unexpected type")
	}
	if s.items == nil {
		s.items = map[string]*virtualkeypkg.VirtualKey{}
	}
	if _, exists := s.items[id]; !exists {
		return configstoreintf.ErrNotFound
	}
	cloned := *item
	s.items[id] = &cloned
	return nil
}

func (s *testProvisionVirtualKeyStore) Delete(context.Context, string) error { return nil }

func (s *testProvisionVirtualKeyStore) Get(_ context.Context, id string) (any, error) {
	item, ok := s.items[id]
	if !ok {
		return nil, configstoreintf.ErrNotFound
	}
	return item, nil
}

func (s *testProvisionVirtualKeyStore) GetByKey(_ context.Context, key string) (any, error) {
	for _, item := range s.items {
		if item.Key == key {
			return item, nil
		}
	}
	return nil, configstoreintf.ErrNotFound
}
