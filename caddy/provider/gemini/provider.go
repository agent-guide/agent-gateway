// Package gemini adapts the Gemini runtime provider to Caddy.
package gemini

import (
	caddyprovider "github.com/agent-guide/caddy-agent-gateway/caddy/provider"
	runtimeprovider "github.com/agent-guide/caddy-agent-gateway/pkg/llm/provider"
	runtimegemini "github.com/agent-guide/caddy-agent-gateway/pkg/llm/provider/gemini"
	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
)

func init() {
	caddy.RegisterModule(Provider{})
}

type Provider struct {
	caddyprovider.RuntimeAdapter
}

func (Provider) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "llm.providers.gemini",
		New: func() caddy.Module { return new(Provider) },
	}
}

func (p *Provider) Provision(_ caddy.Context) error {
	if err := caddyprovider.ValidateProviderType(&p.ProviderConfig, "gemini"); err != nil {
		return err
	}
	built, err := runtimegemini.New(p.ProviderConfig)
	if err != nil {
		return err
	}
	p.Runtime = built
	p.ProviderConfig = built.Config()
	return nil
}

func (p *Provider) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	return caddyprovider.UnmarshalCaddyfileConfig(d, &p.ProviderConfig)
}

var (
	_ caddy.Provisioner        = (*Provider)(nil)
	_ caddyfile.Unmarshaler    = (*Provider)(nil)
	_ runtimeprovider.Provider = (*Provider)(nil)
)
