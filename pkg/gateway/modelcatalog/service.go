package modelcatalog

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	"go.uber.org/zap"
)

type Service interface {
	RefreshProvider(ctx context.Context, providerID string) error
	ListManagedModels(ctx context.Context, filter ManagedModelFilter) ([]ManagedModel, error)
	GetManagedModel(ctx context.Context, providerID string, upstreamModel string) (*ManagedModel, bool, error)
	GetResolvedManagedModel(ctx context.Context, providerID string, upstreamModel string) (*ResolvedManagedModel, bool, error)
	CreateManagedModel(ctx context.Context, model ManagedModel) error
	UpdateManagedModel(ctx context.Context, model ManagedModel) error
	DeleteManagedModel(ctx context.Context, providerID string, upstreamModel string) error
	ListProviderSnapshots(ctx context.Context, providerID string) ([]ProviderModelSnapshot, error)
}

type ManagedModelFilter struct {
	ProviderID string
}

type providerResolver interface {
	ResolveProvider(ctx context.Context, providerID string) (provider.Provider, error)
}

type service struct {
	mu sync.RWMutex

	store         configstore.ConfigStore
	providerMgr   providerResolver
	staticManaged map[string]ManagedModel
	snapshots     map[string][]ProviderModelSnapshot
	logger        *zap.Logger
}

func NewService(store configstore.ConfigStore, providerMgr providerResolver, staticManaged []ManagedModel, logger *zap.Logger) Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	staticMap := make(map[string]ManagedModel, len(staticManaged))
	for _, item := range staticManaged {
		item.Normalize()
		staticMap[managedKey(item.ProviderID, item.UpstreamModel)] = item
	}
	return &service{
		store:         store,
		providerMgr:   providerMgr,
		staticManaged: staticMap,
		snapshots:     map[string][]ProviderModelSnapshot{},
		logger:        logger,
	}
}

func (s *service) RefreshProvider(ctx context.Context, providerID string) error {
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
	s.snapshots[providerID] = snapshots
	s.mu.Unlock()
	return err
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

func (s *service) ListManagedModels(ctx context.Context, filter ManagedModelFilter) ([]ManagedModel, error) {
	managed, err := s.loadManagedModels(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]ManagedModel, 0, len(managed))
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

func (s *service) GetManagedModel(ctx context.Context, providerID string, upstreamModel string) (*ManagedModel, bool, error) {
	managed, ok, err := s.getManagedModel(ctx, providerID, upstreamModel)
	if err != nil || !ok {
		return nil, ok, err
	}
	return &managed, true, nil
}

func (s *service) GetResolvedManagedModel(ctx context.Context, providerID string, upstreamModel string) (*ResolvedManagedModel, bool, error) {
	managed, ok, err := s.getManagedModel(ctx, providerID, upstreamModel)
	if err != nil || !ok {
		return nil, ok, err
	}
	resolved, err := s.resolvedManagedModel(ctx, managed)
	if err != nil {
		return nil, false, err
	}
	return &resolved, true, nil
}

func (s *service) CreateManagedModel(ctx context.Context, model ManagedModel) error {
	model.Normalize()
	if model.ProviderID == "" || model.UpstreamModel == "" {
		return fmt.Errorf("provider_id and upstream_model are required")
	}
	if s.store == nil {
		return fmt.Errorf("model store is not configured")
	}
	return s.store.Create(ctx, &model)
}

func (s *service) UpdateManagedModel(ctx context.Context, model ManagedModel) error {
	model.Normalize()
	if model.ProviderID == "" || model.UpstreamModel == "" {
		return fmt.Errorf("provider_id and upstream_model are required")
	}
	if s.store == nil {
		return fmt.Errorf("model store is not configured")
	}
	return s.store.Update(ctx, &model)
}

func (s *service) DeleteManagedModel(ctx context.Context, providerID string, upstreamModel string) error {
	if s.store == nil {
		return fmt.Errorf("model store is not configured")
	}
	return s.store.Delete(ctx, providerID, upstreamModel)
}

func (s *service) loadManagedModels(ctx context.Context) ([]ManagedModel, error) {
	merged := map[string]ManagedModel{}
	for key, item := range s.staticManaged {
		merged[key] = item
	}
	if s.store != nil {
		items, err := s.store.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			model, ok := item.(*ManagedModel)
			if !ok || model == nil {
				return nil, fmt.Errorf("unexpected managed model type %T", item)
			}
			model.Normalize()
			merged[managedKey(model.ProviderID, model.UpstreamModel)] = *model
		}
	}
	out := make([]ManagedModel, 0, len(merged))
	for _, item := range merged {
		out = append(out, item)
	}
	return out, nil
}

func (s *service) getManagedModel(ctx context.Context, providerID string, upstreamModel string) (ManagedModel, bool, error) {
	if item, ok := s.staticManaged[managedKey(providerID, upstreamModel)]; ok {
		return item, true, nil
	}
	if s.store == nil {
		return ManagedModel{}, false, nil
	}
	obj, err := s.store.Get(ctx, providerID, upstreamModel)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			return ManagedModel{}, false, nil
		}
		return ManagedModel{}, false, err
	}
	model, ok := obj.(*ManagedModel)
	if !ok || model == nil {
		return ManagedModel{}, false, fmt.Errorf("unexpected managed model type %T", obj)
	}
	model.Normalize()
	return *model, true, nil
}

func (s *service) resolvedManagedModel(ctx context.Context, item ManagedModel) (ResolvedManagedModel, error) {
	view := ResolvedManagedModel{ManagedModel: item}
	snapshot, ok := s.findSnapshot(item.ProviderID, item.UpstreamModel)
	if !ok {
		_ = s.RefreshProvider(ctx, item.ProviderID)
		snapshot, ok = s.findSnapshot(item.ProviderID, item.UpstreamModel)
	}
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

func managedKey(providerID string, upstreamModel string) string {
	return providerID + "\x00" + upstreamModel
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
