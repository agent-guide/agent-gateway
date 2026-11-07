package modelcatalog

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	"github.com/cloudwego/eino/schema"
)

type testProviderResolver struct {
	provider provider.Provider
	err      error
}

func (r testProviderResolver) ResolveProvider(context.Context, string) (provider.Provider, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.provider, nil
}

type testProvider struct {
	cfg    provider.ProviderConfig
	models []provider.ModelInfo
	err    error
}

func (p testProvider) Chat(context.Context, *provider.ChatRequest) (*provider.ChatResponse, error) {
	return nil, errors.New("not implemented")
}

func (p testProvider) StreamChat(context.Context, *provider.ChatRequest) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("not implemented")
}

func (p testProvider) ListModels(context.Context) ([]provider.ModelInfo, error) {
	if p.err != nil {
		return nil, p.err
	}
	return append([]provider.ModelInfo(nil), p.models...), nil
}

func (p testProvider) Capabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{Streaming: true}
}

func (p testProvider) Config() provider.ProviderConfig {
	return p.cfg
}

type blockingDiscoveryProvider struct {
	testProvider
	started, release chan struct{}
}

type retiredDiscoveryResolver struct {
	provider provider.Provider
	retired  atomic.Bool
}

func (r *retiredDiscoveryResolver) ResolveProvider(context.Context, string) (provider.Provider, error) {
	if r.retired.Load() {
		return nil, errors.New("provider removed")
	}
	return r.provider, nil
}

func (p blockingDiscoveryProvider) ListModels(context.Context) ([]provider.ModelInfo, error) {
	close(p.started)
	<-p.release
	return []provider.ModelInfo{{ID: "old"}}, nil
}

func TestInvalidationPreventsLateDiscoveryPublication(t *testing.T) {
	p := blockingDiscoveryProvider{testProvider: testProvider{cfg: provider.ProviderConfig{Id: "p", ProviderType: "openai"}}, started: make(chan struct{}), release: make(chan struct{})}
	resolver := &retiredDiscoveryResolver{provider: p}
	svc := NewService(nil, resolver, nil)
	done := make(chan error, 1)
	go func() { done <- svc.RefreshProvider(t.Context(), "p") }()
	<-p.started
	InvalidateProvider(svc, "p")
	resolver.retired.Store(true)
	close(p.release)
	if err := <-done; err == nil {
		t.Fatal("retired discovery reported publication success")
	}
	snapshots, err := svc.ListProviderSnapshots(t.Context(), "p")
	if err == nil || len(snapshots) != 0 {
		t.Fatalf("late discovery republished deleted provider: %v, %v", snapshots, err)
	}
}

func TestListProviderSnapshotsRefreshesOnFirstRead(t *testing.T) {
	t.Parallel()

	svc := NewService(nil, testProviderResolver{
		provider: testProvider{
			cfg: provider.ProviderConfig{Id: "zhipu-main", ProviderType: "zhipu"},
			models: []provider.ModelInfo{{
				ID:          "glm-4.5",
				DisplayName: "GLM-4.5",
			}},
		},
	}, nil)

	items, err := svc.ListProviderSnapshots(context.Background(), "zhipu-main")
	if err != nil {
		t.Fatalf("ListProviderSnapshots returned error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("snapshot count = %d, want 1", len(items))
	}
	if items[0].UpstreamModel != "glm-4.5" {
		t.Fatalf("upstream model = %q, want glm-4.5", items[0].UpstreamModel)
	}
	if items[0].Status != SnapshotStatusOK {
		t.Fatalf("snapshot status = %q, want %q", items[0].Status, SnapshotStatusOK)
	}
}

func TestListProviderSnapshotsReturnsErrorSnapshotWhenRefreshFails(t *testing.T) {
	t.Parallel()

	svc := NewService(nil, testProviderResolver{
		provider: testProvider{
			cfg: provider.ProviderConfig{Id: "zhipu-main", ProviderType: "zhipu"},
			err: errors.New("upstream /models is not supported"),
		},
	}, nil)

	items, err := svc.ListProviderSnapshots(context.Background(), "zhipu-main")
	if err != nil {
		t.Fatalf("ListProviderSnapshots returned error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("snapshot count = %d, want 1", len(items))
	}
	if items[0].Status != SnapshotStatusError {
		t.Fatalf("snapshot status = %q, want %q", items[0].Status, SnapshotStatusError)
	}
	if items[0].LastError == "" {
		t.Fatal("expected last_error to be populated")
	}
}
