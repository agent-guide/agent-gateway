package llmroute

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"sort"
	"strings"

	"github.com/agent-guide/agent-gateway/internal/statuserr"
	"github.com/agent-guide/agent-gateway/pkg/gateway/modelcatalog"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

func (c *RouteTargetPolicyCommon) Normalize() {
	if c == nil {
		return
	}
	if c.CredentialSelectorValue == "" {
		c.CredentialSelectorValue = RouteCredentialSelectRoundRobin
	}
	if len(c.CredentialScopeOrderValue) == 0 {
		c.CredentialScopeOrderValue = []RouteCredentialScope{RouteCredentialScopeProviderID}
	}
	if len(c.CredentialTypeOrderValue) == 0 {
		c.CredentialTypeOrderValue = []RouteCredentialType{RouteCredentialTypeAPIKey, RouteCredentialTypeOAuthToken}
	}
}

func (c RouteTargetPolicyCommon) CredentialSelector() RouteCredentialSelectStrategy {
	if c.CredentialSelectorValue == "" {
		return RouteCredentialSelectRoundRobin
	}
	return c.CredentialSelectorValue
}

func (c RouteTargetPolicyCommon) CredentialScopeOrder() []RouteCredentialScope {
	if len(c.CredentialScopeOrderValue) == 0 {
		return []RouteCredentialScope{RouteCredentialScopeProviderID}
	}
	return append([]RouteCredentialScope(nil), c.CredentialScopeOrderValue...)
}

func (c RouteTargetPolicyCommon) CredentialTypeOrder() []RouteCredentialType {
	if len(c.CredentialTypeOrderValue) == 0 {
		return []RouteCredentialType{RouteCredentialTypeAPIKey, RouteCredentialTypeOAuthToken}
	}
	return append([]RouteCredentialType(nil), c.CredentialTypeOrderValue...)
}

func (p *RouteModelPolicy) Normalize() {
	if p == nil {
		return
	}
	p.RouteTargetPolicyCommon.Normalize()
	p.DefaultModel = strings.TrimSpace(p.DefaultModel)
	for i := range p.ModelTargets {
		t := &p.ModelTargets[i]
		t.Name = strings.TrimSpace(t.Name)
		t.Kind = strings.TrimSpace(t.Kind)
		t.ProviderID = strings.TrimSpace(t.ProviderID)
		t.UpstreamModel = strings.TrimSpace(t.UpstreamModel)
		t.LogicalModelID = strings.TrimSpace(t.LogicalModelID)
		if t.Kind == "provider-model" && t.Name == "" {
			t.Name = t.UpstreamModel
		}
	}
}

func (p *RouteModelPolicy) ProviderIDs() []string {
	seen := map[string]bool{}
	var ids []string
	for _, t := range p.ModelTargets {
		if t.Kind == "provider-model" && !seen[t.ProviderID] {
			seen[t.ProviderID] = true
			ids = append(ids, t.ProviderID)
		}
	}
	return ids
}

func (p *RouteModelPolicy) CredentialSelector() RouteCredentialSelectStrategy {
	return p.RouteTargetPolicyCommon.CredentialSelector()
}

func (p *RouteModelPolicy) CredentialScopeOrder() []RouteCredentialScope {
	scopes := p.RouteTargetPolicyCommon.CredentialScopeOrder()
	if len(scopes) == 1 && scopes[0] == RouteCredentialScopeProviderID {
		return []RouteCredentialScope{RouteCredentialScopeModelCustom, RouteCredentialScopeProviderID}
	}
	return scopes
}

func (p *RouteModelPolicy) CredentialTypeOrder() []RouteCredentialType {
	return p.RouteTargetPolicyCommon.CredentialTypeOrder()
}

func (p *RouteModelPolicy) ResolveTarget(ctx context.Context, routeID string, catalog ModelCatalogResolver, providers ProviderConfigResolver, req RequestRequirements) (*ResolvedTarget, error) {
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

	var bindings []modelcatalog.ModelCandidate
	strategy := modelcatalog.SelectionPriority
	fallback := modelcatalog.FallbackPolicy{}
	logicalID := ""
	switch target.Kind {
	case "provider-model":
		bindings = []modelcatalog.ModelCandidate{{ProviderID: target.ProviderID, UpstreamModel: target.UpstreamModel}}
	case "logical-model":
		m, ok, err := catalog.GetLogicalModel(ctx, target.LogicalModelID)
		if err != nil {
			return nil, err
		}
		if !ok || m.Disabled {
			return nil, statuserr.New(http.StatusBadGateway, "logical model is unavailable")
		}
		bindings = m.Candidates
		strategy = m.Strategy
		fallback = m.Fallback
		logicalID = m.ID
	default:
		return nil, statuserr.New(http.StatusBadGateway, "invalid model target kind")
	}
	candidates := make([]resolvedCandidate, 0, len(bindings))
	var requirementGaps []provider.RequirementGap
	for _, candidate := range bindings {
		if _, excluded := req.ExcludedCandidates[CandidateKey(candidate.ProviderID, candidate.UpstreamModel)]; excluded {
			continue
		}
		view, ok, err := catalog.GetResolvedProviderModel(ctx, candidate.ProviderID, candidate.UpstreamModel)
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
		if !resolved.meetsGenericRequirements(req) {
			continue
		}
		capabilities := provider.CapabilitiesForProviderType(resolved.ProviderType)
		if missing := provider.MissingProtocolFeatures(req.ProtocolRequirements, capabilities.ProtocolFeatures); len(missing) > 0 {
			requirementGaps = append(requirementGaps, provider.RequirementGap{Candidate: provider.CandidateIdentity{
				ProviderID: resolved.ProviderID, ProviderType: resolved.ProviderType, UpstreamModel: resolved.UpstreamModel,
			}, Missing: missing})
			continue
		}
		candidates = append(candidates, resolved)
	}

	if len(candidates) == 0 {
		if len(requirementGaps) > 0 {
			return nil, &provider.RequirementGapsError{Target: modelName, Gaps: requirementGaps}
		}
		message := fmt.Sprintf("model target %q has no eligible bindings", modelName)
		if requirements := requestRequirementNames(req); len(requirements) > 0 {
			message += fmt.Sprintf(" for request requirements: %s", strings.Join(requirements, ", "))
		}
		return nil, statuserr.New(http.StatusBadGateway, message)
	}

	chosen := chooseCandidate(candidates, strategy)
	return &ResolvedTarget{
		LogicalModel:    logicalID,
		ClientModel:     modelName,
		Fallback:        fallback,
		ProviderID:      chosen.ProviderID,
		ProviderType:    chosen.ProviderType,
		UpstreamModel:   chosen.UpstreamModel,
		CredentialScope: chosen.CredentialScope,
		Capabilities:    chosen.Capabilities,
	}, nil
}

func CandidateKey(providerID string, upstreamModel string) string {
	key, _ := json.Marshal([2]string{providerID, upstreamModel})
	return string(key)
}

func (p *RouteModelPolicy) targetByName(name string) *RouteModelTarget {
	for i := range p.ModelTargets {
		if p.ModelTargets[i].Name == name {
			return &p.ModelTargets[i]
		}
	}
	return nil
}

// RequestRequirements captures request attributes required for route resolution.
// Model always means an explicitly exposed route-visible name.
type RequestRequirements struct {
	Model                string
	RequireStreaming     bool
	RequireTools         bool
	RequireVision        bool
	RequireEmbeddings    bool
	ProtocolRequirements provider.ProtocolRequirementSet
	ExcludedCandidates   map[string]struct{}
}

func requestRequirementNames(req RequestRequirements) []string {
	var names []string
	if req.RequireStreaming {
		names = append(names, "streaming")
	}
	if req.RequireTools {
		names = append(names, "tools")
	}
	if req.RequireVision {
		names = append(names, "vision")
	}
	if req.RequireEmbeddings {
		names = append(names, "embeddings")
	}
	for _, feature := range req.ProtocolRequirements.Features() {
		names = append(names, string(feature))
	}
	sort.Strings(names)
	return names
}

type ResolvedTarget struct {
	ClientModel     string
	Fallback        modelcatalog.FallbackPolicy
	LogicalModel    string
	ProviderID      string
	ProviderType    string
	UpstreamModel   string
	CredentialScope string
	Capabilities    provider.ModelCapabilities
}

type ModelCatalogResolver interface {
	GetLogicalModel(context.Context, string) (*modelcatalog.LogicalModel, bool, error)
	GetProviderModel(ctx context.Context, providerID string, upstreamModel string) (*modelcatalog.ProviderModel, bool, error)
	GetResolvedProviderModel(ctx context.Context, providerID string, upstreamModel string) (*modelcatalog.ResolvedProviderModel, bool, error)
}

type ProviderConfigResolver interface {
	GetConfig(ctx context.Context, providerID string) (provider.ProviderConfig, error)
}

func (r LLMRoute) ResolveTarget(ctx context.Context, catalog ModelCatalogResolver, providers ProviderConfigResolver, req RequestRequirements) (*ResolvedTarget, error) {
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

type resolvedCandidate struct {
	ProviderID      string
	ProviderType    string
	UpstreamModel   string
	CredentialScope string
	Capabilities    provider.ModelCapabilities
	Weight          int
	Priority        int
}

func (c resolvedCandidate) meetsGenericRequirements(req RequestRequirements) bool {
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

func chooseCandidate(candidates []resolvedCandidate, strategy modelcatalog.SelectionStrategy) resolvedCandidate {
	if len(candidates) == 1 {
		return candidates[0]
	}
	switch strategy {
	case modelcatalog.SelectionPriority:
		return choosePriorityBinding(candidates)
	case modelcatalog.SelectionWeighted:
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
