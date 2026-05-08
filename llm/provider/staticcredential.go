package provider

import (
	"strings"
	"time"

	"github.com/agent-guide/caddy-agent-gateway/llm/credentialmgr"
)

const staticAPIKeyCredentialIDPrefix = "provider-static-api-key:"

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
	attrs["scope"] = credentialmgr.ProviderIDCredentialScope(providerID)
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
