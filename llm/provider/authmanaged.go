package provider

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/agent-guide/caddy-agent-gateway/llm/cliauth"
	"github.com/agent-guide/caddy-agent-gateway/llm/credentialmgr"
	"github.com/cloudwego/eino/schema"
)

const staticAPIKeyCredentialIDPrefix = "provider-static-api-key:"

type authManagedProvider struct {
	base           Provider
	providerName   string
	cliauthManager *cliauth.Manager
	credentialMgr  *credentialmgr.Manager
	config         ProviderConfig
}

func WrapWithCredentialManager(base Provider, providerName string, credMgr *credentialmgr.Manager, cliauthMgr *cliauth.Manager) Provider {
	if base == nil || credMgr == nil {
		return base
	}
	cfg := base.Config()
	if cfg.ProviderName == "" {
		cfg.ProviderName = providerName
	}
	cfg.Defaults()

	p := &authManagedProvider{
		base:           base,
		providerName:   providerName,
		cliauthManager: cliauthMgr,
		credentialMgr:  credMgr,
		config:         cfg,
	}
	if cred := newStaticAPIKeyCredential(cfg); cred != nil {
		_ = p.registerStaticCred(context.Background(), cred)
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

	var cred *credentialmgr.Credential
	switch p.config.AuthStrategy {
	case AuthStrategyAPIKeyOnly:
		cred = p.pickStaticCredential(ctx, model)
	case AuthStrategyCredentialFirst:
		cred = p.pickManagedCredential(ctx, model)
		if cred == nil {
			cred = p.pickStaticCredential(ctx, model)
		}
	case AuthStrategyCredentialOnly:
		cred = p.pickManagedCredential(ctx, model)
	case AuthStrategyAPIKeyFirst:
		cred = p.pickStaticCredential(ctx, model)
		if cred == nil {
			cred = p.pickManagedCredential(ctx, model)
		}
	default:
		cred = p.pickStaticCredential(ctx, model)
		if cred == nil {
			cred = p.pickManagedCredential(ctx, model)
		}
	}
	if cred == nil {
		return ctx, nil
	}
	return WithCredential(ctx, cred), cred
}

func (p *authManagedProvider) pickManagedCredential(ctx context.Context, model string) *credentialmgr.Credential {
	if p.credentialMgr == nil {
		return nil
	}
	cred, err := p.credentialMgr.PickWithFilter(ctx, p.providerName, model, nil, credentialmgr.Filter{Source: credentialmgr.SourceCLIAuth})
	if err != nil {
		return nil
	}
	return cred
}

// pickStaticCredential uses the static key scheduler to select an available key
// and returns the corresponding credential for context injection.
func (p *authManagedProvider) pickStaticCredential(ctx context.Context, model string) *credentialmgr.Credential {
	if p.credentialMgr == nil {
		return nil
	}
	cred, err := p.credentialMgr.PickWithFilter(ctx, p.providerName, model, nil, credentialmgr.Filter{Source: credentialmgr.SourceAPIKey})
	if err != nil || cred == nil {
		return nil
	}
	return cred
}

func (p *authManagedProvider) markResult(ctx context.Context, cred *credentialmgr.Credential, model string, err error) {
	if cred == nil {
		return
	}

	result := cliauth.Result{
		CredentialID: cred.ID,
		Provider:     cred.Provider,
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
	if isStaticAPIKeyCredential(cred) {
		p.markCredentialResult(ctx, result)
		return
	}
	if p.cliauthManager != nil && cred.Source == credentialmgr.SourceCLIAuth {
		p.cliauthManager.MarkResult(ctx, result)
		return
	}
	p.markCredentialResult(ctx, result)
}

func (p *authManagedProvider) markCredentialResult(ctx context.Context, result cliauth.Result) {
	if p.credentialMgr == nil {
		return
	}
	p.credentialMgr.MarkResult(ctx, credentialmgr.Result{
		CredentialID: result.CredentialID,
		Provider:     result.Provider,
		Model:        result.Model,
		Success:      result.Success,
		RetryAfter:   result.RetryAfter,
		Error:        result.Error,
	})
}

func (p *authManagedProvider) registerStaticCred(ctx context.Context, cred *credentialmgr.Credential) error {
	if p.credentialMgr == nil || cred == nil {
		return nil
	}
	return p.credentialMgr.RegisterCredential(ctx, cred)
}

func newStaticAPIKeyCredential(cfg ProviderConfig) *credentialmgr.Credential {
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		return nil
	}
	cfg.Defaults()
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
		ID:         staticAPIKeyCredentialID(cfg),
		Provider:   cfg.ProviderName,
		Source:     credentialmgr.SourceAPIKey,
		Attributes: attrs,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

func staticAPIKeyCredentialID(cfg ProviderConfig) string {
	id := strings.TrimSpace(cfg.Id)
	if id == "" {
		id = strings.TrimSpace(cfg.ProviderName)
	}
	if id == "" {
		id = "default"
	}
	return staticAPIKeyCredentialIDPrefix + id
}

func isStaticAPIKeyCredential(cred *credentialmgr.Credential) bool {
	return cred != nil && strings.HasPrefix(cred.ID, staticAPIKeyCredentialIDPrefix)
}
