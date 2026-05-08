package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	routepkg "github.com/agent-guide/caddy-agent-gateway/gateway/route"
	"github.com/agent-guide/caddy-agent-gateway/internal/statuserr"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
	"github.com/agent-guide/caddy-agent-gateway/pkg/llm/credentialmgr"
	credentialmgrscheduler "github.com/agent-guide/caddy-agent-gateway/pkg/llm/credentialmgr/scheduler"
	"github.com/cloudwego/eino/schema"
)

var errCandidateRejected = errors.New("candidate rejected")

type RoutedProvider struct {
	route               routepkg.AgentRoute
	requestRequirements routepkg.RequestRequirements
	providerResolver    ProviderResolver
	providerConfigs     routepkg.ProviderConfigResolver
	modelCatalog        routepkg.ModelCatalogResolver
	credentialMgr       *credentialmgr.Manager
	scheduler           credentialmgrscheduler.CredentialScheduler
}

type executionState struct {
	triedCandidates  map[string]struct{}
	triedCredentials map[string]struct{}
	modelFallbacks   int
}

type resolvedAttempt struct {
	target *routepkg.ResolvedTarget
	base   provider.Provider
	cred   *credentialmgr.ManagedCredential
	ctx    context.Context
}

func (p *RoutedProvider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	var out *provider.ChatResponse
	err := p.executeWithFallback(ctx, req.Model, func(ctx context.Context, attempt *resolvedAttempt) error {
		cloned := *req
		cloned.Model = attempt.target.UpstreamModel
		resp, err := attempt.base.Chat(ctx, &cloned)
		if err == nil {
			req.Model = cloned.Model
			out = resp
		}
		return err
	})
	return out, err
}

func (p *RoutedProvider) StreamChat(ctx context.Context, req *provider.ChatRequest) (*schema.StreamReader[*schema.Message], error) {
	var out *schema.StreamReader[*schema.Message]
	err := p.executeWithFallback(ctx, req.Model, func(ctx context.Context, attempt *resolvedAttempt) error {
		cloned := *req
		cloned.Model = attempt.target.UpstreamModel
		stream, err := attempt.base.StreamChat(ctx, &cloned)
		if err == nil {
			req.Model = cloned.Model
			out = stream
		}
		return err
	})
	return out, err
}

func (p *RoutedProvider) CreateResponses(ctx context.Context, req *provider.ResponsesRequest) (*provider.ResponsesResponse, error) {
	var out *provider.ResponsesResponse
	err := p.executeWithFallback(ctx, req.Model, func(ctx context.Context, attempt *resolvedAttempt) error {
		base, ok := attempt.base.(provider.ResponsesProvider)
		if !ok {
			return statuserr.New(http.StatusNotImplemented, "responses api is not supported by this provider")
		}
		cloned := *req
		cloned.Model = attempt.target.UpstreamModel
		resp, err := base.CreateResponses(ctx, &cloned)
		if err == nil {
			req.Model = cloned.Model
			out = resp
		}
		return err
	})
	return out, err
}

func (p *RoutedProvider) StreamResponses(ctx context.Context, req *provider.ResponsesRequest) (*schema.StreamReader[*provider.ResponsesStreamEvent], error) {
	var out *schema.StreamReader[*provider.ResponsesStreamEvent]
	err := p.executeWithFallback(ctx, req.Model, func(ctx context.Context, attempt *resolvedAttempt) error {
		base, ok := attempt.base.(provider.ResponsesProvider)
		if !ok {
			return statuserr.New(http.StatusNotImplemented, "responses api is not supported by this provider")
		}
		cloned := *req
		cloned.Model = attempt.target.UpstreamModel
		stream, err := base.StreamResponses(ctx, &cloned)
		if err == nil {
			req.Model = cloned.Model
			out = stream
		}
		return err
	})
	return out, err
}

func (p *RoutedProvider) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	target, err := p.resolveTarget(ctx, "")
	if err != nil {
		return nil, err
	}
	base, err := p.resolveProvider(ctx, target.ProviderID)
	if err != nil {
		return nil, err
	}
	return base.ListModels(ctx)
}

func (p *RoutedProvider) Capabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{}
}

func (p *RoutedProvider) Config() provider.ProviderConfig {
	return provider.ProviderConfig{}
}

func (p *RoutedProvider) executeWithFallback(ctx context.Context, reqModel string, call func(context.Context, *resolvedAttempt) error) error {
	state := &executionState{
		triedCandidates:  map[string]struct{}{},
		triedCredentials: map[string]struct{}{},
	}
	maxFallbacks := 0
	if p.route.UsesLogicalModel() && p.route.TargetPolicy.Fallback.Enabled {
		maxFallbacks = p.route.TargetPolicy.Fallback.MaxNum
	}

	var lastErr error
	for {
		attempt, err := p.prepareAttempt(ctx, reqModel, state)
		if err != nil {
			if errors.Is(err, errCandidateRejected) && state.modelFallbacks <= maxFallbacks {
				lastErr = err
				continue
			}
			if lastErr != nil {
				return lastErr
			}
			return err
		}

		err = call(attempt.ctx, attempt)
		p.markResult(ctx, attempt, err)
		if err == nil {
			return nil
		}
		lastErr = err

		if p.classifyFailure(err) != failureReselectModel || state.modelFallbacks >= maxFallbacks {
			return err
		}
		state.triedCandidates[routepkg.CandidateKey(attempt.target.ProviderID, attempt.target.UpstreamModel)] = struct{}{}
		state.modelFallbacks++
	}
}

func (p *RoutedProvider) prepareAttempt(ctx context.Context, reqModel string, state *executionState) (*resolvedAttempt, error) {
	target, err := p.resolveTarget(ctx, reqModel, state.triedCandidates)
	if err != nil {
		return nil, err
	}
	base, err := p.resolveProvider(ctx, target.ProviderID)
	if err != nil {
		return nil, err
	}
	credCtx, cred, err := p.selectCredential(ctx, target, state)
	if err != nil {
		state.triedCandidates[routepkg.CandidateKey(target.ProviderID, target.UpstreamModel)] = struct{}{}
		if p.route.UsesLogicalModel() {
			state.modelFallbacks++
		}
		return nil, fmt.Errorf("%w: %w", errCandidateRejected, err)
	}
	return &resolvedAttempt{target: target, base: base, cred: cred, ctx: credCtx}, nil
}

func (p *RoutedProvider) resolveTarget(ctx context.Context, reqModel string, excluded ...map[string]struct{}) (*routepkg.ResolvedTarget, error) {
	req := p.requestRequirements
	req.Model = reqModel
	if len(excluded) > 0 {
		req.ExcludedCandidates = excluded[0]
	}
	return p.route.ResolveTarget(ctx, p.modelCatalog, p.providerConfigs, req)
}

func (p *RoutedProvider) resolveProvider(ctx context.Context, providerID string) (provider.Provider, error) {
	prov, err := p.providerResolver.ResolveProvider(ctx, providerID)
	if err != nil || prov == nil {
		if errors.Is(err, ErrProviderDisabled) {
			return nil, statuserr.New(http.StatusForbidden, fmt.Sprintf("route target provider %q is disabled", providerID))
		}
		return nil, statuserr.New(http.StatusBadGateway, fmt.Sprintf("route target provider %q is not configured", providerID))
	}
	return prov, nil
}

func (p *RoutedProvider) selectCredential(ctx context.Context, target *routepkg.ResolvedTarget, state *executionState) (context.Context, *credentialmgr.ManagedCredential, error) {
	if p.scheduler == nil || p.credentialMgr == nil {
		return ctx, nil, nil
	}
	scopes := p.expandCredentialScopes(target)
	for _, scope := range scopes {
		if scope == "" {
			continue
		}
		for _, source := range p.route.TargetPolicy.CredentialSourceOrder {
			cred, err := p.scheduler.Pick(ctx, credentialmgrscheduler.Filter{
				Source:          string(source),
				CredentialScope: scope,
				Model:           target.UpstreamModel,
				Selector:        string(p.route.TargetPolicy.CredentialSelector),
			}, state.triedCredentials)
			if err != nil || cred == nil {
				continue
			}
			if cred.Source == credentialmgr.SourceCLIAuthToken {
				refreshed, err := p.credentialMgr.RefreshCredentialIfNeeded(ctx, cred.ID)
				if err != nil {
					state.triedCredentials[cred.ID] = struct{}{}
					continue
				}
				cred = refreshed
			}
			state.triedCredentials[cred.ID] = struct{}{}
			return provider.WithCredential(ctx, cred.Credential.Clone()), cred, nil
		}
	}
	if p.route.UsesLogicalModel() {
		return ctx, nil, statuserr.New(http.StatusBadGateway, fmt.Sprintf("no credential available for provider %q model %q", target.ProviderID, target.UpstreamModel))
	}
	return ctx, nil, nil
}

func (p *RoutedProvider) expandCredentialScopes(target *routepkg.ResolvedTarget) []string {
	out := make([]string, 0, len(p.route.TargetPolicy.CredentialScopeOrder))
	for _, scope := range p.route.TargetPolicy.CredentialScopeOrder {
		switch scope {
		case routepkg.RouteCredentialScopeModelCustom:
			if target.CredentialScope != "" {
				out = append(out, target.CredentialScope)
			}
		case routepkg.RouteCredentialScopeProviderID:
			out = append(out, credentialmgr.ProviderIDCredentialScope(target.ProviderID))
		}
	}
	return out
}

type failureAction int

const (
	failureStop failureAction = iota
	failureReselectModel
)

func (p *RoutedProvider) classifyFailure(err error) failureAction {
	status := statuserr.StatusCode(err, http.StatusBadGateway)
	switch status {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return failureReselectModel
	default:
		if status >= 500 {
			return failureReselectModel
		}
		return failureStop
	}
}

func (p *RoutedProvider) markResult(ctx context.Context, attempt *resolvedAttempt, err error) {
	if p.scheduler == nil || attempt == nil || attempt.cred == nil {
		return
	}
	result := credentialmgrscheduler.Result{
		CredentialID: attempt.cred.ID,
		Model:        attempt.target.UpstreamModel,
		Success:      err == nil,
	}
	if err != nil {
		status := statuserr.StatusCode(err, http.StatusBadGateway)
		result.Error = &credentialmgrscheduler.Error{
			Code:       http.StatusText(status),
			Message:    err.Error(),
			HTTPStatus: status,
			Retryable:  status == http.StatusTooManyRequests || status >= 500,
		}
	}
	p.scheduler.MarkResult(ctx, result)
}

var (
	_ provider.Provider          = (*RoutedProvider)(nil)
	_ provider.ResponsesProvider = (*RoutedProvider)(nil)
)
