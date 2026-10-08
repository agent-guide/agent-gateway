package gateway

import (
	"context"
	"encoding/json"
	"github.com/agent-guide/agent-gateway/pkg/distribution"
	"github.com/agent-guide/agent-gateway/pkg/extension"
	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
	"sync/atomic"
	"testing"
)

type bootstrapExtensionResource struct{ closes atomic.Int32 }

func (*bootstrapExtensionResource) Health(context.Context) error  { return nil }
func (r *bootstrapExtensionResource) Close(context.Context) error { r.closes.Add(1); return nil }
func TestFailedBootstrapClosesPreparedExtension(t *testing.T) {
	resource := &bootstrapExtensionResource{}
	d := extension.Descriptor{ID: "example.fixture.bootstrap", Version: "1.0.0", SchemaVersion: 1, Roles: []string{"runtime"}}
	registry, err := extension.NewRegistry(extension.Registration{Descriptor: d, Factory: &extension.Factory{Validate: func(_ context.Context, b json.RawMessage) (json.RawMessage, error) { return b, nil }, Prepare: func(context.Context, extension.PreparedConfig, extension.Snapshot) (extension.Resource, error) {
		return resource, nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	g := NewAgentGateway()
	g.routeConfigManager = routecore.NewAgentRouteConfigManager(nil)
	err = g.Bootstrap(context.Background(), BootstrapOptions{ExtensionRegistry: registry, Distribution: distribution.Assembly{Modules: []extension.Descriptor{d}}, StaticExtensions: []extension.Envelope{{ModuleID: d.ID, SchemaVersion: 1, Config: json.RawMessage(`{}`)}}})
	if err == nil {
		t.Fatal("bootstrap should fail after preparation")
	}
	if resource.closes.Load() != 1 || g.Extensions() != nil {
		t.Fatal("failed bootstrap leaked extension state")
	}
	g.Close()
}
