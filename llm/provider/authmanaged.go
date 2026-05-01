package provider

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/agent-guide/caddy-agent-gateway/internal/statuserr"
	"github.com/agent-guide/caddy-agent-gateway/llm/credentialmgr"
	"github.com/cloudwego/eino/schema"
)

const staticAPIKeyCredentialIDPrefix = "provider-static-api-key:"

type authManagedProvider struct {
	base          Provider
	credentialMgr *credentialmgr.Manager
}

func WrapWithCredentialManager(base Provider, credMgr *credentialmgr.Manager) Provider {
	if base == nil {
		return base
	}
	p := &authManagedProvider{
		base:          base,
		credentialMgr: credMgr,
	}
	return p
}

func (p *authManagedProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	ctx, cred := p.pickCredential(ctx, req.Model)
	resp, err := p.base.Chat(ctx, req)
	p.markResult(ctx, cred, req.Model, err)
	return resp, err
}

func (p *authManagedProvider) StreamChat(ctx context.Context, req *ChatRequest) (*schema.StreamReader[*schema.Message], error) {
	ctx, cred := p.pickCredential(ctx, req.Model)
	stream, err := p.base.StreamChat(ctx, req)
	p.markResult(ctx, cred, req.Model, err)
	return stream, err
}

func (p *authManagedProvider) Embedding(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
	base, ok := p.base.(EmbeddingProvider)
	if !ok {
		return nil, statuserr.New(http.StatusNotImplemented, "embeddings api is not supported by this provider")
	}
	ctx, cred := p.pickCredential(ctx, req.Model)
	resp, err := base.Embedding(ctx, req)
	p.markResult(ctx, cred, req.Model, err)
	return resp, err
}

func (p *authManagedProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	return p.base.ListModels(ctx)
}

func (p *authManagedProvider) Capabilities() ProviderCapabilities {
	return p.base.Capabilities()
}

func (p *authManagedProvider) Config() ProviderConfig {
	return p.base.Config()
}

func (p *authManagedProvider) CreateResponses(ctx context.Context, req *ResponsesRequest) (*ResponsesResponse, error) {
	base, ok := p.base.(ResponsesProvider)
	if !ok {
		return nil, statuserr.New(http.StatusNotImplemented, "responses api is not supported by this provider")
	}
	ctx, cred := p.pickCredential(ctx, req.Model)
	resp, err := base.CreateResponses(ctx, req)
	p.markResult(ctx, cred, req.Model, err)
	return resp, err
}

func (p *authManagedProvider) StreamResponses(ctx context.Context, req *ResponsesRequest) (*schema.StreamReader[*ResponsesStreamEvent], error) {
	base, ok := p.base.(ResponsesProvider)
	if !ok {
		return nil, statuserr.New(http.StatusNotImplemented, "responses api is not supported by this provider")
	}
	ctx, cred := p.pickCredential(ctx, req.Model)
	stream, err := base.StreamResponses(ctx, req)
	p.markResult(ctx, cred, req.Model, err)
	return stream, err
}

func (p *authManagedProvider) pickCredential(ctx context.Context, model string) (context.Context, *credentialmgr.ManagedCredential) {
	switch p.base.Config().AuthStrategy {
	case AuthStrategyManagedCLIAuthTokenFirst:
		cred := p.pickManagedCredential(ctx, credentialmgr.SourceCLIAuthToken, model)
		if cred != nil {
			return WithCredential(ctx, cred.Credential.Clone()), cred
		}
		return ctx, nil
	case AuthStrategyManagedAPIKeyFirst, "":
		cred := p.pickManagedCredential(ctx, credentialmgr.SourceAPIKey, model)
		if cred != nil {
			return WithCredential(ctx, cred.Credential.Clone()), cred
		}
		return ctx, nil
	default:
		return ctx, nil
	}
}

func (p *authManagedProvider) pickManagedCredential(ctx context.Context, source string, model string) *credentialmgr.ManagedCredential {
	filter := credentialmgr.Filter{Source: source, Model: model}
	filter.ProviderID = p.base.Config().Id
	filter.ProviderType = p.base.Config().ProviderType
	cred, err := p.credentialMgr.PickWithFilter(ctx, filter, nil)
	if err != nil {
		return nil
	}
	if source == credentialmgr.SourceCLIAuthToken {
		cred, err = p.credentialMgr.RefreshCredentialIfNeeded(ctx, cred.ID)
		if err != nil {
			return nil
		}
	}
	return cred
}

func (p *authManagedProvider) markResult(ctx context.Context, cred *credentialmgr.ManagedCredential, model string, err error) {
	if cred == nil || p.credentialMgr == nil {
		return
	}

	result := credentialmgr.Result{
		CredentialID: cred.ID,
		ProviderType: cred.ProviderType,
		Model:        model,
		Success:      err == nil,
	}
	if err != nil {
		var se statuserr.StatusError
		httpStatus := http.StatusBadGateway
		if errors.As(err, &se) {
			httpStatus = se.StatusCode()
		}
		result.Error = &credentialmgr.Error{
			Code:       http.StatusText(httpStatus),
			Message:    err.Error(),
			HTTPStatus: httpStatus,
			Retryable:  httpStatus == http.StatusTooManyRequests || httpStatus >= 500,
		}
	}
	p.credentialMgr.MarkResult(ctx, result)
}

func StaticAPIKeyCredential(cfg ProviderConfig, providerID string) *credentialmgr.Credential {
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		return nil
	}
	cfg.Defaults()
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		providerID = strings.TrimSpace(cfg.Id)
	}
	if providerID == "" {
		providerID = strings.TrimSpace(cfg.ProviderType)
	}
	providerType := strings.TrimSpace(cfg.ProviderType)
	if providerType == "" {
		providerType = providerID
	}
	attrs := map[string]string{
		"api_key": apiKey,
	}
	if baseURL := strings.TrimSpace(cfg.BaseURL); baseURL != "" {
		attrs["base_url"] = baseURL
	}
	attrs["priority"] = "-1"
	now := time.Now().UTC()
	return &credentialmgr.Credential{
		ID:           StaticAPIKeyCredentialID(cfg),
		ProviderType: providerType,
		ProviderID:   providerID,
		Source:       credentialmgr.SourceAPIKey,
		Attributes:   attrs,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

func StaticAPIKeyCredentialID(cfg ProviderConfig) string {
	id := strings.TrimSpace(cfg.Id)
	if id == "" {
		id = strings.TrimSpace(cfg.ProviderType)
	}
	if id == "" {
		id = "default"
	}
	return staticAPIKeyCredentialIDPrefix + id
}

func StaticAPIKeyCredentialProviderID(credentialID string) (string, bool) {
	credentialID = strings.TrimSpace(credentialID)
	if credentialID == "" || !strings.HasPrefix(credentialID, staticAPIKeyCredentialIDPrefix) {
		return "", false
	}
	providerID := strings.TrimSpace(strings.TrimPrefix(credentialID, staticAPIKeyCredentialIDPrefix))
	if providerID == "" {
		return "", false
	}
	return providerID, true
}
