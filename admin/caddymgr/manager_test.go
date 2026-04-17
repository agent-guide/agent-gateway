package caddymgr

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFromCaddyRouteExtractsNestedSubrouteHandlers(t *testing.T) {
	mgr := New("")

	resp := mgr.fromCaddyRoute(0, &caddyRoute{
		Match: []caddyMatch{{
			Path: []string{"/v1/*"},
		}},
		Handle: []caddyHandler{
			{
				"handler": "subroute",
				"routes": []any{
					map[string]any{
						"handle": []any{
							map[string]any{
								"handler": "agent_route_dispatcher",
								"api_handlers": map[string]any{
									"openai":    map[string]any{},
									"anthropic": map[string]any{},
								},
							},
						},
					},
				},
			},
		},
	})

	if got, want := len(resp.Handlers), 1; got != want {
		t.Fatalf("handlers len = %d, want %d", got, want)
	}
	if got := resp.Handlers[0]; got.Type != "agent_route_dispatcher" || len(got.APIs) != 2 || got.APIs[0] != "anthropic" || got.APIs[1] != "openai" {
		t.Fatalf("handler = %+v, want dispatcher with anthropic/openai", got)
	}
	if got, want := len(resp.Match.Paths), 1; got != want || resp.Match.Paths[0] != "/v1/*" {
		t.Fatalf("match paths = %+v, want [/v1/*]", resp.Match.Paths)
	}
}

func TestExtractHandlersFromHandlerGracefullyHandlesMalformedSubroute(t *testing.T) {
	mgr := New("")

	got := mgr.extractHandlersFromHandler(caddyHandler{
		"handler": "subroute",
		"routes":  "invalid",
	})

	if len(got) != 0 {
		t.Fatalf("handlers len = %d, want 0", len(got))
	}
}

func TestFromCaddyRouteCombinesOuterHostAndNestedPath(t *testing.T) {
	mgr := New("")

	resp := mgr.fromCaddyRoute(0, &caddyRoute{
		Match: []caddyMatch{{
			Host: []string{"127.0.0.1"},
		}},
		Handle: []caddyHandler{
			{
				"handler": "subroute",
				"routes": []any{
					map[string]any{
						"match": []any{
							map[string]any{
								"path": []any{"/v1/*"},
							},
						},
						"handle": []any{
							map[string]any{
								"handler": "agent_route_dispatcher",
								"api_handlers": map[string]any{
									"openai": map[string]any{},
								},
							},
						},
					},
				},
			},
		},
	})

	if got, want := len(resp.Match.Hosts), 1; got != want || resp.Match.Hosts[0] != "127.0.0.1" {
		t.Fatalf("match hosts = %+v, want [127.0.0.1]", resp.Match.Hosts)
	}
	if got, want := len(resp.Match.Paths), 1; got != want || resp.Match.Paths[0] != "/v1/*" {
		t.Fatalf("match paths = %+v, want [/v1/*]", resp.Match.Paths)
	}
}

func TestFromCaddyRouteCollectsAllMatcherEntries(t *testing.T) {
	mgr := New("")

	resp := mgr.fromCaddyRoute(0, &caddyRoute{
		Match: []caddyMatch{
			{Host: []string{"example.com"}},
			{Path: []string{"/v1/*"}},
		},
		Handle: []caddyHandler{
			{
				"handler": "agent_route_dispatcher",
				"api_handlers": map[string]any{
					"openai": map[string]any{},
				},
			},
		},
	})

	if got, want := len(resp.Match.Hosts), 1; got != want || resp.Match.Hosts[0] != "example.com" {
		t.Fatalf("match hosts = %+v, want [example.com]", resp.Match.Hosts)
	}
	if got, want := len(resp.Match.Paths), 1; got != want || resp.Match.Paths[0] != "/v1/*" {
		t.Fatalf("match paths = %+v, want [/v1/*]", resp.Match.Paths)
	}
}

func TestFromCaddyServerMarksAdminServerReadOnly(t *testing.T) {
	mgr := New("")

	resp := mgr.fromCaddyServer("admin", &caddyServer{
		Listen: []string{"127.0.0.1:8081"},
		Routes: []caddyRoute{
			{
				Handle: []caddyHandler{
					{"handler": "subroute", "routes": []any{
						map[string]any{
							"handle": []any{
								map[string]any{"handler": "agent_gateway_admin"},
							},
						},
					}},
				},
			},
		},
	})

	if !resp.ReadOnly {
		t.Fatalf("readonly = %v, want true", resp.ReadOnly)
	}
	if got, want := resp.Source, "system"; got != want {
		t.Fatalf("source = %q, want %q", got, want)
	}
	if got, want := resp.PublicURL, "http://127.0.0.1:8081/"; got != want {
		t.Fatalf("public_url = %q, want %q", got, want)
	}
}

func TestFromCaddyServerMarksConfiguredServerReadOnly(t *testing.T) {
	mgr := New("")
	mgr.SetReadOnlyServerIDs([]string{"srv1"})

	resp := mgr.fromCaddyServer("srv1", &caddyServer{
		Listen: []string{"127.0.0.1:8082"},
		Routes: []caddyRoute{
			{
				Handle: []caddyHandler{
					{
						"handler": "agent_route_dispatcher",
						"api_handlers": map[string]any{
							"openai": map[string]any{},
						},
					},
				},
			},
		},
	})

	if !resp.ReadOnly {
		t.Fatalf("readonly = %v, want true", resp.ReadOnly)
	}
	if got, want := resp.Source, "caddyfile"; got != want {
		t.Fatalf("source = %q, want %q", got, want)
	}
	if got, want := resp.PublicURL, "http://127.0.0.1:8082/"; got != want {
		t.Fatalf("public_url = %q, want %q", got, want)
	}
}

func TestConfiguredServerRejectsUpdateAndDelete(t *testing.T) {
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
		_, _ = w.Write([]byte(`{"listen":["127.0.0.1:8082"],"routes":[{"handle":[{"handler":"agent_route_dispatcher"}]}]}`))
	}))
	defer srv.Close()

	mgr := New(srv.URL)
	mgr.SetReadOnlyServerIDs([]string{"srv1"})

	err := mgr.UpdateServer(context.Background(), &ServerRequest{
		ID:     "srv1",
		Listen: []string{"127.0.0.1:8083"},
	})
	if !errors.Is(err, ErrReadOnly) {
		t.Fatalf("UpdateServer error = %v, want ErrReadOnly", err)
	}

	err = mgr.DeleteServer(context.Background(), "srv1")
	if !errors.Is(err, ErrReadOnly) {
		t.Fatalf("DeleteServer error = %v, want ErrReadOnly", err)
	}
	if writeCalls != 0 {
		t.Fatalf("writeCalls = %d, want 0", writeCalls)
	}
}

func TestConfiguredServerRejectsCreateWithSameID(t *testing.T) {
	writeCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeCalls++
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	defer srv.Close()

	mgr := New(srv.URL)
	mgr.SetReadOnlyServerIDs([]string{"srv1"})

	err := mgr.CreateServer(context.Background(), &ServerRequest{
		ID:     "srv1",
		Listen: []string{"127.0.0.1:8083"},
	})
	if !errors.Is(err, ErrReadOnly) {
		t.Fatalf("CreateServer error = %v, want ErrReadOnly", err)
	}
	if writeCalls != 0 {
		t.Fatalf("writeCalls = %d, want 0", writeCalls)
	}
}

func TestDeriveServerPublicURLNormalizesLoopbackListen(t *testing.T) {
	if got, want := deriveServerPublicURL([]string{":8081"}, false), "http://127.0.0.1:8081/"; got != want {
		t.Fatalf("deriveServerPublicURL = %q, want %q", got, want)
	}
}
