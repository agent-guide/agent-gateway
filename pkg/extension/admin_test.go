package extension

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/agent-guide/agent-gateway/pkg/attribution"
	"github.com/agent-guide/agent-gateway/pkg/event"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func adminRegistration(routes ...AdminRoute) Registration {
	return Registration{Descriptor: Descriptor{ID: "example.admin", Version: "1.0.0", SchemaVersion: 1, Roles: []string{"runtime", "admin"}}, Factory: &Factory{AdminRoutes: routes, Validate: func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) { return raw, nil }, Prepare: func(context.Context, PreparedConfig, Snapshot) (Resource, error) { return &adminResource{}, nil }}}
}

type adminResource struct{}

func (*adminResource) Health(context.Context) error                                         { return nil }
func (*adminResource) Close(context.Context) error                                          { return nil }
func (*adminResource) ServeAdminHTTP(http.ResponseWriter, *http.Request, AdminDependencies) {}

type healthOnlyResource struct{}

func (*healthOnlyResource) Health(context.Context) error { return nil }
func (*healthOnlyResource) Close(context.Context) error  { return nil }

func TestAdminOwnershipAndConflict(t *testing.T) {
	prefix := "/admin/extensions/example.admin/"
	for _, routes := range [][]AdminRoute{
		{{"GET", "/admin/memory/config"}}, {{"POST", "/admin/agents/routes"}}, {{"GET", "/admin/extensions/example.other/status"}}, {{"GET", strings.TrimSuffix(prefix, "/")}},
		{{"GET", prefix + "status"}, {"GET", prefix + "status"}}, {{"GET", prefix + "{id}"}, {"GET", prefix + "status"}}, {{"GET", prefix + "{a}/value"}, {"GET", prefix + "key/{b}"}}, {{"GET", prefix + "status"}, {"HEAD", prefix + "status"}},
		{{"GET", prefix + "{id}/{id}"}}, {{"GET", prefix + "{rest...}"}}, {{"GET", prefix + "a/../b"}}, {{"GET", prefix + ""}}, {{"GET", prefix + "a/"}}, {{"OPTIONS", prefix + "status"}},
	} {
		reg := adminRegistration(routes...)
		_, err := NewRegistry(reg)
		var e *Error
		if !errors.As(err, &e) || e.Code != "route_conflict" {
			t.Fatalf("accepted/unsafe conflict %#v: %v", routes, err)
		}
	}
	reg := adminRegistration(AdminRoute{"GET", prefix + "items/{id}"}, AdminRoute{"PUT", prefix + "items/{id}"}, AdminRoute{"GET", prefix + "status"})
	r, err := NewRegistry(reg)
	if err != nil {
		t.Fatal(err)
	}
	reg.Factory.AdminRoutes[0].Path = "mutated"
	routes := r.AdminRoutes()
	routes[0].Path = "mutated"
	if r.AdminRoutes()[0].Path != prefix+"items/{id}" {
		t.Fatal("declarations alias caller")
	}
	// The complete declaration set registers without a public ServeMux panic.
	mux := http.NewServeMux()
	for _, route := range r.AdminRoutes() {
		mux.HandleFunc(route.Method+" "+route.Path, func(http.ResponseWriter, *http.Request) {})
	}
}

func TestAdminMissingContributorFailsPreparation(t *testing.T) {
	reg := adminRegistration(AdminRoute{"GET", "/admin/extensions/example.admin/status"})
	reg.Factory.Prepare = func(context.Context, PreparedConfig, Snapshot) (Resource, error) { return &healthOnlyResource{}, nil }
	r, err := NewRegistry(reg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewManager(context.Background(), r, nil, []Envelope{{ModuleID: reg.Descriptor.ID, SchemaVersion: 1, Config: json.RawMessage(`{}`)}})
	var e *Error
	if !errors.As(err, &e) || e.Code != "prepare_failed" || e.Field != "admin_routes" {
		t.Fatalf("missing contributor: %v", err)
	}
}

func TestAdminErrorBound(t *testing.T) {
	rr := httptest.NewRecorder()
	WriteAdminError(rr, 400, strings.Repeat("secret", 500))
	if rr.Code != 400 || rr.Body.Len() > 1024 || strings.Contains(rr.Body.String(), "secret") {
		t.Fatalf("unbounded error: %s", rr.Body.String())
	}
}

type modifyingAdminSinkPublisher struct {
	t        *testing.T
	want     attribution.Value
	delivery context.Context
	count    int
}

func (p *modifyingAdminSinkPublisher) Publish(ctx context.Context, e event.Envelope) bool {
	p.t.Helper()
	if ctx != p.delivery {
		p.t.Error("publication discarded delivery context")
	}
	if e.Attribution.Subject != p.want.Subject || e.Attribution.Tenant != p.want.Tenant || e.Attribution.RouteID != p.want.RouteID || e.Attribution.Attributes["example.admin/region"] != "original" {
		p.t.Fatalf("replaced request identity: %+v", e.Attribution)
	}
	// Downstream code cannot corrupt the captured identity for the next event.
	e.Attribution.Attributes["example.admin/region"] = "modified"
	p.count++
	return true
}
func TestAdminPublisherBindsEachRequestAndCopiesAttribution(t *testing.T) {
	reg := adminRegistration(AdminRoute{"POST", "/admin/extensions/example.admin/reset"})
	registry, err := NewRegistry(reg)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(context.Background(), registry, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(context.Background())
	policy, err := attribution.NewPolicy([]string{"example.admin"}, []string{"example.admin/region"})
	if err != nil {
		t.Fatal(err)
	}
	fake, err := attribution.WithContext(context.Background(), attribution.Value{Subject: "forged", Tenant: "forged-tenant", RouteID: "forged-route"}, attribution.Policy{})
	if err != nil {
		t.Fatal(err)
	}
	delivery, cancel := context.WithCancel(fake)
	defer cancel()
	downstream := &modifyingAdminSinkPublisher{t: t, delivery: delivery}
	manager.SetPublisher(downstream)
	bind := manager.AdminPublisher("example.admin")
	if bind == nil || manager.AdminPublisher("example.other") != nil {
		t.Fatal("owner binding")
	}
	for _, actor := range []string{"first", "second"} {
		value := attribution.Value{Subject: actor, Tenant: actor + "-tenant", RouteID: actor + "-route", Attributes: map[string]string{"example.admin/region": "original"}}
		trusted, err := attribution.WithContext(context.Background(), value, policy)
		if err != nil {
			t.Fatal(err)
		}
		scoped := bind(trusted)
		downstream.want = value
		for range 2 {
			if !scoped.Publish(delivery, event.Envelope{Family: "extension.mutation", Attribution: attribution.Value{Subject: "forged-envelope"}, Mutation: &event.ExtensionMutation{ModuleID: "example.admin", Operation: "reset"}}) {
				t.Fatal("owner publication failed")
			}
		}
	}
	if downstream.count != 4 {
		t.Fatalf("publications: %d", downstream.count)
	}
}
