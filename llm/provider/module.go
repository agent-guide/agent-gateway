package provider

import (
	"fmt"
	"strconv"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
)

// Module is a provider implementation that can be loaded through Caddy's module system.
type Module interface {
	caddy.Module
	Provider
}

// UnmarshalCaddyfileConfig parses common provider settings from a Caddyfile block.
func UnmarshalCaddyfileConfig(d *caddyfile.Dispenser, cfg *ProviderConfig) error {
	for d.Next() {
		if cfg.Id == "" {
			cfg.Id = d.Val()
		}
		for d.NextBlock(0) {
			switch d.Val() {
			case "provider_name":
				if !d.NextArg() {
					return d.ArgErr()
				}
				cfg.ProviderName = d.Val()
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
			case "timeout_seconds":
				if !d.NextArg() {
					return d.ArgErr()
				}
				v, err := strconv.Atoi(d.Val())
				if err != nil {
					return err
				}
				cfg.Network.TimeoutSeconds = v
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

// ValidateProviderName ensures the provider config name matches the mounted module name.
func ValidateProviderName(cfg *ProviderConfig, expected string) error {
	if cfg.ProviderName == "" {
		cfg.ProviderName = expected
		return nil
	}
	if cfg.ProviderName != expected {
		return fmt.Errorf("provider_name must be %q, got %q", expected, cfg.ProviderName)
	}
	return nil
}
