package cliauthstore

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/agent-guide/agent-gateway/pkg/adminclient"
	configstoreintf "github.com/agent-guide/agent-gateway/pkg/configstore/intf"
	"github.com/agent-guide/agent-gateway/pkg/llm/credentialmgr"
)

type Config struct {
	BaseURL  string
	Username string
	Password string
}

type Manager struct {
	client *adminclient.Client
}

func New(cfg Config) (*Manager, error) {
	client := adminclient.New(adminclient.Config{
		BaseURL:  strings.TrimSpace(cfg.BaseURL),
		Username: strings.TrimSpace(cfg.Username),
		Password: cfg.Password,
	})
	if client == nil {
		return nil, fmt.Errorf("cliauth store: admin client is nil")
	}
	return &Manager{client: client}, nil
}

func (m *Manager) GetCredential(id string) *credentialmgr.Credential {
	cred, _ := m.GetCredentialWithError(id)
	return cred
}

func (m *Manager) GetCredentialWithError(id string) (*credentialmgr.Credential, error) {
	if m == nil || m.client == nil {
		return nil, nil
	}
	item, err := m.client.GetCredential(context.Background(), strings.TrimSpace(id))
	if err != nil {
		return nil, err
	}
	cred := credentialFromAdminView(item)
	if cred == nil || cred.Source != credentialmgr.SourceCLIAuthToken {
		return nil, nil
	}
	return cred, nil
}

func (m *Manager) ListCredentials(filter credentialmgr.Filter) []*credentialmgr.Credential {
	items, _ := m.ListCredentialsWithError(filter)
	return items
}

func (m *Manager) ListCredentialsWithError(filter credentialmgr.Filter) ([]*credentialmgr.Credential, error) {
	if m == nil || m.client == nil {
		return nil, nil
	}

	filter = normalizeFilter(filter)
	if filter.Source != "" && filter.Source != credentialmgr.SourceCLIAuthToken {
		return nil, nil
	}

	items, err := m.client.ListCredentials(context.Background(), adminclient.CredentialListOptions{
		ProviderType: filter.ProviderType,
		ProviderID:   filter.ProviderID,
		Source:       credentialmgr.SourceCLIAuthToken,
	})
	if err != nil {
		return nil, err
	}

	out := make([]*credentialmgr.Credential, 0, len(items))
	for _, item := range items {
		cred := credentialFromAdminView(&item)
		if cred == nil || !matchesFilter(cred, filter) {
			continue
		}
		out = append(out, cred)
	}
	return out, nil
}

func (m *Manager) RegisterCredential(ctx context.Context, cred *credentialmgr.Credential) error {
	if m == nil {
		return fmt.Errorf("credential manager: manager is nil")
	}
	if cred == nil {
		return fmt.Errorf("credential manager: credential is nil")
	}

	normalized := cred.Clone().Normalize()
	if normalized.ProviderType == "" {
		return fmt.Errorf("credential manager: provider type is required")
	}
	if normalized.ProviderID == "" {
		normalized.ProviderID = normalized.ProviderType
	}

	managed, err := m.client.CreateCredential(ctx, createRequestFromCredential(normalized))
	if err != nil {
		return mapAdminClientError(err)
	}
	if managed != nil {
		cred.ID = managed.ID
		cred.ProviderType = managed.ProviderType
		cred.ProviderID = managed.ProviderID
		cred.Source = managed.Source
		cred.Label = managed.Label
		cred.Attributes = cloneStringMap(managed.Attributes)
		cred.Metadata = cloneAnyMap(managed.Metadata)
		cred.Disabled = managed.Disabled
		cred.CreatedAt = managed.CreatedAt
		cred.UpdatedAt = managed.UpdatedAt
	}
	return nil
}

func (m *Manager) UpdateCredential(ctx context.Context, cred *credentialmgr.Credential) error {
	if m == nil {
		return fmt.Errorf("credential manager: manager is nil")
	}
	if cred == nil {
		return fmt.Errorf("credential manager: credential is nil")
	}

	normalized := cred.Clone().Normalize()
	if normalized.ID == "" {
		return fmt.Errorf("credential manager: credential id is required")
	}

	managed, err := m.client.UpdateCredential(ctx, normalized.ID, updateRequestFromCredential(normalized))
	if err != nil {
		return mapAdminClientError(err)
	}
	if managed != nil {
		cred.ProviderType = managed.ProviderType
		cred.ProviderID = managed.ProviderID
		cred.Source = managed.Source
		cred.Label = managed.Label
		cred.Attributes = cloneStringMap(managed.Attributes)
		cred.Metadata = cloneAnyMap(managed.Metadata)
		cred.Disabled = managed.Disabled
		cred.CreatedAt = managed.CreatedAt
		cred.UpdatedAt = managed.UpdatedAt
	}
	return nil
}

func (m *Manager) DeregisterCredential(ctx context.Context, id string) error {
	if m == nil {
		return nil
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("credential manager: credential id is required")
	}
	if _, err := m.client.DeleteCredential(ctx, id); err != nil {
		return mapAdminClientError(err)
	}
	return nil
}

func DefaultGatewayAddr() string {
	return "http://localhost:8019"
}

func normalizeFilter(filter credentialmgr.Filter) credentialmgr.Filter {
	filter.Source = strings.ToLower(strings.TrimSpace(filter.Source))
	filter.ProviderType = strings.ToLower(strings.TrimSpace(filter.ProviderType))
	filter.ProviderID = strings.ToLower(strings.TrimSpace(filter.ProviderID))
	filter.Model = strings.TrimSpace(filter.Model)
	return filter
}

func matchesFilter(cred *credentialmgr.Credential, filter credentialmgr.Filter) bool {
	if cred == nil {
		return false
	}
	if filter.Source != "" && strings.ToLower(cred.Source) != filter.Source {
		return false
	}
	if filter.ProviderType != "" && strings.ToLower(cred.ProviderType) != filter.ProviderType {
		return false
	}
	if filter.ProviderID != "" && strings.ToLower(cred.ProviderID) != filter.ProviderID {
		return false
	}
	if filter.Model != "" {
		model := ""
		if cred.Attributes != nil {
			model = strings.TrimSpace(cred.Attributes["model"])
		}
		if model == "" || model != filter.Model {
			return false
		}
	}
	return true
}

func credentialFromAdminView(item *adminclient.Credential) *credentialmgr.Credential {
	if item == nil {
		return nil
	}
	return (&credentialmgr.Credential{
		ID:           item.ID,
		ProviderType: item.ProviderType,
		ProviderID:   item.ProviderID,
		Source:       item.Source,
		Label:        item.Label,
		Attributes:   cloneStringMap(item.Attributes),
		Metadata:     cloneAnyMap(item.Metadata),
		Disabled:     item.Disabled,
		CreatedAt:    item.CreatedAt,
		UpdatedAt:    item.UpdatedAt,
	}).Normalize()
}

func createRequestFromCredential(cred *credentialmgr.Credential) adminclient.CreateCredentialRequest {
	if cred == nil {
		return adminclient.CreateCredentialRequest{}
	}
	return adminclient.CreateCredentialRequest{
		ID:           cred.ID,
		Source:       cred.Source,
		ProviderType: cred.ProviderType,
		ProviderID:   cred.ProviderID,
		Label:        cred.Label,
		Attributes:   cloneStringMap(cred.Attributes),
		Metadata:     cloneAnyMap(cred.Metadata),
		Disabled:     cred.Disabled,
		CreatedAt:    cred.CreatedAt,
		UpdatedAt:    cred.UpdatedAt,
	}
}

func updateRequestFromCredential(cred *credentialmgr.Credential) adminclient.UpdateCredentialRequest {
	if cred == nil {
		return adminclient.UpdateCredentialRequest{}
	}
	return adminclient.UpdateCredentialRequest{
		Source:       cred.Source,
		ProviderType: cred.ProviderType,
		ProviderID:   cred.ProviderID,
		Label:        cred.Label,
		Attributes:   cloneStringMap(cred.Attributes),
		Metadata:     cloneAnyMap(cred.Metadata),
		Disabled:     cred.Disabled,
		CreatedAt:    cred.CreatedAt,
		UpdatedAt:    cred.UpdatedAt,
	}
}

func mapAdminClientError(err error) error {
	var adminErr *adminclient.Error
	if !errors.As(err, &adminErr) {
		return err
	}
	if adminErr.StatusCode == 404 {
		return configstoreintf.ErrNotFound
	}
	return err
}

func cloneStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func cloneAnyMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
