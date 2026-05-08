package cliauth

import (
	"context"

	"github.com/agent-guide/caddy-agent-gateway/pkg/llm/credentialmgr"
)

// CredentialManager captures the credential store/snapshot operations cliauth
// needs from the shared credential manager.
type CredentialManager interface {
	GetCredential(id string) *credentialmgr.Credential
	ListCredentials(filter credentialmgr.Filter) []*credentialmgr.Credential
	RegisterCredential(ctx context.Context, cred *credentialmgr.Credential) error
	UpdateCredential(ctx context.Context, cred *credentialmgr.Credential) error
	DeregisterCredential(ctx context.Context, id string) error
}

type sharedCredentialManager struct {
	manager *credentialmgr.Manager
}

// WrapSharedCredentialManager adapts the shared credential manager to the
// cliauth-focused CredentialManager interface.
func WrapSharedCredentialManager(manager *credentialmgr.Manager) CredentialManager {
	if manager == nil {
		return nil
	}
	return &sharedCredentialManager{manager: manager}
}

func (m *sharedCredentialManager) GetCredential(id string) *credentialmgr.Credential {
	if m == nil || m.manager == nil {
		return nil
	}
	cred := m.manager.GetCredential(id)
	if cred == nil {
		return nil
	}
	return cred.Credential.Clone()
}

func (m *sharedCredentialManager) ListCredentials(filter credentialmgr.Filter) []*credentialmgr.Credential {
	if m == nil || m.manager == nil {
		return nil
	}
	items := m.manager.ListCredentials(filter)
	out := make([]*credentialmgr.Credential, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		out = append(out, item.Credential.Clone())
	}
	return out
}

func (m *sharedCredentialManager) RegisterCredential(ctx context.Context, cred *credentialmgr.Credential) error {
	if m == nil || m.manager == nil {
		return nil
	}
	return m.manager.RegisterCredential(ctx, cred)
}

func (m *sharedCredentialManager) UpdateCredential(ctx context.Context, cred *credentialmgr.Credential) error {
	if m == nil || m.manager == nil {
		return nil
	}
	return m.manager.UpdateCredential(ctx, cred)
}

func (m *sharedCredentialManager) DeregisterCredential(ctx context.Context, id string) error {
	if m == nil || m.manager == nil {
		return nil
	}
	return m.manager.DeregisterCredential(ctx, id)
}
