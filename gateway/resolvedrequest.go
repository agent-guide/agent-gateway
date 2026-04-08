package gateway

import (
	localapikeypkg "github.com/agent-guide/caddy-agent-gateway/gateway/localapikey"
	routepkg "github.com/agent-guide/caddy-agent-gateway/gateway/route"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
)

// ResolvedRequest contains the route, consumer, and provider selected for a request.
type ResolvedRequest struct {
	Route        routepkg.Route
	LocalAPIKey  *localapikeypkg.LocalAPIKey
	ProviderName string
	Provider     provider.Provider
}
