package provider

import (
	"context"
	"strings"

	"github.com/agent-guide/agent-gateway/pkg/llm/credentialmgr"
)

const (
	providerConfigAPIKeyCredentialIDPrefix = "provider-config-api-key:"
)

func ProviderConfigAPIKeyCredential(cfg ProviderConfig, providerID string) *credentialmgr.Credential {
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
	attrs["scope"] = credentialmgr.ProviderIDCredentialScope(providerID)
	return &credentialmgr.Credential{
		ID:           ProviderConfigAPIKeyCredentialID(cfg),
		ProviderType: providerType,
		ProviderID:   providerID,
		Source:       credentialmgr.SourceAPIKey,
		Attributes:   attrs,
	}
}

func ProviderConfigAPIKeyCredentialID(cfg ProviderConfig) string {
	id := strings.TrimSpace(cfg.Id)
	if id == "" {
		id = strings.TrimSpace(cfg.ProviderType)
	}
	if id == "" {
		id = "default"
	}
	return providerConfigAPIKeyCredentialIDPrefix + id
}

func ProviderConfigAPIKeyCredentialProviderID(credentialID string) (string, bool) {
	credentialID = strings.TrimSpace(credentialID)
	if credentialID == "" {
		return "", false
	}
	if !strings.HasPrefix(credentialID, providerConfigAPIKeyCredentialIDPrefix) {
		return "", false
	}
	providerID := strings.TrimSpace(strings.TrimPrefix(credentialID, providerConfigAPIKeyCredentialIDPrefix))
	if providerID == "" {
		return "", false
	}
	return providerID, true
}

func SyncProviderConfigAPIKeyCredential(ctx context.Context, mgr *credentialmgr.Manager, cfg ProviderConfig, providerID string) error {
	if mgr == nil {
		return nil
	}
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		providerID = strings.TrimSpace(cfg.Id)
	}
	if providerID == "" {
		return nil
	}

	credID := ProviderConfigAPIKeyCredentialID(ProviderConfig{Id: providerID, ProviderType: cfg.ProviderType})
	cred := ProviderConfigAPIKeyCredential(cfg, providerID)
	if cred == nil {
		if existing := mgr.GetCredential(credID); existing != nil {
			return mgr.DeregisterCredential(credentialmgr.WithSkipPersist(ctx), credID)
		}
		return nil
	}
	if existing := mgr.GetCredential(cred.ID); existing != nil {
		cred.CreatedAt = existing.CreatedAt
		return mgr.UpdateCredential(credentialmgr.WithSkipPersist(ctx), cred)
	}
	return mgr.RegisterCredential(credentialmgr.WithSkipPersist(ctx), cred)
}

func RemoveProviderConfigAPIKeyCredential(ctx context.Context, mgr *credentialmgr.Manager, providerID string) error {
	if mgr == nil {
		return nil
	}
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return nil
	}
	credID := ProviderConfigAPIKeyCredentialID(ProviderConfig{Id: providerID})
	if mgr.GetCredential(credID) == nil {
		return nil
	}
	return mgr.DeregisterCredential(credentialmgr.WithSkipPersist(ctx), credID)
}
