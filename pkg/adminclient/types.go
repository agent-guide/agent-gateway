package adminclient

import (
	adminapi "github.com/agent-guide/agent-gateway/pkg/admin"
	"github.com/agent-guide/agent-gateway/pkg/cliauth"
	"github.com/agent-guide/agent-gateway/pkg/gateway/modelcatalog"
	routepkg "github.com/agent-guide/agent-gateway/pkg/gateway/route"
	virtualkeypkg "github.com/agent-guide/agent-gateway/pkg/gateway/virtualkey"
	"github.com/agent-guide/agent-gateway/pkg/llm/credentialmgr"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

type Provider = adminapi.ProviderView
type ProviderType = adminapi.ProviderTypeView
type LLMAPIHandlerType = adminapi.LLMApiHandlerTypeView
type Route = adminapi.RouteView
type VirtualKey = adminapi.VirtualKeyView
type Credential = adminapi.CredentialView
type ManagedModel = adminapi.ManagedConcreteModelView
type DiscoveredModel = modelcatalog.ProviderModelSnapshot

type ProviderConfig = provider.ProviderConfig
type RouteConfig = routepkg.AgentRoute
type VirtualKeyConfig = virtualkeypkg.VirtualKey
type ManagedCredential = credentialmgr.ManagedCredential

type ProviderListOptions struct {
	ProviderType string
}

type RouteListOptions struct {
	Tag       string
	TagPrefix string
}

type VirtualKeyListOptions struct {
	Tag string
}

type CredentialListOptions struct {
	ProviderType string
	ProviderID   string
	Source       string
}

type CreateCredentialRequest struct {
	ProviderID string            `json:"provider_id"`
	Label      string            `json:"label,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

type UpdateCredentialRequest struct {
	Label      string            `json:"label,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
	Disabled   bool              `json:"disabled,omitempty"`
}

type CLIAuthAuthenticator struct {
	Name         string                      `json:"name"`
	ProviderType string                      `json:"provider_type,omitempty"`
	Enabled      bool                        `json:"enabled"`
	Config       cliauth.AuthenticatorConfig `json:"config"`
}

type UpdateCLIAuthAuthenticatorRequest struct {
	Enabled *bool                        `json:"enabled,omitempty"`
	Config  *cliauth.AuthenticatorConfig `json:"config,omitempty"`
}

type CLIAuthUpdateAuthenticatorResponse struct {
	Status        string               `json:"status"`
	Authenticator CLIAuthAuthenticator `json:"authenticator"`
}

type CLIAuthRefresherStatus struct {
	Enabled bool `json:"enabled"`
}

type CLIAuthLogin struct {
	LoginID           string `json:"login_id"`
	Status            string `json:"status"`
	AuthenticatorName string `json:"authenticator_name"`
	Message           string `json:"message,omitempty"`
}

type CLIAuthLoginStatus struct {
	LoginID           string `json:"login_id"`
	AuthenticatorName string `json:"authenticator_name"`
	Status            string `json:"status"`
	StartedAt         any    `json:"started_at,omitempty"`
	FinishedAt        any    `json:"finished_at,omitempty"`
	Phase             string `json:"phase,omitempty"`
	Message           string `json:"message,omitempty"`
	VerificationURL   string `json:"verification_url,omitempty"`
	UserCode          string `json:"user_code,omitempty"`
	Error             string `json:"error,omitempty"`
	CredentialID      string `json:"credential_id,omitempty"`
}

type itemsResponse[T any] struct {
	Items []T `json:"items"`
}
