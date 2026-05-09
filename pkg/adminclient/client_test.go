package adminclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agent-guide/caddy-agent-gateway/pkg/cliauth"
	"github.com/agent-guide/caddy-agent-gateway/pkg/gateway/modelcatalog"
)

func TestListProvidersAutoLogin(t *testing.T) {
	t.Parallel()

	loginCalls := 0
	providerCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/admin/auth/login":
			loginCalls++
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected login method: %s", r.Method)
			}
			_ = json.NewEncoder(w).Encode(LoginResponse{Token: "test-token", Username: "admin"})
		case "/admin/providers":
			providerCalls++
			if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
				t.Fatalf("unexpected auth header: %q", got)
			}
			_ = json.NewEncoder(w).Encode(itemsResponse[Provider]{
				Items: []Provider{{ProviderConfig: ProviderConfig{Id: "openai-main", ProviderType: "openai"}}},
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	client := New(Config{
		BaseURL:  srv.URL,
		Username: "admin",
		Password: "secret",
	})

	items, err := client.ListProviders(context.Background(), ProviderListOptions{})
	if err != nil {
		t.Fatalf("ListProviders error: %v", err)
	}
	if len(items) != 1 || items[0].Id != "openai-main" {
		t.Fatalf("unexpected providers: %+v", items)
	}
	if loginCalls != 1 {
		t.Fatalf("expected 1 login call, got %d", loginCalls)
	}
	if providerCalls != 1 {
		t.Fatalf("expected 1 provider call, got %d", providerCalls)
	}
}

func TestCreateProvider(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/admin/providers":
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected method: %s", r.Method)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer preset-token" {
				t.Fatalf("unexpected auth header: %q", got)
			}
			var req ProviderConfig
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if req.Id != "openai-main" || req.ProviderType != "openai" {
				t.Fatalf("unexpected request: %+v", req)
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(Provider{
				ProviderConfig: req,
				Source:         "dynamic",
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	client := New(Config{
		BaseURL: srv.URL,
		Token:   "preset-token",
	})

	created, err := client.CreateProvider(context.Background(), ProviderConfig{
		Id:           "openai-main",
		ProviderType: "openai",
	})
	if err != nil {
		t.Fatalf("CreateProvider error: %v", err)
	}
	if created.Id != "openai-main" || created.Source != "dynamic" {
		t.Fatalf("unexpected provider: %+v", created)
	}
}

func TestListRoutesIncludesQuery(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/routes" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("tag_prefix"); got != "prod-" {
			t.Fatalf("unexpected tag_prefix: %q", got)
		}
		_ = json.NewEncoder(w).Encode(itemsResponse[Route]{Items: []Route{}})
	}))
	defer srv.Close()

	client := New(Config{
		BaseURL: srv.URL,
		Token:   "preset-token",
	})

	if _, err := client.ListRoutes(context.Background(), RouteListOptions{TagPrefix: "prod-"}); err != nil {
		t.Fatalf("ListRoutes error: %v", err)
	}
}

func TestListProviderTypes(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/provider_types" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(itemsResponse[ProviderType]{
			Items: []ProviderType{{ProviderType: "openai", Enabled: true}},
		})
	}))
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL, Token: "preset-token"})
	items, err := client.ListProviderTypes(context.Background())
	if err != nil {
		t.Fatalf("ListProviderTypes error: %v", err)
	}
	if len(items) != 1 || items[0].ProviderType != "openai" {
		t.Fatalf("unexpected items: %+v", items)
	}
}

func TestRefreshProviderModels(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/models/providers/openai-main/refresh" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(RefreshDiscoveredModelsResponse{
			ProviderID: "openai-main",
			Items: []DiscoveredModel{{
				ProviderID:    "openai-main",
				ProviderType:  "openai",
				UpstreamModel: "gpt-4.1",
			}},
		})
	}))
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL, Token: "preset-token"})
	resp, err := client.RefreshProviderModels(context.Background(), "openai-main")
	if err != nil {
		t.Fatalf("RefreshProviderModels error: %v", err)
	}
	if resp.ProviderID != "openai-main" || len(resp.Items) != 1 {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestEnableCLIAuthAuthenticatorAllowsCreated(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/cliauth/authenticators/codex/enable" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(CLIAuthEnableAuthenticatorResponse{
			Status: "enabled",
			Authenticator: CLIAuthAuthenticator{
				Name:         "codex",
				ProviderType: "openai",
				Enabled:      true,
				Config: cliauth.AuthenticatorConfig{
					NoBrowser: true,
				},
			},
		})
	}))
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL, Token: "preset-token"})
	resp, err := client.EnableCLIAuthAuthenticator(context.Background(), "codex", EnableCLIAuthAuthenticatorRequest{
		Config: &cliauth.AuthenticatorConfig{NoBrowser: true},
	})
	if err != nil {
		t.Fatalf("EnableCLIAuthAuthenticator error: %v", err)
	}
	if resp.Authenticator.Name != "codex" || !resp.Authenticator.Enabled {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestUpsertManagedModel(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/models/managed/openai-main/gpt-4.1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var req modelcatalog.ManagedModel
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(ManagedModel{
			ManagedModel: modelcatalog.ManagedModel{
				ProviderID:    "openai-main",
				UpstreamModel: "gpt-4.1",
				Enabled:       true,
			},
		})
	}))
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL, Token: "preset-token"})
	resp, err := client.UpsertManagedModel(context.Background(), "openai-main", "gpt-4.1", modelcatalog.ManagedModel{
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("UpsertManagedModel error: %v", err)
	}
	if resp.ProviderID != "openai-main" || resp.UpstreamModel != "gpt-4.1" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}
