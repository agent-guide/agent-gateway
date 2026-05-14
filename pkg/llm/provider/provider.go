package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/agent-guide/agent-gateway/pkg/httpclient"
	"github.com/cloudwego/eino/schema"
)

// Provider is the core interface implemented by all LLM providers.
// Additional capabilities are exposed through optional interfaces such as
// EmbeddingProvider and ResponsesProvider.
type Provider interface {
	// Chat performs a non-streaming chat completion and returns the full response.
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)

	// StreamChat performs a streaming chat completion and returns an Eino message stream.
	StreamChat(ctx context.Context, req *ChatRequest) (*schema.StreamReader[*schema.Message], error)

	// ListModels returns the list of models available from this provider.
	ListModels(ctx context.Context) ([]ModelInfo, error)

	// Capabilities returns what this provider instance supports.
	Capabilities() ProviderCapabilities

	// Config returns the provider instance configuration view used at runtime.
	Config() ProviderConfig
}

// LLMApiRequestType identifies the prepared provider-facing request kind.
type LLMApiRequestType string

const (
	LLMApiRequestTypeChat      LLMApiRequestType = "chat"
	LLMApiRequestTypeEmbedding LLMApiRequestType = "embedding"
	LLMApiRequestTypeResponses LLMApiRequestType = "responses"
)

// Configuration and capability types.

// ProviderCapabilities describes what a provider instance supports.
type ProviderCapabilities struct {
	Streaming       bool
	Tools           bool
	Vision          bool
	Embeddings      bool
	ContextWindow   int
	MaxOutputTokens int
}

// ModelCapabilities describes what one specific upstream model supports.
// It is the routing authority for model-catalog decisions.
type ModelCapabilities struct {
	Streaming       bool `json:"streaming,omitempty"`
	Tools           bool `json:"tools,omitempty"`
	Vision          bool `json:"vision,omitempty"`
	Embeddings      bool `json:"embeddings,omitempty"`
	ContextWindow   int  `json:"context_window,omitempty"`
	MaxOutputTokens int  `json:"max_output_tokens,omitempty"`
}

// ModelCapabilitiesFromProviderSummary converts coarse provider-level capability
// metadata into a per-model fallback shape.
func ModelCapabilitiesFromProviderSummary(c ProviderCapabilities) ModelCapabilities {
	return ModelCapabilities{
		Streaming:       c.Streaming,
		Tools:           c.Tools,
		Vision:          c.Vision,
		Embeddings:      c.Embeddings,
		ContextWindow:   c.ContextWindow,
		MaxOutputTokens: c.MaxOutputTokens,
	}
}

// ProviderConfig contains configuration for a provider instance.
type ProviderConfig struct {
	// Id is the unique provider config ID.
	Id string `json:"id"`
	// ProviderType is the registered provider type (e.g. "openai", "anthropic").
	ProviderType string `json:"provider_type"`
	// Disabled prevents the provider from being selected at runtime.
	Disabled bool `json:"disabled"`
	// APIKey is the provider API key. May be empty for local providers (Ollama).
	APIKey string `json:"api_key,omitempty"`
	// BaseURL overrides the provider's default API base URL.
	BaseURL string `json:"base_url,omitempty"`
	// DefaultModel is used when the request does not specify a model.
	DefaultModel string `json:"default_model,omitempty"`
	// Network contains HTTP client configuration (timeout, retry, proxy).
	Network NetworkConfig `json:"network"`
	// Options holds provider-specific extra configuration.
	Options map[string]any `json:"options,omitempty"`
}

// NetworkConfig re-exports the shared HTTP network config type for provider configs.
type NetworkConfig = httpclient.NetworkConfig

// ProviderConfig helpers.

// Defaults fills in zero values with sensible defaults.
func (c *ProviderConfig) Defaults() {
	c.Network.Defaults()
}

// NormalizeConfig returns a runtime-ready provider config without mutating the
// source value. If ProviderType is empty, fallbackName is applied before defaults.
func NormalizeConfig(cfg ProviderConfig, fallbackId string, fallbackName string) ProviderConfig {
	if cfg.Id == "" {
		cfg.Id = fallbackId
	}
	if cfg.ProviderType == "" {
		cfg.ProviderType = fallbackName
	}
	cfg.Defaults()
	return cfg
}

// NormalizeStoredProviderConfig converts a decoded config-store object into a
// runtime-ready ProviderConfig without mutating the decoded object.
func NormalizeStoredProviderConfig(fallbackId string, fallbackName string, obj any) (ProviderConfig, error) {
	cfg, ok := obj.(*ProviderConfig)
	if !ok || cfg == nil {
		if fallbackId == "" {
			fallbackId = "<unknown>"
		}
		return ProviderConfig{}, fmt.Errorf("stored provider %q has unexpected type %T", fallbackId, obj)
	}

	return NormalizeConfig(*cfg, fallbackId, fallbackName), nil
}

// DecodeStoredProviderConfig converts a config-store provider payload into ProviderConfig.
func DecodeStoredProviderConfig(data []byte) (any, error) {
	var providerConfig ProviderConfig
	if err := json.Unmarshal(data, &providerConfig); err != nil {
		return nil, err
	}
	return &providerConfig, nil
}
