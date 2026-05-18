package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/mcp/transport"
)

var ErrServiceNotConfigured = errors.New("mcp service is not configured")

type Manager struct {
	store configstore.ConfigStore

	mu               sync.Mutex
	discoverySession map[string]*discoverySession
}

func NewManager(store configstore.ConfigStore) *Manager {
	return &Manager{
		store:            store,
		discoverySession: make(map[string]*discoverySession),
	}
}

func (m *Manager) List(ctx context.Context) ([]MCPServiceConfig, error) {
	if m == nil || m.store == nil {
		return nil, ErrServiceNotConfigured
	}
	items, err := m.store.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]MCPServiceConfig, 0, len(items))
	for _, item := range items {
		cfg, err := decodeServiceConfigItem("", item)
		if err != nil {
			return nil, err
		}
		out = append(out, cfg)
	}
	return out, nil
}

func (m *Manager) Get(ctx context.Context, id string) (MCPServiceConfig, error) {
	if id == "" {
		return MCPServiceConfig{}, fmt.Errorf("id is required")
	}
	if m == nil || m.store == nil {
		return MCPServiceConfig{}, ErrServiceNotConfigured
	}
	item, err := m.store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			return MCPServiceConfig{}, ErrServiceNotConfigured
		}
		return MCPServiceConfig{}, err
	}
	return decodeServiceConfigItem(id, item)
}

func (m *Manager) Create(ctx context.Context, cfg MCPServiceConfig) error {
	if m == nil || m.store == nil {
		return ErrServiceNotConfigured
	}
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return err
	}
	cfg.NormalizeTimestamps(time.Now().UTC())
	if err := m.store.Create(ctx, storedMCPServiceConfig{cfg: &cfg, tag: string(cfg.Transport)}); err != nil {
		return err
	}
	m.invalidateDiscoverySession(cfg.ID)
	return nil
}

func (m *Manager) Update(ctx context.Context, id string, cfg MCPServiceConfig) error {
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
	if err := m.store.Update(ctx, storedMCPServiceConfig{cfg: &cfg, tag: string(cfg.Transport)}); err != nil {
		return err
	}
	m.invalidateDiscoverySession(id)
	return nil
}

func (m *Manager) Delete(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("id is required")
	}
	if m == nil || m.store == nil {
		return ErrServiceNotConfigured
	}
	if err := m.store.Delete(ctx, id); err != nil {
		return err
	}
	m.invalidateDiscoverySession(id)
	return nil
}

type discoverySession struct {
	transport       *transport.StreamableHTTPTransport
	configSignature string
	protocolVersion string
	initialize      initializeResult
	initializedAt   time.Time
	lastUsedAt      time.Time
}

type storedMCPServiceConfig struct {
	cfg any
	tag string
}

func (c storedMCPServiceConfig) ConfigStoreObject() any {
	return c.cfg
}

func (c storedMCPServiceConfig) ConfigStoreTag() string {
	return c.tag
}

func decodeServiceConfigItem(id string, item any) (MCPServiceConfig, error) {
	cfg, ok := item.(*MCPServiceConfig)
	if !ok || cfg == nil || cfg.ID == "" {
		if id == "" {
			id = "<unknown>"
		}
		return MCPServiceConfig{}, fmt.Errorf("mcp service %q has unexpected type %T", id, item)
	}
	cloned := *cfg
	return cloned, nil
}
