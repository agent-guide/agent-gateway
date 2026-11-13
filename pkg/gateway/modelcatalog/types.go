package modelcatalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/agent-guide/agent-gateway/internal/strictjson"
	"strings"
	"time"

	credmodel "github.com/agent-guide/agent-gateway/pkg/credential/model"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

type SnapshotStatus string

const (
	SnapshotStatusOK    SnapshotStatus = "ok"
	SnapshotStatusError SnapshotStatus = "error"
)

type ProviderModelSnapshot struct {
	ProviderID    string                     `json:"provider_id"`
	ProviderType  string                     `json:"provider_type"`
	UpstreamModel string                     `json:"upstream_model"`
	DisplayName   string                     `json:"display_name,omitempty"`
	Description   string                     `json:"description,omitempty"`
	Capabilities  provider.ModelCapabilities `json:"capabilities,omitempty"`
	Status        SnapshotStatus             `json:"status"`
	FetchedAt     time.Time                  `json:"fetched_at"`
	LastError     string                     `json:"last_error,omitempty"`
}

type ProviderModel struct {
	ReadOnly            bool                        `json:"read_only,omitempty"`
	ProviderID          string                      `json:"provider_id"`
	UpstreamModel       string                      `json:"upstream_model"`
	CredentialScope     string                      `json:"credential_scope,omitempty"`
	Enabled             bool                        `json:"enabled"`
	CapabilityOverrides *provider.ModelCapabilities `json:"capability_overrides,omitempty"`
}

type ResolvedProviderModel struct {
	ProviderModel
	Snapshot     *ProviderModelSnapshot     `json:"snapshot,omitempty"`
	Capabilities provider.ModelCapabilities `json:"capabilities,omitempty"`
}

func (m *ProviderModel) Normalize() {
	if m == nil {
		return
	}
	m.ProviderID = strings.TrimSpace(m.ProviderID)
	m.UpstreamModel = strings.TrimSpace(m.UpstreamModel)
	if scope := credmodel.NormalizeCredentialScope(m.CredentialScope); scope != "" {
		m.CredentialScope = scope
	} else {
		m.CredentialScope = ""
	}
}

func (m *ProviderModel) ModelStorageKey() (string, string) {
	if m == nil {
		return "", ""
	}
	return m.ProviderID, m.UpstreamModel
}

func DecodeStoredProviderModel(data []byte) (any, error) {
	var model ProviderModel
	if err := decodeModelJSON(data, &model); err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	if value, ok := fields["enabled"]; !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return nil, fmt.Errorf("ProviderModel enabled must be an explicit boolean; repair existing data with scripts/repair-architecture-config")
	}
	model.Normalize()
	if err := model.Validate(); err != nil {
		return nil, err
	}
	return &model, nil
}

func (m ProviderModel) Validate() error {
	if m.ProviderID == "" || m.UpstreamModel == "" {
		return fmt.Errorf("provider_id and upstream_model are required")
	}
	return nil
}

func decodeModelJSON(data []byte, value any) error {
	if _, err := strictjson.Parse(data, 4<<20, 64); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}
