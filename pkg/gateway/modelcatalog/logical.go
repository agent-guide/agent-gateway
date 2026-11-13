package modelcatalog

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/agent-guide/agent-gateway/pkg/configmgr"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
)

type SelectionStrategy string

const (
	SelectionAuto     SelectionStrategy = "auto"
	SelectionPriority SelectionStrategy = "priority"
	SelectionWeighted SelectionStrategy = "weighted"
)

type FallbackPolicy struct {
	Enabled bool `json:"enabled,omitempty"`
	MaxNum  int  `json:"max_num,omitempty"`
}

type ModelCandidate struct {
	ProviderID    string `json:"provider_id"`
	UpstreamModel string `json:"upstream_model"`
	Weight        int    `json:"weight,omitempty"`
	Priority      int    `json:"priority,omitempty"`
}

// LogicalModel owns model scheduling independently of any HTTP ingress.
type LogicalModel struct {
	ReadOnly    bool              `json:"read_only,omitempty"`
	ID          string            `json:"id"`
	Description string            `json:"description,omitempty"`
	Disabled    bool              `json:"disabled"`
	Strategy    SelectionStrategy `json:"strategy,omitempty"`
	Fallback    FallbackPolicy    `json:"fallback,omitempty"`
	Candidates  []ModelCandidate  `json:"candidates"`
}

func (m *LogicalModel) Normalize() {
	m.ID = strings.TrimSpace(m.ID)
	if m.Strategy == "" {
		m.Strategy = SelectionAuto
	}
	for i := range m.Candidates {
		m.Candidates[i].ProviderID = strings.TrimSpace(m.Candidates[i].ProviderID)
		m.Candidates[i].UpstreamModel = strings.TrimSpace(m.Candidates[i].UpstreamModel)
	}
}
func (m LogicalModel) Validate() error {
	if m.ID == "" || strings.Contains(m.ID, "/") {
		return fmt.Errorf("logical model id must be nonempty and slash-free")
	}
	switch m.Strategy {
	case "", SelectionAuto, SelectionPriority, SelectionWeighted:
	default:
		return fmt.Errorf("invalid strategy %q", m.Strategy)
	}
	if m.Fallback.MaxNum < 0 || m.Fallback.MaxNum > 5 {
		return fmt.Errorf("fallback.max_num must be between 0 and 5")
	}
	if len(m.Candidates) == 0 || len(m.Candidates) > 1024 {
		return fmt.Errorf("logical model %q requires candidates", m.ID)
	}
	seen := map[[2]string]bool{}
	for _, c := range m.Candidates {
		key := [2]string{c.ProviderID, c.UpstreamModel}
		if c.ProviderID == "" || c.UpstreamModel == "" || seen[key] || c.Weight < 0 || c.Weight > 1000000 {
			return fmt.Errorf("logical model %q has an invalid or duplicate candidate", m.ID)
		}
		seen[key] = true
	}
	return nil
}
func (m LogicalModel) Clone() LogicalModel {
	m.Candidates = append([]ModelCandidate(nil), m.Candidates...)
	return m
}
func DecodeStoredLogicalModel(data []byte) (any, error) {
	var m LogicalModel
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	m.Normalize()
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// ConfigureLogicalModels installs storage and its composed mutation guard before startup publication.
func ConfigureLogicalModels(svc Service, store configstore.ConfigStore, guard configmgr.MutationGuard[LogicalModel], validator func(context.Context, []ProviderModel, []LogicalModel) error, static ...StaticModels) error {
	s := svc.(*service)
	s.staticProviderModels = map[[2]string]ProviderModel{}
	s.staticLogicalModels = map[string]LogicalModel{}
	for _, definitions := range static {
		for _, m := range definitions.ProviderModels {
			m = cloneProviderModel(m)
			m.Normalize()
			if err := m.Validate(); err != nil {
				return err
			}
			key := [2]string{m.ProviderID, m.UpstreamModel}
			if _, ok := s.staticProviderModels[key]; ok {
				return fmt.Errorf("duplicate static ProviderModel")
			}
			m.ReadOnly = true
			s.staticProviderModels[key] = m
		}
		for _, m := range definitions.LogicalModels {
			m = m.Clone()
			m.Normalize()
			if err := m.Validate(); err != nil {
				return err
			}
			if _, ok := s.staticLogicalModels[m.ID]; ok {
				return fmt.Errorf("duplicate static LogicalModel")
			}
			m.ReadOnly = true
			s.staticLogicalModels[m.ID] = m
		}
	}
	s.logicalStore = store
	s.logicalGuard = guard
	s.validateGeneration = validator
	return s.RefreshModels(context.Background())
}

// RefreshModels publishes only a fully decoded generation. Resolution never reads storage.
func (s *service) RefreshModels(ctx context.Context) error {
	return configmgr.GuardMutation(s.guard, ctx, configmgr.MutationRefresh, "", nil, func(ctx context.Context) error {
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		return s.refreshModels(ctx)
	})
}
func (s *service) refreshModels(ctx context.Context) error {
	models, err := s.loadProviderModels(ctx)
	if err != nil {
		return err
	}
	for key, m := range s.staticProviderModels {
		for _, v := range models {
			if key == [2]string{v.ProviderID, v.UpstreamModel} {
				return fmt.Errorf("static/dynamic ProviderModel conflict %q/%q", key[0], key[1])
			}
		}
		models = append(models, cloneProviderModel(m))
	}
	pm := map[[2]string]ProviderModel{}
	for _, m := range models {
		pm[[2]string{m.ProviderID, m.UpstreamModel}] = cloneProviderModel(m)
	}
	lm := map[string]LogicalModel{}
	for id, m := range s.staticLogicalModels {
		lm[id] = m.Clone()
	}
	if s.logicalStore != nil {
		items, err := s.logicalStore.List(ctx)
		if err != nil {
			return err
		}
		for _, item := range items {
			m, ok := item.(*LogicalModel)
			if !ok || m == nil {
				return fmt.Errorf("unexpected logical model type %T", item)
			}
			v := m.Clone()
			v.ReadOnly = false
			v.Normalize()
			if err := v.Validate(); err != nil {
				return err
			}
			for _, c := range v.Candidates {
				if _, ok := pm[[2]string{c.ProviderID, c.UpstreamModel}]; !ok {
					return fmt.Errorf("logical model %q references missing provider model %q/%q", v.ID, c.ProviderID, c.UpstreamModel)
				}
			}
			if _, exists := lm[v.ID]; exists {
				return fmt.Errorf("static/dynamic LogicalModel conflict %q", v.ID)
			}
			lm[v.ID] = v
		}
	}
	for _, m := range lm {
		for _, c := range m.Candidates {
			if _, ok := pm[[2]string{c.ProviderID, c.UpstreamModel}]; !ok {
				return fmt.Errorf("logical model %q references missing ProviderModel %q/%q", m.ID, c.ProviderID, c.UpstreamModel)
			}
		}
	}
	if s.validateGeneration != nil {
		logical := make([]LogicalModel, 0, len(lm))
		for _, m := range lm {
			logical = append(logical, m.Clone())
		}
		if err := s.validateGeneration(ctx, models, logical); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.initialError = nil
	s.models = pm
	s.logicalModels = lm
	s.mu.Unlock()
	return nil
}
func cloneProviderModel(m ProviderModel) ProviderModel {
	if m.CapabilityOverrides != nil {
		v := *m.CapabilityOverrides
		m.CapabilityOverrides = &v
	}
	return m
}
func (s *service) ListLogicalModels(ctx context.Context) ([]LogicalModel, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]LogicalModel, 0, len(s.logicalModels))
	for _, m := range s.logicalModels {
		out = append(out, m.Clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func (s *service) GetLogicalModel(ctx context.Context, id string) (*LogicalModel, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.logicalModels[id]
	if !ok {
		return nil, false, nil
	}
	v := m.Clone()
	return &v, true, nil
}
func (s *service) CreateLogicalModel(ctx context.Context, m LogicalModel) error {
	return s.mutateLogical(ctx, configmgr.MutationCreate, m.ID, &m)
}
func (s *service) UpdateLogicalModel(ctx context.Context, m LogicalModel) error {
	return s.mutateLogical(ctx, configmgr.MutationUpdate, m.ID, &m)
}
func (s *service) DeleteLogicalModel(ctx context.Context, id string) error {
	return s.mutateLogical(ctx, configmgr.MutationDelete, id, nil)
}
func (s *service) mutateLogical(ctx context.Context, action configmgr.MutationAction, id string, m *LogicalModel) error {
	if _, ok := s.staticLogicalModels[strings.TrimSpace(id)]; ok {
		return ErrStaticModelReadOnly
	}
	if m != nil {
		v := m.Clone()
		v.ReadOnly = false
		v.Normalize()
		if err := v.Validate(); err != nil {
			return err
		}
		m = &v
		id = v.ID
	}
	return configmgr.GuardMutation(s.logicalGuard, ctx, action, id, m, func(ctx context.Context) error {
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		if s.logicalStore == nil {
			return fmt.Errorf("logical model store is not configured")
		}
		if m != nil {
			for _, c := range m.Candidates {
				_, ok, err := s.GetProviderModel(ctx, c.ProviderID, c.UpstreamModel)
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("provider model %q/%q is not configured", c.ProviderID, c.UpstreamModel)
				}
			}
		}
		var err error
		switch action {
		case configmgr.MutationCreate:
			err = s.logicalStore.Create(ctx, m)
		case configmgr.MutationUpdate:
			err = s.logicalStore.Update(ctx, m)
		case configmgr.MutationDelete:
			err = s.logicalStore.Delete(ctx, id)
		}
		if err != nil {
			return err
		}
		// Publish the committed object without introducing another failing I/O operation.
		s.mu.Lock()
		defer s.mu.Unlock()
		if action == configmgr.MutationDelete {
			delete(s.logicalModels, id)
		} else {
			s.logicalModels[id] = m.Clone()
		}
		return nil
	})
}

func RetireProviderModels(svc Service, id string) {
	if s, ok := svc.(*service); ok {
		s.mu.Lock()
		defer s.mu.Unlock()
		for key := range s.models {
			if key[0] == id {
				delete(s.models, key)
			}
		}
	}
}

func (m *LogicalModel) UnmarshalJSON(data []byte) error {
	type value LogicalModel
	var v value
	if err := decodeModelJSON(data, &v); err != nil {
		return err
	}
	*m = LogicalModel(v)
	return nil
}

type StaticModels struct {
	ProviderModels []ProviderModel
	LogicalModels  []LogicalModel
}

var ErrStaticModelReadOnly = fmt.Errorf("static model is read-only")
