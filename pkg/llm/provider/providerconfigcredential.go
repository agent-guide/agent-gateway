package provider

import (
	"strings"
	"time"

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
	now := time.Now().UTC()
	return &credentialmgr.Credential{
		ID:           ProviderConfigAPIKeyCredentialID(cfg),
		ProviderType: providerType,
		ProviderID:   providerID,
		Source:       credentialmgr.SourceAPIKey,
		Attributes:   attrs,
		CreatedAt:    now,
		UpdatedAt:    now,
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
