package provider

import (
	"context"
	"fmt"
	"strconv"

	runtimeprovider "github.com/agent-guide/caddy-agent-gateway/pkg/llm/provider"
	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/cloudwego/eino/schema"
)

// Module is a provider implementation that can be loaded through Caddy's module system.
type Module interface {
	caddy.Module
	runtimeprovider.Provider
}

// RuntimeAdapter delegates provider runtime calls to a pkg/llm/provider implementation.
type RuntimeAdapter struct {
	runtimeprovider.ProviderConfig
	Runtime runtimeprovider.Provider `json:"-"`
}

func (a *RuntimeAdapter) Chat(ctx context.Context, req *runtimeprovider.ChatRequest) (*runtimeprovider.ChatResponse, error) {
	return a.Runtime.Chat(ctx, req)
}

func (a *RuntimeAdapter) StreamChat(ctx context.Context, req *runtimeprovider.ChatRequest) (*schema.StreamReader[*schema.Message], error) {
	return a.Runtime.StreamChat(ctx, req)
}

func (a *RuntimeAdapter) ListModels(ctx context.Context) ([]runtimeprovider.ModelInfo, error) {
	return a.Runtime.ListModels(ctx)
}

func (a *RuntimeAdapter) Capabilities() runtimeprovider.ProviderCapabilities {
	return a.Runtime.Capabilities()
}

func (a *RuntimeAdapter) Config() runtimeprovider.ProviderConfig {
	if a.Runtime == nil {
		return a.ProviderConfig
	}
	return a.Runtime.Config()
}

// UnmarshalCaddyfileConfig parses common provider settings from a Caddyfile block.
func UnmarshalCaddyfileConfig(d *caddyfile.Dispenser, cfg *runtimeprovider.ProviderConfig) error {
	for d.Next() {
		if cfg.Id == "" {
			cfg.Id = d.Val()
		}
		for d.NextBlock(0) {
			switch d.Val() {
			case "provider_type":
				if !d.NextArg() {
					return d.ArgErr()
				}
				cfg.ProviderType = d.Val()
			case "api_key":
				if !d.NextArg() {
					return d.ArgErr()
				}
				cfg.APIKey = d.Val()
			case "base_url":
				if !d.NextArg() {
					return d.ArgErr()
				}
				cfg.BaseURL = d.Val()
			case "default_model":
				if !d.NextArg() {
					return d.ArgErr()
				}
				cfg.DefaultModel = d.Val()
			case "request_timeout_seconds":
				if !d.NextArg() {
					return d.ArgErr()
				}
				v, err := strconv.Atoi(d.Val())
				if err != nil {
					return err
				}
				cfg.Network.RequestTimeoutSeconds = v
			case "max_retries":
				if !d.NextArg() {
					return d.ArgErr()
				}
				v, err := strconv.Atoi(d.Val())
				if err != nil {
					return err
				}
				cfg.Network.MaxRetries = v
			case "retry_delay_seconds":
				if !d.NextArg() {
					return d.ArgErr()
				}
				v, err := strconv.Atoi(d.Val())
				if err != nil {
					return err
				}
				cfg.Network.RetryDelaySeconds = v
			case "max_idle_connections":
				if !d.NextArg() {
					return d.ArgErr()
				}
				v, err := strconv.Atoi(d.Val())
				if err != nil {
					return err
				}
				cfg.Network.MaxIdleConnections = v
			case "max_idle_connections_per_host":
				if !d.NextArg() {
					return d.ArgErr()
				}
				v, err := strconv.Atoi(d.Val())
				if err != nil {
					return err
				}
				cfg.Network.MaxIdleConnectionsPerHost = v
			case "idle_keep_alive_timeout_seconds":
				if !d.NextArg() {
					return d.ArgErr()
				}
				v, err := strconv.Atoi(d.Val())
				if err != nil {
					return err
				}
				cfg.Network.IdleKeepAliveTimeoutSeconds = v
			case "proxy_url":
				if !d.NextArg() {
					return d.ArgErr()
				}
				cfg.Network.ProxyURL = d.Val()
			case "header":
				args := d.RemainingArgs()
				if len(args) != 2 {
					return d.ArgErr()
				}
				if cfg.Network.ExtraHeaders == nil {
					cfg.Network.ExtraHeaders = make(map[string]string)
				}
				cfg.Network.ExtraHeaders[args[0]] = args[1]
			case "option":
				args := d.RemainingArgs()
				if len(args) != 2 {
					return d.ArgErr()
				}
				if cfg.Options == nil {
					cfg.Options = make(map[string]any)
				}
				cfg.Options[args[0]] = args[1]
			default:
				return d.Errf("unknown subdirective: %s", d.Val())
			}
		}
	}
	return nil
}

// ValidateProviderType ensures the provider config name matches the mounted module name.
func ValidateProviderType(cfg *runtimeprovider.ProviderConfig, expected string) error {
	if cfg.ProviderType == "" {
		cfg.ProviderType = expected
		return nil
	}
	if cfg.ProviderType != expected {
		return fmt.Errorf("provider_type must be %q, got %q", expected, cfg.ProviderType)
	}
	return nil
}
