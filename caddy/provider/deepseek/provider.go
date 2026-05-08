// Package deepseek adapts the DeepSeek runtime provider to Caddy.
package deepseek

import (
	"context"

	caddyprovider "github.com/agent-guide/caddy-agent-gateway/caddy/provider"
	runtimeprovider "github.com/agent-guide/caddy-agent-gateway/pkg/llm/provider"
	runtimedeepseek "github.com/agent-guide/caddy-agent-gateway/pkg/llm/provider/deepseek"
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
		ID:  "llm.providers.deepseek",
		New: func() caddy.Module { return new(Provider) },
	}
}

func (p *Provider) Provision(_ caddy.Context) error {
	if err := caddyprovider.ValidateProviderType(&p.ProviderConfig, "deepseek"); err != nil {
		return err
	}
	built, err := runtimedeepseek.New(p.ProviderConfig)
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

func (p *Provider) Embedding(ctx context.Context, req *runtimeprovider.EmbeddingRequest) (*runtimeprovider.EmbeddingResponse, error) {
	return p.Runtime.(runtimeprovider.EmbeddingProvider).Embedding(ctx, req)
}

var (
	_ caddy.Provisioner                 = (*Provider)(nil)
	_ caddyfile.Unmarshaler             = (*Provider)(nil)
	_ runtimeprovider.Provider          = (*Provider)(nil)
	_ runtimeprovider.EmbeddingProvider = (*Provider)(nil)
)
