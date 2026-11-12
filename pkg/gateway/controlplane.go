package gateway

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/agent-guide/agent-gateway/pkg/agent"
	"github.com/agent-guide/agent-gateway/pkg/configmgr"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	connectorpkg "github.com/agent-guide/agent-gateway/pkg/connector"
	"github.com/agent-guide/agent-gateway/pkg/credential"
	"github.com/agent-guide/agent-gateway/pkg/gateway/agentroute"
	"github.com/agent-guide/agent-gateway/pkg/gateway/llmroute"
	"github.com/agent-guide/agent-gateway/pkg/gateway/mcproute"
	"github.com/agent-guide/agent-gateway/pkg/gateway/modelcatalog"
	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

var (
	ErrResourceReferenced      = errors.New("resource is referenced")
	ErrDependencyNotConfigured = errors.New("referenced resource is not configured")
)

// controlPlane serializes cross-resource decisions for one gateway lifecycle.
// Agent mutations enter after their write lock; Connector mutations acquire it
// before coordination so reference checks share the same established lock order.
type controlPlane struct {
	connectors  *connectorpkg.Manager
	mu          sync.Mutex
	backend     configstore.ConfigStoreBackend
	providers   *ProviderManager
	routes      *routecore.RouteConfigManager
	agents      *agent.Manager
	credentials *credential.Manager
	models      modelcatalog.Service
	modelStore  configstore.ConfigStore
}

func (c *controlPlane) providerMutation(ctx context.Context, action configmgr.MutationAction, id string, next *provider.ProviderConfig, apply func(context.Context) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id == "" {
		return fmt.Errorf("provider id is required")
	}
	if action == configmgr.MutationRefresh {
		return c.recoverProvider(ctx, id)
	}
	if err := c.providers.checkPending(id); err != nil {
		return err
	}
	if c.providers.IsStatic(id) {
		return fmt.Errorf("%w: %q", ErrStaticProviderReadOnly, id)
	}
	if action == configmgr.MutationDelete {
		refs, err := c.providerReferences(ctx, id)
		if err != nil {
			return err
		}
		if len(refs) > 0 {
			return fmt.Errorf("%w: provider %q: %s", ErrResourceReferenced, id, strings.Join(refs, ", "))
		}
		if c.modelStore == nil {
			c.providers.beginDeletion(id)
			err := apply(ctx)
			if err != nil {
				return c.providers.pendingFailure(id, err)
			}
			if err := c.publishProviderDeletion(id); err != nil {
				return c.providers.pendingFailure(id, err)
			}
			c.providers.clearPending(id)
			return nil
		}
		return c.deleteProvider(ctx, id)
	}
	if next != nil {
		creds, err := c.credentialDefinitions(ctx)
		if err != nil {
			return err
		}
		for _, cred := range creds {
			if strings.EqualFold(cred.ProviderID, id) && !strings.EqualFold(strings.TrimSpace(cred.ProviderType), strings.TrimSpace(next.ProviderType)) {
				return fmt.Errorf("%w: credential %q requires provider type %q", ErrResourceReferenced, cred.ID, cred.ProviderType)
			}
		}
	}
	return c.writeProvider(ctx, action, id, next)
}

func (c *controlPlane) providerReferences(ctx context.Context, id string) ([]string, error) {
	var refs []string
	if c.routes != nil {
		routes, err := c.routes.List(ctx, routecore.RouteListOptions{})
		if err != nil {
			return nil, err
		}
		for _, cfg := range routes {
			if cfg.Kind != routecore.RouteKindLLM {
				continue
			}
			r, err := llmroute.NewLLMRouteFromConfig(cfg)
			if err != nil {
				return nil, err
			}
			if r.TargetPolicy != nil && slices.Contains(r.TargetPolicy.ProviderIDs(), id) {
				refs = append(refs, "route:"+cfg.ID)
			}
		}
	}
	creds, err := c.credentialDefinitions(ctx)
	if err != nil {
		return nil, err
	}
	for _, cred := range creds {
		if strings.EqualFold(cred.ProviderID, id) {
			refs = append(refs, "credential:"+cred.ID)
		}
	}
	sort.Strings(refs)
	return refs, nil
}

func (c *controlPlane) credentialDefinitions(ctx context.Context) ([]credential.Credential, error) {
	byID := map[string]credential.Credential{}
	if c.credentials != nil {
		for _, cred := range c.credentials.ListCredentials(credential.Filter{}) {
			byID[cred.ID] = cred.Credential
		}
	}
	if c.backend != nil {
		store, err := c.backend.Get(schema.StoreCredentials)
		if err != nil {
			return nil, err
		}
		if store != nil {
			items, err := store.List(ctx)
			if err != nil {
				return nil, err
			}
			for _, item := range items {
				cred, ok := item.(*credential.Credential)
				if !ok || cred == nil {
					return nil, fmt.Errorf("unexpected credential type %T", item)
				}
				byID[cred.ID] = *cred
			}
		}
	}
	out := make([]credential.Credential, 0, len(byID))
	for _, cred := range byID {
		out = append(out, cred)
	}
	return out, nil
}

func (c *controlPlane) deleteProvider(ctx context.Context, id string) error {
	tx, ok := c.backend.(configstore.TransactionalBackend)
	if !ok {
		return configstore.ErrTransactionsUnsupported
	}
	c.providers.beginDeletion(id)
	written := false
	var callbackErr error
	err := tx.Transaction(ctx, []string{schema.StoreProviders, schema.StoreManagedModels}, func(stores map[string]configstore.ConfigStore) (err error) {
		defer func() { callbackErr = err }()
		providers, models := stores[schema.StoreProviders], stores[schema.StoreManagedModels]
		if _, err := providers.Get(ctx, id); err != nil {
			if errors.Is(err, configstore.ErrNotFound) {
				return fmt.Errorf("%w: %q", ErrProviderNotConfigured, id)
			}
			return err
		}
		items, err := models.List(ctx)
		if err != nil {
			return err
		}
		if err := providers.Delete(ctx, id); err != nil {
			return err
		}
		written = true
		for _, item := range items {
			model, ok := item.(*modelcatalog.ManagedModel)
			if !ok || model == nil {
				return fmt.Errorf("unexpected managed model type %T", item)
			}
			if model.ProviderID == id {
				if err := models.Delete(ctx, id, model.UpstreamModel); err != nil {
					return err
				}
			}
		}
		if _, err := providers.Get(ctx, id); !errors.Is(err, configstore.ErrNotFound) {
			return fmt.Errorf("verify provider deletion: %v", err)
		}
		remaining, err := models.List(ctx)
		if err != nil {
			return fmt.Errorf("verify managed model cleanup: %w", err)
		}
		for _, item := range remaining {
			model, ok := item.(*modelcatalog.ManagedModel)
			if !ok || model == nil {
				return fmt.Errorf("unexpected managed model type %T", item)
			}
			if model.ProviderID == id {
				return fmt.Errorf("managed model %q/%q remains after cleanup", id, model.UpstreamModel)
			}
		}
		return nil
	})
	if err != nil {
		if callbackErr != nil || !written {
			c.providers.clearPending(id)
			return err
		}
		return c.providers.pendingFailure(id, err)
	}
	if err := c.publishProviderDeletion(id); err != nil {
		return c.providers.pendingFailure(id, err)
	}
	c.providers.clearPending(id)
	return nil
}

func (c *controlPlane) routeMutation(ctx context.Context, action configmgr.MutationAction, id string, next *routecore.RouteConfig, apply func(context.Context) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.agents != nil {
		var refs []string
		agents, err := c.agents.List(ctx)
		if err != nil && !errors.Is(err, agent.ErrAgentNotConfigured) {
			return err
		}
		agents = append(agents, c.agents.Snapshot()...)
		for _, a := range agents {
			if slices.Contains(a.Routes.LLMRouteIDs, id) && (next == nil || next.Kind != routecore.RouteKindLLM) {
				refs = append(refs, a.ID)
			}
			if slices.Contains(a.Routes.MCPRouteIDs, id) && (next == nil || next.Kind != routecore.RouteKindMCP) {
				refs = append(refs, a.ID)
			}
		}
		if len(refs) > 0 {
			sort.Strings(refs)
			refs = slices.Compact(refs)
			return fmt.Errorf("%w: route %q is referenced by agents: %s", ErrResourceReferenced, id, strings.Join(refs, ", "))
		}
	}
	if next != nil {
		switch next.Kind {
		case routecore.RouteKindLLM:
			r, err := llmroute.NewLLMRouteFromConfig(*next)
			if err != nil {
				return err
			}
			if err := r.ValidateDefinition(); err != nil {
				return err
			}
			for _, providerID := range r.TargetPolicy.ProviderIDs() {
				if _, err := c.providers.GetConfig(ctx, providerID); err != nil {
					return fmt.Errorf("%w: %w", ErrDependencyNotConfigured, err)
				}
			}
		case routecore.RouteKindMCP:
			r, err := mcproute.NewMCPRouteFromConfig(*next)
			if err != nil {
				return err
			}
			if c.connectors == nil {
				return fmt.Errorf("%w: connector manager unavailable", ErrDependencyNotConfigured)
			}
			if _, err := c.connectors.Get(ctx, r.ConnectorID); err != nil {
				return fmt.Errorf("%w: connector unavailable: %w", ErrDependencyNotConfigured, err)
			}
		case routecore.RouteKindAgent:
			agentID, err := agentroute.DecodeTargetAgentID(next.TargetPolicy)
			if err != nil {
				return err
			}
			if c.agents != nil {
				if err := c.agents.CheckExecutionID(agentID); err != nil {
					return err
				}
			}
			if c.agents == nil || !c.agents.HasAgent(agentID) {
				return fmt.Errorf("%w: route %q targets unknown agent %q", ErrDependencyNotConfigured, id, agentID)
			}
		}
	}
	return apply(ctx)
}

func (c *controlPlane) modelMutation(ctx context.Context, action configmgr.MutationAction, id string, next *modelcatalog.ManagedModel, apply func(context.Context) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.providers.checkPending(id); err != nil {
		return err
	}
	if next != nil {
		if _, err := c.providers.GetConfig(ctx, strings.TrimSpace(next.ProviderID)); err != nil {
			return fmt.Errorf("%w: %w", ErrDependencyNotConfigured, err)
		}
	}
	return apply(ctx)
}

func (c *controlPlane) agentMutation(ctx context.Context, action configmgr.MutationAction, id string, next *agent.Agent, apply func(context.Context) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if next != nil {
		if err := c.validateAgentConnectors(ctx, *next); err != nil {
			return err
		}
	}
	if action == configmgr.MutationRefresh && c.backend != nil {
		store, err := c.backend.Get(schema.StoreAgents)
		if err != nil {
			return err
		}
		if store != nil {
			items, err := store.List(ctx)
			if err != nil {
				return err
			}
			for _, item := range items {
				a, ok := item.(*agent.Agent)
				if !ok || a == nil {
					return fmt.Errorf("invalid stored Agent")
				}
				if err := c.validateAgentConnectors(ctx, *a); err != nil {
					return err
				}
			}
		}
	}
	return apply(ctx)
}

func (c *controlPlane) validateAgentConnectors(ctx context.Context, a agent.Agent) error {
	for _, id := range a.Resources.ConnectorIDs {
		id = strings.TrimSpace(id)
		if c.connectors == nil {
			return fmt.Errorf("%w: connector %q", ErrDependencyNotConfigured, id)
		}
		_, err := c.connectors.Get(ctx, id)
		if err != nil {
			return fmt.Errorf("%w: connector %q: %w", ErrDependencyNotConfigured, id, err)
		}
	}
	return nil
}

// Credentials normalize their ProviderID to lowercase, while Provider IDs retain
// case. Resolve that ownership key without changing Provider lookup semantics.
func (c *controlPlane) credentialProvider(ctx context.Context, id string) (provider.ProviderConfig, error) {
	configs, err := c.providers.ListConfigs(ctx, ProviderListOptions{})
	if err != nil {
		return provider.ProviderConfig{}, err
	}
	configs = append(configs, c.providers.configs.Snapshot(configmgr.ListQuery{})...)
	ids := map[string]bool{}
	for _, cfg := range configs {
		ids[cfg.Id] = true
	}
	// A pending deletion may already be absent from persistence and cache.
	c.providers.mu.RLock()
	for pendingID := range c.providers.pending {
		ids[pendingID] = true
	}
	c.providers.mu.RUnlock()
	canonical := ""
	for candidate := range ids {
		if !strings.EqualFold(candidate, strings.TrimSpace(id)) {
			continue
		}
		if canonical != "" {
			return provider.ProviderConfig{}, fmt.Errorf("credential provider_id %q matches multiple Provider IDs", id)
		}
		canonical = candidate
	}
	if canonical == "" {
		return provider.ProviderConfig{}, fmt.Errorf("%w: %q", ErrProviderNotConfigured, id)
	}
	return c.providers.GetConfig(ctx, canonical)
}

func (c *controlPlane) credentialMutation(ctx context.Context, action configmgr.MutationAction, id string, next *credential.Credential, apply func(context.Context) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	check := func(cred *credential.Credential) error {
		if credential.IsHTTPAgentCredentialScope(cred.ScopeValue()) {
			return nil
		}
		cfg, err := c.credentialProvider(ctx, cred.ProviderID)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrDependencyNotConfigured, err)
		}
		if !strings.EqualFold(strings.TrimSpace(cfg.ProviderType), strings.TrimSpace(cred.ProviderType)) {
			return fmt.Errorf("credential %q provider type must match provider %q", cred.ID, cfg.Id)
		}
		return nil
	}
	if next != nil {
		if err := check(next); err != nil {
			return err
		}
	}
	if action == configmgr.MutationRefresh && c.backend != nil {
		store, err := c.backend.Get(schema.StoreCredentials)
		if err != nil {
			return err
		}
		items, err := store.List(ctx)
		if err != nil {
			return err
		}
		for _, item := range items {
			cred, ok := item.(*credential.Credential)
			if !ok || cred == nil {
				return fmt.Errorf("unexpected credential type %T", item)
			}
			if err := check(cred); err != nil {
				return err
			}
		}
	}
	return apply(ctx)
}

func (c *controlPlane) connectorMutation(ctx context.Context, id string, next *connectorpkg.ConnectorConfig, apply func() error) error {
	run := func() error {
		c.mu.Lock()
		defer c.mu.Unlock()
		if id == "" { // Refresh compares authoritative records against the published family.
			store, err := c.backend.Get(schema.StoreConnectors)
			if err != nil {
				return err
			}
			items, err := store.List(ctx)
			if err != nil {
				return err
			}
			candidate := map[string]*connectorpkg.ConnectorConfig{}
			for _, item := range items {
				cfg, ok := item.(*connectorpkg.ConnectorConfig)
				if !ok {
					return fmt.Errorf("invalid connector record")
				}
				candidate[cfg.ID] = cfg
			}
			for _, old := range c.connectors.Snapshot() {
				cfg := candidate[old.ID]
				if cfg == nil || !connectorpkg.ExecutionEqual(old, *cfg) {
					if err := c.connectorReferences(ctx, old.ID); err != nil {
						return err
					}
				}
			}
		} else if c.connectors.ExecutionChange(id, next) {
			if err := c.connectorReferences(ctx, id); err != nil {
				return err
			}
		}
		return apply()
	}
	if c.agents != nil {
		return c.agents.WithMutationLock(run)
	}
	return run()
}
func (c *controlPlane) connectorReferences(ctx context.Context, id string) error {
	if c.routes != nil {
		routes, err := c.routes.List(ctx, routecore.RouteListOptions{})
		if err != nil {
			return err
		}
		for _, cfg := range routes {
			if cfg.Kind != routecore.RouteKindMCP {
				continue
			}
			r, err := mcproute.NewMCPRouteFromConfig(cfg)
			if err != nil {
				return err
			}
			if r.ConnectorID == id {
				return fmt.Errorf("%w: connector %q is referenced by route %q", ErrResourceReferenced, id, r.ID)
			}
		}
	}
	if c.agents != nil {
		agents, err := c.agents.List(ctx)
		if err != nil {
			return err
		}
		agents = append(agents, c.agents.Snapshot()...)
		for _, a := range agents {
			if slices.Contains(a.Resources.ConnectorIDs, id) {
				return fmt.Errorf("%w: connector %q is referenced by Agent %q", ErrResourceReferenced, id, a.ID)
			}
		}
	}
	return nil
}
