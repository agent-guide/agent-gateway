package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// Provider defines the interface for LLM providers.
//
// Design follows CLIProxyAPI's approach: a small, focused interface covering
// the core operations every provider must support. Providers that support
// additional capabilities (embeddings, image generation, etc.) may implement
// optional capability interfaces (EmbeddingProvider, etc.).
type Provider interface {
	// Generate performs a non-streaming completion and returns the full response.
	Generate(ctx context.Context, req *GenerateRequest) (*GenerateResponse, error)

	// Stream performs a streaming completion and returns an Eino message stream.
	Stream(ctx context.Context, req *GenerateRequest) (*schema.StreamReader[*schema.Message], error)

	// ListModels returns the list of models available from this provider.
	ListModels(ctx context.Context) ([]ModelInfo, error)

	// Capabilities returns what this provider instance supports.
	Capabilities() ProviderCapabilities

	// Config returns the provider instance configuration view used at runtime.
	Config() ProviderConfig
}

// EmbeddingProvider is an optional interface for providers that support embeddings.
// The memory module uses this to generate vectors for storage and search.
type EmbeddingProvider interface {
	Provider
	Embed(ctx context.Context, req *EmbedRequest) (*EmbedResponse, error)
}

// StatusError is implemented by errors that carry an HTTP status code.
// Provider implementations should return StatusError so that the handler layer
// can make informed retry and degradation decisions (e.g. 401→disable key,
// 429→backoff, 503→try next provider).
type StatusError interface {
	error
	StatusCode() int
}

// AuthStrategy controls how a provider instance chooses between static API keys
// and managed credentials for upstream requests.
type AuthStrategy string

const (
	AuthStrategyAPIKeyFirst     AuthStrategy = "api_key_first"
	AuthStrategyCredentialFirst AuthStrategy = "credential_first"
	AuthStrategyCredentialOnly  AuthStrategy = "credential_only"
	AuthStrategyAPIKeyOnly      AuthStrategy = "api_key_only"
)

// ProviderCapabilities describes what a provider instance supports.
type ProviderCapabilities struct {
	Streaming       bool
	Tools           bool
	Vision          bool
	Embeddings      bool
	ContextWindow   int
	MaxOutputTokens int
}

// ProviderConfig contains configuration for a provider instance.
type ProviderConfig struct {
	// Id is the unique provider config ID.
	Id string `json:"id"`
	// ProviderName is the registered provider name (e.g. "openai", "anthropic").
	ProviderName string `json:"provider_name"`
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
	// AuthStrategy controls how static API keys and managed credentials are combined.
	AuthStrategy AuthStrategy `json:"auth_strategy,omitempty"`
}

// NetworkConfig controls HTTP client behavior for a provider.
// Borrowed from Bifrost's NetworkConfig, simplified for our needs.
type NetworkConfig struct {
	// TimeoutSeconds is the per-request HTTP timeout. Default: 120.
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
	// MaxRetries is the number of automatic retries on transient errors. Default: 3.
	MaxRetries int `json:"max_retries,omitempty"`
	// RetryDelaySeconds is the base delay between retries. Default: 1.
	RetryDelaySeconds int `json:"retry_delay_seconds,omitempty"`
	// ProxyURL is an optional HTTP/HTTPS/SOCKS5 proxy URL.
	ProxyURL string `json:"proxy_url,omitempty"`
	// ExtraHeaders are additional HTTP headers sent with every provider request.
	ExtraHeaders map[string]string `json:"extra_headers,omitempty"`
}

// Defaults fills in zero values with sensible defaults.
func (c *NetworkConfig) Defaults() {
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = 120
	}
	if c.MaxRetries == 0 {
		c.MaxRetries = 3
	}
	if c.RetryDelaySeconds == 0 {
		c.RetryDelaySeconds = 1
	}
}

// Timeout returns the configured timeout as a time.Duration.
func (c *NetworkConfig) Timeout() time.Duration {
	if c.TimeoutSeconds == 0 {
		return 120 * time.Second
	}
	return time.Duration(c.TimeoutSeconds) * time.Second
}

// Defaults fills in zero values with sensible defaults.
func (c *ProviderConfig) Defaults() {
	c.Network.Defaults()
	if c.AuthStrategy == "" {
		c.AuthStrategy = AuthStrategyAPIKeyFirst
	}
}

// NormalizeConfig returns a runtime-ready provider config without mutating the
// source value. If ProviderName is empty, fallbackName is applied before defaults.
func NormalizeConfig(cfg ProviderConfig, fallbackId string, fallbackName string) ProviderConfig {
	if cfg.Id == "" {
		cfg.Id = fallbackId
	}
	if cfg.ProviderName == "" {
		cfg.ProviderName = fallbackName
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

// --- Request / Response types ---

// GenerateRequest is the unified internal request format passed to providers.
type GenerateRequest struct {
	Model    string
	Messages []*schema.Message
	Options  []einomodel.Option
}

// GenerateResponse is the unified internal response format returned by providers.
type GenerateResponse struct {
	Message *schema.Message
}

// EmbedRequest is the request to generate vector embeddings.
type EmbedRequest struct {
	// Model is the embedding model to use. Leave empty to use provider default.
	Model string
	// Texts are the strings to embed.
	Texts []string
}

// EmbedResponse contains the generated embeddings.
type EmbedResponse struct {
	Embeddings [][]float64
	Model      string
	Usage      Usage
}

// ModelInfo describes a model available from a provider.
type ModelInfo struct {
	ID           string
	Name         string
	Description  string
	Capabilities ProviderCapabilities
}

// Usage contains token consumption information.
type Usage struct {
	InputTokens  int
	OutputTokens int
}
