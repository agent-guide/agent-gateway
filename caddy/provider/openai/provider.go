// Package openai adapts the OpenAI runtime provider to Caddy.
package openai

import (
	"context"

	caddyprovider "github.com/agent-guide/caddy-agent-gateway/caddy/provider"
	runtimeprovider "github.com/agent-guide/caddy-agent-gateway/pkg/llm/provider"
	runtimeopenai "github.com/agent-guide/caddy-agent-gateway/pkg/llm/provider/openai"
	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/cloudwego/eino/schema"
)

func init() {
	caddy.RegisterModule(Provider{})
}

type Provider struct {
	caddyprovider.RuntimeAdapter
}

func (Provider) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "llm.providers.openai",
		New: func() caddy.Module { return new(Provider) },
	}
}

func (p *Provider) Provision(_ caddy.Context) error {
	if err := caddyprovider.ValidateProviderType(&p.ProviderConfig, "openai"); err != nil {
		return err
	}
	built, err := runtimeopenai.New(p.ProviderConfig)
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

func (p *Provider) CreateResponses(ctx context.Context, req *runtimeprovider.ResponsesRequest) (*runtimeprovider.ResponsesResponse, error) {
	return p.Runtime.(runtimeprovider.ResponsesProvider).CreateResponses(ctx, req)
}

func (p *Provider) StreamResponses(ctx context.Context, req *runtimeprovider.ResponsesRequest) (*schema.StreamReader[*runtimeprovider.ResponsesStreamEvent], error) {
	return p.Runtime.(runtimeprovider.ResponsesProvider).StreamResponses(ctx, req)
}

var (
	_ caddy.Provisioner                 = (*Provider)(nil)
	_ caddyfile.Unmarshaler             = (*Provider)(nil)
	_ runtimeprovider.Provider          = (*Provider)(nil)
	_ runtimeprovider.EmbeddingProvider = (*Provider)(nil)
	_ runtimeprovider.ResponsesProvider = (*Provider)(nil)
)
