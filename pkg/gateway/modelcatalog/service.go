package modelcatalog

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/configmgr"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	"go.uber.org/zap"
)

type Service interface {
	RefreshModels(ctx context.Context) error
	ListLogicalModels(ctx context.Context) ([]LogicalModel, error)
	GetLogicalModel(ctx context.Context, id string) (*LogicalModel, bool, error)
	CreateLogicalModel(ctx context.Context, model LogicalModel) error
	UpdateLogicalModel(ctx context.Context, model LogicalModel) error
	DeleteLogicalModel(ctx context.Context, id string) error
	RefreshProvider(ctx context.Context, providerID string) error
	ListProviderModels(ctx context.Context, filter ProviderModelFilter) ([]ProviderModel, error)
	GetProviderModel(ctx context.Context, providerID string, upstreamModel string) (*ProviderModel, bool, error)
	GetResolvedProviderModel(ctx context.Context, providerID string, upstreamModel string) (*ResolvedProviderModel, bool, error)
	CreateProviderModel(ctx context.Context, model ProviderModel) error
	UpdateProviderModel(ctx context.Context, model ProviderModel) error
	DeleteProviderModel(ctx context.Context, providerID string, upstreamModel string) error
	ListProviderSnapshots(ctx context.Context, providerID string) ([]ProviderModelSnapshot, error)
}

type ProviderModelFilter struct {
	ProviderID string
}

type providerResolver interface {
	ResolveProvider(ctx context.Context, providerID string) (provider.Provider, error)
}

type service struct {
	staticProviderModels map[[2]string]ProviderModel
	staticLogicalModels  map[string]LogicalModel
	initialError         error
	validateGeneration   func(context.Context, []ProviderModel, []LogicalModel) error
	mu                   sync.RWMutex
	writeMu              sync.Mutex
	models               map[[2]string]ProviderModel
	logicalModels        map[string]LogicalModel
	logicalStore         configstore.ConfigStore
	logicalGuard         configmgr.MutationGuard[LogicalModel]

	store       configstore.ConfigStore
	providerMgr providerResolver
	snapshots   map[string][]ProviderModelSnapshot
	epochs      map[string]uint64
	logger      *zap.Logger
	guard       configmgr.MutationGuard[ProviderModel]
}

func NewService(store configstore.ConfigStore, providerMgr providerResolver, logger *zap.Logger, guards ...configmgr.MutationGuard[ProviderModel]) Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	s := &service{
		guard:  configmgr.SelectMutationGuard(guards),
		models: map[[2]string]ProviderModel{}, logicalModels: map[string]LogicalModel{},
		store:       store,
		providerMgr: providerMgr,
		snapshots:   map[string][]ProviderModelSnapshot{},
		epochs:      map[string]uint64{},
		logger:      logger,
	}
	s.initialError = s.RefreshModels(context.Background())
	return s
}

func (s *service) RefreshProvider(ctx context.Context, providerID string) error {
	s.mu.RLock()
	epoch := s.epochs[providerID]
	s.mu.RUnlock()
	if s.providerMgr == nil {
		s.logger.Error("provider model discovery cannot run without provider manager",
			zap.String("provider_id", providerID))
		return fmt.Errorf("provider manager is not configured")
	}
	s.logger.Info("refreshing provider model discovery",
		zap.String("provider_id", providerID))
	prov, err := s.providerMgr.ResolveProvider(ctx, providerID)
	if err != nil {
		s.logger.Error("provider model discovery failed to resolve provider",
			zap.String("provider_id", providerID),
			zap.Error(err))
		return err
	}
	cfg := prov.Config()
	models, err := prov.ListModels(ctx)
	now := time.Now().UTC()
	snapshots := make([]ProviderModelSnapshot, 0, len(models))
	if err != nil {
		snapshots = append(snapshots, ProviderModelSnapshot{
			ProviderID:   providerID,
			ProviderType: cfg.ProviderType,
			Status:       SnapshotStatusError,
			FetchedAt:    now,
			LastError:    err.Error(),
		})
		s.logger.Warn("provider model discovery request failed",
			zap.String("provider_id", providerID),
			zap.String("provider_type", cfg.ProviderType),
			zap.Error(err))
	} else {
		for _, model := range models {
			upstream := model.ID
			if upstream == "" {
				upstream = model.Name
			}
			snapshots = append(snapshots, ProviderModelSnapshot{
				ProviderID:    providerID,
				ProviderType:  cfg.ProviderType,
				UpstreamModel: upstream,
				DisplayName:   firstNonEmpty(model.DisplayName, model.Name, model.ID),
				Description:   model.Description,
				Capabilities:  effectiveDiscoveredCaps(model, prov.Capabilities()),
				Status:        SnapshotStatusOK,
				FetchedAt:     now,
			})
		}
		s.logger.Info("provider model discovery finished",
			zap.String("provider_id", providerID),
			zap.String("provider_type", cfg.ProviderType),
			zap.Int("model_count", len(models)))
	}
	s.mu.Lock()
	if s.epochs[providerID] != epoch {
		s.mu.Unlock()
		return fmt.Errorf("provider %q discovery generation was retired", providerID)
	}
	s.snapshots[providerID] = snapshots
	s.mu.Unlock()
	return err
}

// InvalidateProvider retires discovery state after a committed Provider change.
// In-flight discovery may finish but cannot republish the retired generation.
func InvalidateProvider(svc Service, id string) {
	if s, ok := svc.(*service); ok {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.epochs == nil {
			s.epochs = map[string]uint64{}
		}
		s.epochs[id]++
		delete(s.snapshots, id)
	}
}

func (s *service) ListProviderSnapshots(ctx context.Context, providerID string) ([]ProviderModelSnapshot, error) {
	if providerID != "" {
		s.mu.RLock()
		out := append([]ProviderModelSnapshot(nil), s.snapshots[providerID]...)
		s.mu.RUnlock()
		if len(out) == 0 {
			s.logger.Info("provider model snapshots cache miss; refreshing",
				zap.String("provider_id", providerID))
			err := s.RefreshProvider(ctx, providerID)
			s.mu.RLock()
			out = append([]ProviderModelSnapshot(nil), s.snapshots[providerID]...)
			s.mu.RUnlock()
			if err != nil {
				if len(out) > 0 {
					s.logger.Warn("provider model snapshots refresh failed; returning error snapshot",
						zap.String("provider_id", providerID),
						zap.Int("snapshot_count", len(out)),
						zap.Error(err))
					return append([]ProviderModelSnapshot(nil), out...), nil
				}
				return nil, err
			}
		}
		return append([]ProviderModelSnapshot(nil), out...), nil
	}
	return nil, nil
}

func (s *service) ListProviderModels(ctx context.Context, filter ProviderModelFilter) ([]ProviderModel, error) {
	s.mu.RLock()
	if s.initialError != nil {
		err := s.initialError
		s.mu.RUnlock()
		return nil, err
	}
	managed := make([]ProviderModel, 0, len(s.models))
	for _, m := range s.models {
		managed = append(managed, cloneProviderModel(m))
	}
	s.mu.RUnlock()
	items := make([]ProviderModel, 0, len(managed))
	for _, item := range managed {
		if filter.ProviderID != "" && item.ProviderID != filter.ProviderID {
			continue
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ProviderID != items[j].ProviderID {
			return items[i].ProviderID < items[j].ProviderID
		}
		return items[i].UpstreamModel < items[j].UpstreamModel
	})
	return items, nil
}

func (s *service) GetProviderModel(ctx context.Context, providerID string, upstreamModel string) (*ProviderModel, bool, error) {
	managed, ok, err := s.getProviderModel(ctx, providerID, upstreamModel)
	if err != nil || !ok {
		return nil, ok, err
	}
	return &managed, true, nil
}

func (s *service) GetResolvedProviderModel(ctx context.Context, providerID string, upstreamModel string) (*ResolvedProviderModel, bool, error) {
	managed, ok, err := s.getProviderModel(ctx, providerID, upstreamModel)
	if err != nil || !ok {
		return nil, ok, err
	}
	resolved, err := s.resolvedProviderModel(ctx, managed)
	if err != nil {
		return nil, false, err
	}
	return &resolved, true, nil
}

func (s *service) CreateProviderModel(ctx context.Context, model ProviderModel) error {
	model.Normalize()
	if _, ok := s.staticProviderModels[[2]string{model.ProviderID, model.UpstreamModel}]; ok {
		return ErrStaticModelReadOnly
	}
	model.ReadOnly = false
	model = cloneProviderModel(model)
	model.Normalize()
	return configmgr.GuardMutation(s.guard, ctx, configmgr.MutationCreate, model.ProviderID, &model, func(ctx context.Context) error { return s.createProviderModel(ctx, model) })
}

func (s *service) createProviderModel(ctx context.Context, model ProviderModel) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	model.Normalize()
	if model.ProviderID == "" || model.UpstreamModel == "" {
		return fmt.Errorf("provider_id and upstream_model are required")
	}
	if s.store == nil {
		return fmt.Errorf("model store is not configured")
	}
	if err := s.store.Create(ctx, &model); err != nil {
		return err
	}
	s.mu.Lock()
	s.models[[2]string{model.ProviderID, model.UpstreamModel}] = cloneProviderModel(model)
	s.mu.Unlock()
	return nil
}

func (s *service) UpdateProviderModel(ctx context.Context, model ProviderModel) error {
	model.Normalize()
	if _, ok := s.staticProviderModels[[2]string{model.ProviderID, model.UpstreamModel}]; ok {
		return ErrStaticModelReadOnly
	}
	model.ReadOnly = false
	model = cloneProviderModel(model)
	model.Normalize()
	return configmgr.GuardMutation(s.guard, ctx, configmgr.MutationUpdate, model.ProviderID, &model, func(ctx context.Context) error { return s.updateProviderModel(ctx, model) })
}

func (s *service) updateProviderModel(ctx context.Context, model ProviderModel) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	model.Normalize()
	if model.ProviderID == "" || model.UpstreamModel == "" {
		return fmt.Errorf("provider_id and upstream_model are required")
	}
	if s.store == nil {
		return fmt.Errorf("model store is not configured")
	}
	if err := s.store.Update(ctx, &model); err != nil {
		return err
	}
	s.mu.Lock()
	s.models[[2]string{model.ProviderID, model.UpstreamModel}] = cloneProviderModel(model)
	s.mu.Unlock()
	return nil
}

func (s *service) DeleteProviderModel(ctx context.Context, providerID string, upstreamModel string) error {
	if _, ok := s.staticProviderModels[[2]string{providerID, upstreamModel}]; ok {
		return ErrStaticModelReadOnly
	}
	return configmgr.GuardMutation(s.guard, ctx, configmgr.MutationDelete, providerID, &ProviderModel{ProviderID: providerID, UpstreamModel: upstreamModel}, func(ctx context.Context) error { return s.deleteProviderModel(ctx, providerID, upstreamModel) })
}

func (s *service) deleteProviderModel(ctx context.Context, providerID string, upstreamModel string) error {
	if s.store == nil {
		return fmt.Errorf("model store is not configured")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.store.Delete(ctx, providerID, upstreamModel); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.models, [2]string{providerID, upstreamModel})
	s.mu.Unlock()
	return nil
}

func (s *service) loadProviderModels(ctx context.Context) ([]ProviderModel, error) {
	if s.store == nil {
		return nil, nil
	}
	items, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ProviderModel, 0, len(items))
	for _, item := range items {
		model, ok := item.(*ProviderModel)
		if !ok || model == nil {
			return nil, fmt.Errorf("unexpected provider model type %T", item)
		}
		copy := cloneProviderModel(*model)
		copy.ReadOnly = false
		copy.Normalize()
		if err := copy.Validate(); err != nil {
			return nil, err
		}
		out = append(out, copy)
	}
	return out, nil
}

func (s *service) getProviderModel(ctx context.Context, providerID string, upstreamModel string) (ProviderModel, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.initialError != nil {
		return ProviderModel{}, false, s.initialError
	}
	model, ok := s.models[[2]string{providerID, upstreamModel}]
	return cloneProviderModel(model), ok, nil
}

func (s *service) resolvedProviderModel(ctx context.Context, item ProviderModel) (ResolvedProviderModel, error) {
	view := ResolvedProviderModel{ProviderModel: item}
	snapshot, ok := s.findSnapshot(item.ProviderID, item.UpstreamModel)
	if ok {
		view.Snapshot = &snapshot
		view.Capabilities = snapshot.Capabilities
	}
	if item.CapabilityOverrides != nil {
		view.Capabilities = *item.CapabilityOverrides
	}
	return view, nil
}

func (s *service) findSnapshot(providerID string, upstreamModel string) (ProviderModelSnapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, snapshot := range s.snapshots[providerID] {
		if snapshot.UpstreamModel == upstreamModel {
			return snapshot, true
		}
	}
	return ProviderModelSnapshot{}, false
}

func firstNonEmpty(items ...string) string {
	for _, item := range items {
		if item != "" {
			return item
		}
	}
	return ""
}

func effectiveDiscoveredCaps(model provider.ModelInfo, summary provider.ProviderCapabilities) provider.ModelCapabilities {
	if model.Capabilities != (provider.ModelCapabilities{}) {
		return model.Capabilities
	}
	return provider.ModelCapabilitiesFromProviderSummary(summary)
}
