package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/configstore"
)

type Manager struct {
	store configstore.ConfigStore
}

func NewManager(store configstore.ConfigStore) *Manager {
	return &Manager{store: store}
}

func (m *Manager) List(ctx context.Context) ([]ServiceConfig, error) {
	if m == nil || m.store == nil {
		return nil, ErrServiceNotConfigured
	}
	items, err := m.store.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ServiceConfig, 0, len(items))
	for _, item := range items {
		cfg, err := decodeServiceConfigItem("", item)
		if err != nil {
			return nil, err
		}
		out = append(out, cfg)
	}
	return out, nil
}

func (m *Manager) Get(ctx context.Context, id string) (ServiceConfig, error) {
	if id == "" {
		return ServiceConfig{}, fmt.Errorf("id is required")
	}
	if m == nil || m.store == nil {
		return ServiceConfig{}, ErrServiceNotConfigured
	}
	item, err := m.store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			return ServiceConfig{}, ErrServiceNotConfigured
		}
		return ServiceConfig{}, err
	}
	return decodeServiceConfigItem(id, item)
}

func (m *Manager) Create(ctx context.Context, cfg ServiceConfig) error {
	if m == nil || m.store == nil {
		return ErrServiceNotConfigured
	}
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return err
	}
	cfg.NormalizeTimestamps(time.Now().UTC())
	return m.store.Create(ctx, storedServiceConfig{cfg: &cfg, tag: cfg.AgentType})
}

func (m *Manager) Update(ctx context.Context, id string, cfg ServiceConfig) error {
	if id == "" {
		return fmt.Errorf("id is required")
	}
	if m == nil || m.store == nil {
		return ErrServiceNotConfigured
	}
	current, err := m.Get(ctx, id)
	if err != nil {
		return err
	}
	cfg.ID = id
	cfg.CreatedAt = current.CreatedAt
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return err
	}
	cfg.NormalizeTimestamps(time.Now().UTC())
	return m.store.Update(ctx, storedServiceConfig{cfg: &cfg, tag: cfg.AgentType})
}

func (m *Manager) Delete(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("id is required")
	}
	if m == nil || m.store == nil {
		return ErrServiceNotConfigured
	}
	return m.store.Delete(ctx, id)
}

type storedServiceConfig struct {
	cfg any
	tag string
}

func (c storedServiceConfig) ConfigStoreObject() any {
	return c.cfg
}

func (c storedServiceConfig) ConfigStoreTag() string {
	return c.tag
}

func decodeServiceConfigItem(id string, item any) (ServiceConfig, error) {
	cfg, ok := item.(*ServiceConfig)
	if !ok || cfg == nil || cfg.ID == "" {
		if id == "" {
			id = "<unknown>"
		}
		return ServiceConfig{}, fmt.Errorf("acp service %q has unexpected type %T", id, item)
	}
	cloned := *cfg
	return cloned, nil
}
