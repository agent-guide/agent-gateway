package llmroute

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"

	"github.com/agent-guide/agent-gateway/internal/statuserr"
	"github.com/agent-guide/agent-gateway/pkg/gateway/modelcatalog"
	"github.com/agent-guide/agent-gateway/pkg/llm/credentialmgr"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

// RequestRequirements captures request attributes required for route resolution.
// Model means logical model ID in logical-model mode and upstream model in direct mode.
type RequestRequirements struct {
	Model              string
	RequireStreaming   bool
	RequireTools       bool
	RequireVision      bool
	RequireEmbeddings  bool
	ExcludedCandidates map[string]struct{}
}

type ResolvedTarget struct {
	LogicalModel    string
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

func (r AgentRoute) ResolveTarget(ctx context.Context, catalog ModelCatalogResolver, providers ProviderConfigResolver, req RequestRequirements) (*ResolvedTarget, error) {
	r.Normalize()
	if catalog == nil {
		return nil, statuserr.New(http.StatusServiceUnavailable, "model catalog is not configured")
	}
	if providers == nil {
		return nil, statuserr.New(http.StatusServiceUnavailable, "provider resolver is not configured")
	}
	if r.TargetPolicy == nil {
		return nil, statuserr.New(http.StatusBadGateway, fmt.Sprintf("route %q target policy is not configured", r.ID))
	}
	return r.TargetPolicy.ResolveTarget(ctx, r.ID, catalog, providers, req)
}

func (p *RouteDirectProviderPolicy) ResolveTarget(ctx context.Context, routeID string, _ ModelCatalogResolver, providers ProviderConfigResolver, req RequestRequirements) (*ResolvedTarget, error) {
	p.Normalize()
	providerID := p.ProviderID
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

func (p *RouteLogicalModelTargetPolicy) ResolveTarget(ctx context.Context, routeID string, catalog ModelCatalogResolver, providers ProviderConfigResolver, req RequestRequirements) (*ResolvedTarget, error) {
	p.Normalize()
	modelName := req.Model
	if modelName == "" {
		modelName = p.DefaultModel
	}
	if modelName == "" {
		return nil, statuserr.New(http.StatusBadRequest, fmt.Sprintf("route %q requires a model", routeID))
	}

	target := p.targetByName(modelName)
	if target == nil {
		return nil, statuserr.New(http.StatusForbidden, fmt.Sprintf("model %q is not allowed on route %q", modelName, routeID))
	}

	candidates := make([]resolvedCandidate, 0, len(target.Candidates))
	for _, candidate := range target.Candidates {
		if _, excluded := req.ExcludedCandidates[CandidateKey(candidate.ProviderID, candidate.UpstreamModel)]; excluded {
			continue
		}
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
			Weight:          candidate.Weight,
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

	chosen := chooseCandidate(candidates, p.ModelSelectorStrategy)
	return &ResolvedTarget{
		LogicalModel:    modelName,
		ProviderID:      chosen.ProviderID,
		ProviderType:    chosen.ProviderType,
		UpstreamModel:   chosen.UpstreamModel,
		CredentialScope: chosen.CredentialScope,
		Capabilities:    chosen.Capabilities,
	}, nil
}

func CandidateKey(providerID string, upstreamModel string) string {
	return providerID + "/" + upstreamModel
}

func (p *RouteLogicalModelTargetPolicy) targetByName(name string) *RouteModelTarget {
	for i := range p.ModelTargets {
		if p.ModelTargets[i].Name == name {
			return &p.ModelTargets[i]
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

func (c resolvedCandidate) meetsRequirements(req RequestRequirements) bool {
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
		return 0
	}
	return weight
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

func chooseCandidate(candidates []resolvedCandidate, strategy RouteSelectionStrategy) resolvedCandidate {
	if len(candidates) == 1 {
		return candidates[0]
	}
	switch strategy {
	case RouteSelectionStrategyPriority:
		return choosePriorityBinding(candidates)
	case RouteSelectionStrategyWeighted:
		return chooseWeightedBinding(candidates)
	default:
		return chooseAutoBinding(candidates)
	}
}

func choosePriorityBinding(candidates []resolvedCandidate) resolvedCandidate {
	bestIndex := 0
	for i := 1; i < len(candidates); i++ {
		if candidates[i].Priority > candidates[bestIndex].Priority {
			bestIndex = i
		}
	}
	return candidates[bestIndex]
}

func chooseAutoBinding(candidates []resolvedCandidate) resolvedCandidate {
	bestPriority := candidates[0].Priority
	for _, candidate := range candidates[1:] {
		if candidate.Priority > bestPriority {
			bestPriority = candidate.Priority
		}
	}
	tier := make([]resolvedCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Priority == bestPriority {
			tier = append(tier, candidate)
		}
	}
	if !hasPositiveWeight(tier) {
		return tier[0]
	}
	return chooseWeightedBinding(tier)
}

func hasPositiveWeight(candidates []resolvedCandidate) bool {
	for _, candidate := range candidates {
		if candidate.Weight > 0 {
			return true
		}
	}
	return false
}
