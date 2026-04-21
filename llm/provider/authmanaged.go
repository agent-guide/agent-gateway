package provider

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/agent-guide/caddy-agent-gateway/llm/credentialmgr"
	"github.com/cloudwego/eino/schema"
)

const staticAPIKeyCredentialIDPrefix = "provider-static-api-key:"

type authManagedProvider struct {
	base          Provider
	providerID    string
	credentialMgr *credentialmgr.Manager
	config        ProviderConfig
}

func WrapWithCredentialManager(base Provider, providerID string, credMgr *credentialmgr.Manager) Provider {
	if base == nil || credMgr == nil {
		return base
	}
	cfg := base.Config()
	if cfg.Id == "" {
		cfg.Id = providerID
	}
	if cfg.ProviderType == "" {
		cfg.ProviderType = providerID
	}
	cfg.Defaults()

	p := &authManagedProvider{
		base:          base,
		providerID:    providerID,
		credentialMgr: credMgr,
		config:        cfg,
	}
	return p
}

func (p *authManagedProvider) Generate(ctx context.Context, req *GenerateRequest) (*GenerateResponse, error) {
	ctx, cred := p.pickCredential(ctx, req.Model)
	resp, err := p.base.Generate(ctx, req)
	p.markResult(ctx, cred, req.Model, err)
	return resp, err
}

func (p *authManagedProvider) Stream(ctx context.Context, req *GenerateRequest) (*schema.StreamReader[*schema.Message], error) {
	ctx, cred := p.pickCredential(ctx, req.Model)
	stream, err := p.base.Stream(ctx, req)
	p.markResult(ctx, cred, req.Model, err)
	return stream, err
}

func (p *authManagedProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	return p.base.ListModels(ctx)
}

func (p *authManagedProvider) Capabilities() ProviderCapabilities {
	return p.base.Capabilities()
}

func (p *authManagedProvider) Config() ProviderConfig {
	return p.config
}

func (p *authManagedProvider) pickCredential(ctx context.Context, model string) (context.Context, *credentialmgr.Credential) {
	if p.credentialMgr == nil {
		return ctx, nil
	}

	providerType := strings.TrimSpace(p.config.ProviderType)
	if providerType == "" {
		providerType = p.providerID
	}
	cred, err := p.credentialMgr.PickWithFilter(ctx, providerType, model, nil, credentialmgr.Filter{Source: credentialmgr.SourceCLIAuth})
	if err != nil {
		return ctx, nil
	}
	if cred == nil {
		return ctx, nil
	}
	return WithCredential(ctx, cred), cred
}

func (p *authManagedProvider) markResult(ctx context.Context, cred *credentialmgr.Credential, model string, err error) {
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
		var se StatusError
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
	switch cfg.AuthStrategy {
	case AuthStrategyAPIKeyFirst, AuthStrategyAPIKeyOnly, "":
		attrs["priority"] = "1"
	case AuthStrategyCredentialFirst:
		attrs["priority"] = "-1"
	}
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
