package route

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"sort"

	"github.com/agent-guide/caddy-agent-gateway/gateway/modelcatalog"
	"github.com/agent-guide/caddy-agent-gateway/internal/statuserr"
	"github.com/agent-guide/caddy-agent-gateway/llm/credentialmgr"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
)

// RouteResolveRequest captures request attributes required for route resolution.
// Model means logical model ID in logical-model mode and upstream model in direct mode.
type RouteResolveRequest struct {
	Model             string
	RequireStreaming  bool
	RequireTools      bool
	RequireVision     bool
	RequireEmbeddings bool
}

type ResolvedTarget struct {
	Model           string
	ProviderID      string
	ProviderType    string
	UpstreamModel   string
	CredentialScope string
	Capabilities    provider.ModelCapabilities
}

type ModelCatalogResolver interface {
	GetManagedModel(ctx context.Context, providerID string, upstreamModel string) (*modelcatalog.ManagedModel, bool, error)
	GetResolvedManagedModel(ctx context.Context, providerID string, upstreamModel string) (*modelcatalog.ResolvedManagedModel, bool, error)
}

type ProviderConfigResolver interface {
	GetConfig(ctx context.Context, providerID string) (provider.ProviderConfig, error)
}

func (r AgentRoute) ResolveTarget(ctx context.Context, catalog ModelCatalogResolver, providers ProviderConfigResolver, req RouteResolveRequest) (*ResolvedTarget, error) {
	if catalog == nil {
		return nil, statuserr.New(http.StatusServiceUnavailable, "model catalog is not configured")
	}
	if providers == nil {
		return nil, statuserr.New(http.StatusServiceUnavailable, "provider resolver is not configured")
	}

	if r.usesDirectProvider() {
		providerID := r.TargetPolicy.ProviderTarget.ProviderID
		credentialScope := credentialmgr.ProviderIDCredentialScope(providerID)
		cfg, err := providers.GetConfig(ctx, providerID)
		if err != nil {
			return nil, err
		}
		return &ResolvedTarget{
			ProviderID:      providerID,
			ProviderType:    cfg.ProviderType,
			UpstreamModel:   req.Model,
			CredentialScope: credentialScope,
		}, nil
	}

	modelName := req.Model
	if modelName == "" {
		modelName = r.TargetPolicy.DefaultModel
	}
	if modelName == "" {
		return nil, statuserr.New(http.StatusBadRequest, fmt.Sprintf("route %q requires a model", r.ID))
	}

	target := r.targetByName(modelName)
	if target == nil {
		return nil, statuserr.New(http.StatusForbidden, fmt.Sprintf("model %q is not allowed on route %q", modelName, r.ID))
	}

	candidates := make([]resolvedCandidate, 0, len(target.Candidates))
	for _, candidate := range target.Candidates {
		view, ok, err := catalog.GetResolvedManagedModel(ctx, candidate.ProviderID, candidate.UpstreamModel)
		if err != nil {
			return nil, err
		}
		if !ok || !view.Enabled {
			continue
		}

		cfg, err := providers.GetConfig(ctx, candidate.ProviderID)
		if err != nil || cfg.Disabled {
			continue
		}

		resolved := resolvedCandidate{
			ProviderID:      candidate.ProviderID,
			ProviderType:    cfg.ProviderType,
			UpstreamModel:   candidate.UpstreamModel,
			CredentialScope: view.CredentialScope,
			Capabilities:    view.Capabilities,
			Weight:          firstPositive(candidate.Weight, 1),
			Priority:        candidate.Priority,
		}
		if !resolved.meetsRequirements(req) {
			continue
		}
		candidates = append(candidates, resolved)
	}

	if len(candidates) == 0 {
		return nil, statuserr.New(http.StatusBadGateway, fmt.Sprintf("model target %q has no eligible bindings", modelName))
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority < candidates[j].Priority
		}
		return candidates[i].ProviderID < candidates[j].ProviderID
	})

	bestPriority := candidates[0].Priority
	tier := make([]resolvedCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Priority != bestPriority {
			break
		}
		tier = append(tier, candidate)
	}

	chosen := chooseWeightedBinding(tier)
	return &ResolvedTarget{
		Model:           modelName,
		ProviderID:      chosen.ProviderID,
		ProviderType:    chosen.ProviderType,
		UpstreamModel:   chosen.UpstreamModel,
		CredentialScope: chosen.CredentialScope,
		Capabilities:    chosen.Capabilities,
	}, nil
}

func (r AgentRoute) targetByName(name string) *RouteModelTarget {
	for i := range r.TargetPolicy.ModelTargets {
		if r.TargetPolicy.ModelTargets[i].Name == name {
			return &r.TargetPolicy.ModelTargets[i]
		}
	}
	return nil
}

type resolvedCandidate struct {
	ProviderID      string
	ProviderType    string
	UpstreamModel   string
	CredentialScope string
	Capabilities    provider.ModelCapabilities
	Weight          int
	Priority        int
}

func (c resolvedCandidate) meetsRequirements(req RouteResolveRequest) bool {
	if req.RequireStreaming && !c.Capabilities.Streaming {
		return false
	}
	if req.RequireTools && !c.Capabilities.Tools {
		return false
	}
	if req.RequireVision && !c.Capabilities.Vision {
		return false
	}
	if req.RequireEmbeddings && !c.Capabilities.Embeddings {
		return false
	}
	return true
}

func normalizedWeight(weight int) int {
	if weight <= 0 {
		return 1
	}
	return weight
}

func firstPositive(items ...int) int {
	for _, item := range items {
		if item > 0 {
			return item
		}
	}
	return 0
}

func chooseWeightedBinding(bindings []resolvedCandidate) resolvedCandidate {
	if len(bindings) == 1 {
		return bindings[0]
	}
	total := 0
	for _, binding := range bindings {
		total += normalizedWeight(binding.Weight)
	}
	if total <= 0 {
		return bindings[0]
	}
	pick := rand.Intn(total)
	for _, binding := range bindings {
		weight := normalizedWeight(binding.Weight)
		if pick < weight {
			return binding
		}
		pick -= weight
	}
	return bindings[0]
}
