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
	"github.com/agent-guide/agent-gateway/pkg/credential"
	"github.com/agent-guide/agent-gateway/pkg/gateway/agentroute"
	"github.com/agent-guide/agent-gateway/pkg/gateway/llmroute"
	"github.com/agent-guide/agent-gateway/pkg/gateway/modelcatalog"
	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

var (
	ErrResourceReferenced      = errors.New("resource is referenced")
	ErrDependencyNotConfigured = errors.New("referenced resource is not configured")
)

// controlPlane serializes cross-resource decisions for one gateway lifecycle.
// Agent mutation enters after its write lock; this service never acquires it.
type controlPlane struct {
	mu          sync.Mutex
	backend     configstore.ConfigStoreBackend
	providers   *ProviderManager
	routes      *routecore.AgentRouteConfigManager
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
			err := apply(ctx)
			if err == nil {
				modelcatalog.InvalidateProvider(c.models, id)
			}
			return err
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
	err := apply(ctx)
	if err == nil {
		modelcatalog.InvalidateProvider(c.models, id)
	}
	return err
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
			r, err := llmroute.NewLLMRouteConfigFromConfig(cfg)
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
	err := tx.Transaction(ctx, []string{schema.StoreProviders, schema.StoreManagedModels}, func(stores map[string]configstore.ConfigStore) error {
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
		return err
	}
	c.providers.configs.Evict(id)
	c.providers.deleteDynamicCache(id)
	modelcatalog.InvalidateProvider(c.models, id)
	return nil
}

func (c *controlPlane) routeMutation(ctx context.Context, action configmgr.MutationAction, id string, next *routecore.AgentRouteConfig, apply func(context.Context) error) error {
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
			r, err := llmroute.NewLLMRouteConfigFromConfig(*next)
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
		case routecore.RouteKindAgent:
			agentID, err := agentroute.DecodeTargetAgentID(next.TargetPolicy)
			if err != nil {
				return err
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
	return apply(ctx)
}

func (c *controlPlane) credentialMutation(ctx context.Context, action configmgr.MutationAction, id string, next *credential.Credential, apply func(context.Context) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	check := func(cred *credential.Credential) error {
		if credential.IsHTTPAgentCredentialScope(cred.ScopeValue()) {
			return nil
		}
		cfg, err := c.providers.GetConfig(ctx, strings.TrimSpace(cred.ProviderID))
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
