package admin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/configmgr"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
)

type pendingAdminProviderStore struct {
	configstore.ConfigStore
	failGet bool
	writes  int
}

func (s *pendingAdminProviderStore) Update(ctx context.Context, item any) error {
	if err := s.ConfigStore.Update(ctx, item); err != nil {
		return err
	}
	s.writes++
	return errors.New("private uncertain store diagnostic")
}
func (s *pendingAdminProviderStore) Get(ctx context.Context, keys ...any) (any, error) {
	if s.failGet {
		return nil, errors.New("private readback diagnostic")
	}
	return s.ConfigStore.Get(ctx, keys...)
}

func TestProviderAndModelWritesExposePendingRecovery(t *testing.T) {
	store := &pendingAdminProviderStore{ConfigStore: providerFixtures("p")}
	g := newTestAgentGateway(&testConfigStore{providerStore: store, modelStore: &testModelStore{}}, nil, nil, nil, nil)
	t.Cleanup(g.Close)
	cfg, err := g.ProviderManager().GetConfig(t.Context(), "p")
	if err != nil {
		t.Fatal(err)
	}
	cfg.DefaultModel = "changed"
	if err := g.ProviderManager().UpdateConfig(t.Context(), "p", cfg); !errors.Is(err, configmgr.ErrMutationPending) {
		t.Fatalf("uncertain update = %v", err)
	}
	store.failGet = true
	if err := g.ProviderManager().Recover(t.Context(), "p"); !errors.Is(err, configmgr.ErrMutationPending) {
		t.Fatalf("failed recovery = %v", err)
	}
	h := NewHandler(g, nil)
	for _, tc := range []struct {
		name, method, body string
		handler            http.HandlerFunc
	}{
		{"provider create", http.MethodPost, `{"id":"p","provider_type":"openai"}`, h.handleCreateProvider},
		{"model create", http.MethodPost, `{"provider_id":"p","upstream_model":"model"}`, h.handleCreateManagedModel},
		{"model update", http.MethodPut, `{"enabled":true}`, h.handleUpdateManagedModel},
		{"model delete", http.MethodDelete, "", h.handleDeleteManagedModel},
		{"model refresh", http.MethodPost, "", h.handleRefreshProviderModels},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "/admin/", strings.NewReader(tc.body))
			req.SetPathValue("provider_id", "p")
			req.SetPathValue("upstream_model", "model")
			rec := httptest.NewRecorder()
			tc.handler(rec, req)
			if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
				t.Fatalf("pending response = %d, retry=%q, %s", rec.Code, rec.Header().Get("Retry-After"), rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "private") || store.writes != 1 {
				t.Fatal("pending write leaked diagnostics or repeated storage mutation")
			}
		})
	}
}
