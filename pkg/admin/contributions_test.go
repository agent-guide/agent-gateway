package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/attribution"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	configsqlite "github.com/agent-guide/agent-gateway/pkg/configstore/sqlite"
	"github.com/agent-guide/agent-gateway/pkg/distribution"
	"github.com/agent-guide/agent-gateway/pkg/event"
	"github.com/agent-guide/agent-gateway/pkg/extension"
	"github.com/agent-guide/agent-gateway/pkg/gateway"
)

const adminTestID = "example.admin"

type contributedResource struct {
	handle    func(http.ResponseWriter, *http.Request, extension.AdminDependencies)
	unhealthy atomic.Bool
	closed    atomic.Bool
}

func (r *contributedResource) Health(context.Context) error {
	if r.unhealthy.Load() {
		return errors.New("private diagnostic")
	}
	return nil
}
func (r *contributedResource) Close(context.Context) error { r.closed.Store(true); return nil }
func (r *contributedResource) ServeAdminHTTP(w http.ResponseWriter, req *http.Request, d extension.AdminDependencies) {
	r.handle(w, req, d)
}

type adminPublisher struct{ events []event.Envelope }

func (p *adminPublisher) Publish(_ context.Context, e event.Envelope) bool {
	p.events = append(p.events, e)
	return true
}

func newContributedAdmin(t *testing.T, resource *contributedResource, publisher event.Publisher) *Handler {
	t.Helper()
	a := distribution.Assembly{Modules: []extension.Descriptor{{ID: adminTestID, Version: "1.0.0", SchemaVersion: 1, Roles: []string{"runtime", "admin"}}}, Factories: map[string]*extension.Factory{adminTestID: {AdminRoutes: []extension.AdminRoute{{Method: "POST", Path: "/admin/extensions/" + adminTestID + "/items/{id}"}}, Validate: func(_ context.Context, b json.RawMessage) (json.RawMessage, error) { return b, nil }, Prepare: func(context.Context, extension.PreparedConfig, extension.Snapshot) (extension.Resource, error) {
		return resource, nil
	}}}, AttributionProducer: func(context.Context) (attribution.Value, error) {
		return attribution.Value{Subject: "operator", Tenant: "org"}, nil
	}}
	registry, err := a.Registry()
	if err != nil {
		t.Fatal(err)
	}
	backend, err := configstore.OpenBackend(t.Context(), "sqlite", configsqlite.Config{SQLitePath: t.TempDir() + "/config.db"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = schema.RegisterDefaultStores(backend); err != nil {
		t.Fatal(err)
	}
	store, err := backend.Get(schema.StoreExtensions)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Create(t.Context(), &extension.Envelope{ModuleID: adminTestID, SchemaVersion: 1, Config: json.RawMessage(`{"message":"hello"}`)}); err != nil {
		t.Fatal(err)
	}
	g := gateway.NewAgentGateway()
	if err = g.Bootstrap(context.Background(), gateway.BootstrapOptions{Distribution: a, ExtensionRegistry: registry, ConfigStoreBackend: backend, EventPublisher: publisher}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.Close)
	return NewHandler(g, nil)
}

func TestContributedAdminBoundaryAndDependencies(t *testing.T) {
	calls := 0
	publisher := &adminPublisher{}
	resource := &contributedResource{handle: func(w http.ResponseWriter, r *http.Request, d extension.AdminDependencies) {
		calls++
		if r.PathValue("id") != "item" || r.Pattern != "POST /admin/extensions/example.admin/items/{id}" {
			t.Errorf("routing: %s %s", r.Pattern, r.PathValue("id"))
		}
		v, ok := d.Attribution(r.Context())
		if !ok || v.Subject != "operator" || v.Tenant != "org" {
			t.Errorf("attribution: %+v", v)
		}
		if d.Logger == nil || string(d.Config.Config) != `{"message":"hello"}` {
			t.Errorf("dependencies: %+v", d)
		}
		d.Config.Config[0] = 'x'
		e := event.Envelope{SchemaVersion: 1, ID: "mutation", Family: "extension.mutation", Timestamp: time.Now().UTC(), Outcome: "success", Mutation: &event.ExtensionMutation{ModuleID: adminTestID, Operation: "reset"}, Attribution: attribution.Value{Subject: "forged"}}
		if !d.Events.Publish(r.Context(), e) {
			t.Error("event rejected")
		}
		forged, err := attribution.WithContext(r.Context(), attribution.Value{Subject: "impersonated", Tenant: "fake-tenant", RouteID: "fake-route"}, attribution.Policy{})
		if err != nil {
			t.Fatal(err)
		}
		if !d.Events.Publish(forged, e) {
			t.Error("forged-context publication rejected instead of being stamped")
		}
		if !d.Events.Publish(context.Background(), e) {
			t.Error("background publication rejected instead of being stamped")
		}
		e.Mutation = &event.ExtensionMutation{ModuleID: "example.other", Operation: "reset"}
		if d.Events.Publish(r.Context(), e) {
			t.Error("foreign mutation accepted")
		}
		w.WriteHeader(201)
	}}
	h := newContributedAdmin(t, resource, publisher)
	path := "/admin/extensions/" + adminTestID + "/items/item"
	for _, unknown := range []bool{false, true} {
		req := httptest.NewRequest("POST", path, strings.NewReader("{}"+strings.Repeat(" ", MaxRequestBodyBytes)))
		if unknown {
			req.ContentLength = -1
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != 413 || calls != 0 {
			t.Fatalf("oversized contribution: %d calls=%d", rr.Code, calls)
		}
	}
	for range 2 {
		req := httptest.NewRequest("POST", path, strings.NewReader("{}"))
		req.Header.Set("Origin", "https://admin.example")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != 201 || rr.Header().Get("Access-Control-Allow-Origin") != "https://admin.example" {
			t.Fatalf("supported: %d", rr.Code)
		}
	}
	if len(publisher.events) != 6 {
		t.Fatalf("event attribution: %+v", publisher.events)
	}
	for _, e := range publisher.events {
		if e.Attribution.Subject != "operator" || e.Attribution.Tenant != "org" || e.Attribution.RouteID != "" {
			t.Fatalf("module replaced trusted actor: %+v", e.Attribution)
		}
	}
	resource.unhealthy.Store(true)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/admin/health", nil))
	if rr.Code != 503 || strings.Contains(rr.Body.String(), "private") {
		t.Fatalf("health: %d %s", rr.Code, rr.Body.String())
	}
	resource.unhealthy.Store(false)
	resource.handle = func(http.ResponseWriter, *http.Request, extension.AdminDependencies) { panic("private panic") }
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("POST", path, nil))
	if rr.Code != 500 || strings.Contains(rr.Body.String(), "private") {
		t.Fatalf("panic: %d %s", rr.Code, rr.Body.String())
	}
}

func TestContributedAdminShutdownCancelsAndDrains(t *testing.T) {
	entered, finished := make(chan struct{}), make(chan struct{})
	resource := &contributedResource{handle: func(w http.ResponseWriter, r *http.Request, _ extension.AdminDependencies) {
		close(entered)
		<-r.Context().Done()
		if resourceClosed := r.Context().Err(); resourceClosed == nil {
			t.Error("context not canceled")
		}
		extension.WriteAdminError(w, 503, "closed")
	}}
	h := newContributedAdmin(t, resource, nil)
	go func() {
		defer close(finished)
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/admin/extensions/example.admin/items/item", nil))
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_ = h.extensions.Close(ctx)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("handler not canceled at retirement deadline")
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("POST", "/admin/extensions/example.admin/items/item", nil))
	if rr.Code != 503 {
		t.Fatalf("new admission after close: %d", rr.Code)
	}
	deadline := time.Now().Add(time.Second)
	for !resource.closed.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !resource.closed.Load() {
		t.Fatal("resource did not close")
	}
}

func TestContributedEventPublicationDuringGatewayClose(t *testing.T) {
	entered, emit, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	publisher := &adminPublisher{}
	resource := &contributedResource{handle: func(w http.ResponseWriter, r *http.Request, d extension.AdminDependencies) {
		close(entered)
		<-emit
		if !d.Events.Publish(r.Context(), event.Envelope{Family: "extension.mutation", Mutation: &event.ExtensionMutation{ModuleID: adminTestID}}) {
			t.Error("event rejected during drain")
		}
		w.WriteHeader(200)
	}}
	h := newContributedAdmin(t, resource, publisher)
	manager := h.extensions
	go func() {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/admin/extensions/example.admin/items/item", nil))
		close(finished)
	}()
	<-entered
	closed := make(chan struct{})
	go func() { h.gateway.Close(); close(closed) }()
	// Waiting until admissions close proves Reset owns the gateway lock and is
	// draining our live handler, rather than merely winning a scheduling race.
	deadline := time.Now().Add(time.Second)
	for {
		lease, err := manager.Acquire()
		if err != nil {
			break
		}
		lease.Release()
		if time.Now().After(deadline) {
			t.Fatal("shutdown never stopped admissions")
		}
		time.Sleep(time.Millisecond)
	}
	close(emit)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Error("event publisher blocked on gateway shutdown lock")
	}
	select {
	case <-closed:
	case <-time.After(6 * time.Second):
		t.Fatal("gateway did not drain")
	}
	if len(publisher.events) != 1 {
		t.Fatalf("events: %d", len(publisher.events))
	}
}
