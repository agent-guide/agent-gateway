package caddyadminclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateServerRejectsExistingServer(t *testing.T) {
	writeCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeCalls++
			t.Errorf("unexpected write request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected write", http.StatusInternalServerError)
			return
		}
		if r.URL.Path != "/config/apps/http/servers/srv1" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"listen":["127.0.0.1:8080"]}`))
	}))
	defer srv.Close()

	mgr := NewManager(srv.URL + "/")
	err := mgr.CreateServer(context.Background(), &ServerRequest{
		ID:     "srv1",
		Listen: []string{"127.0.0.1:8081"},
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("CreateServer error = %v, want ErrConflict", err)
	}
	if writeCalls != 0 {
		t.Fatalf("writeCalls = %d, want 0", writeCalls)
	}
}

func TestCreateServerPostsFullConfigForNewServer(t *testing.T) {
	var posted map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/config/apps/http/servers/srv1":
			http.NotFound(w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/config/":
			_, _ = w.Write([]byte(`{"apps":{"http":{"servers":{}}}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/config/":
			if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
				t.Errorf("decode posted config: %v", err)
				http.Error(w, "bad json", http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	mgr := NewManager(srv.URL)
	err := mgr.CreateServer(context.Background(), &ServerRequest{
		ID:     "srv1",
		Listen: []string{"127.0.0.1:8081"},
	})
	if err != nil {
		t.Fatalf("CreateServer error = %v", err)
	}

	apps := posted["apps"].(map[string]any)
	httpApp := apps["http"].(map[string]any)
	servers := httpApp["servers"].(map[string]any)
	srv1 := servers["srv1"].(map[string]any)
	listen := srv1["listen"].([]any)
	if got, want := listen[0], "127.0.0.1:8081"; got != want {
		t.Fatalf("posted listen = %v, want %v", got, want)
	}
}

func TestCreateServerReclaimsEmptyServerPlaceholder(t *testing.T) {
	var posted map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/config/apps/http/servers/agw5":
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && r.URL.Path == "/config/":
			_, _ = w.Write([]byte(`{"apps":{"http":{"servers":{"agw5":{}}}}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/config/":
			if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
				t.Errorf("decode posted config: %v", err)
				http.Error(w, "bad json", http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	mgr := NewManager(srv.URL)
	err := mgr.CreateServer(context.Background(), &ServerRequest{
		ID:     "agw5",
		Listen: []string{"127.0.0.1:8095"},
	})
	if err != nil {
		t.Fatalf("CreateServer error = %v", err)
	}

	servers := posted["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)
	agw5 := servers["agw5"].(map[string]any)
	listen := agw5["listen"].([]any)
	if got, want := listen[0], "127.0.0.1:8095"; got != want {
		t.Fatalf("posted listen = %v, want %v", got, want)
	}
}

func TestFromCaddyServerNormalizesNilListen(t *testing.T) {
	mgr := NewManager("")
	resp := mgr.fromCaddyServer("agw5", &caddyServer{})
	if resp.Listen == nil {
		t.Fatal("Listen is nil, want empty slice")
	}
	if len(resp.Listen) != 0 {
		t.Fatalf("Listen len = %d, want 0", len(resp.Listen))
	}
}

func TestListRoutesDistinguishesEmptyRoutesFromMissingServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/config/apps/http/servers/empty/routes":
			http.NotFound(w, r)
		case "/config/apps/http/servers/empty":
			_, _ = w.Write([]byte(`{"listen":["127.0.0.1:8080"]}`))
		case "/config/apps/http/servers/missing/routes", "/config/apps/http/servers/missing":
			http.NotFound(w, r)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	mgr := NewManager(srv.URL)
	routes, err := mgr.ListRoutes(context.Background(), "empty")
	if err != nil {
		t.Fatalf("ListRoutes(empty) error = %v", err)
	}
	if len(routes) != 0 {
		t.Fatalf("ListRoutes(empty) len = %d, want 0", len(routes))
	}

	_, err = mgr.ListRoutes(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("ListRoutes(missing) error = %v, want ErrNotFound", err)
	}
}

func TestReadOnlyServerIDsProtectConfiguredServers(t *testing.T) {
	mgr := NewManager("")
	mgr.SetReadOnlyServerIDs([]string{"srv0"})

	resp := mgr.fromCaddyServer("srv0", &caddyServer{
		Listen: []string{":8080"},
		Routes: []caddyRoute{{
			Group:  "managed",
			Handle: []caddyHandler{{"handler": "reverse_proxy"}},
		}},
	})
	if !resp.ReadOnly {
		t.Fatal("ReadOnly = false, want true")
	}
	if got, want := resp.Source, "caddyfile"; got != want {
		t.Fatalf("Source = %q, want %q", got, want)
	}
}
