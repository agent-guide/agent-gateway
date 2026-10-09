package distribution

import (
	"context"
	"encoding/json"
	"github.com/agent-guide/agent-gateway/pkg/event"
	"testing"
	"time"
)

func TestPublicAssemblyValidationOwnershipAndManifestIsolation(t *testing.T) {
	a := CommunityRuntime()
	a.AttributeKeys = []string{"online.agentguide.community.gateway/region"}
	invoked := false
	a.EventSinks = []event.Registration{{ModuleID: "online.agentguide.community.gateway", New: func(context.Context) (event.Sink, error) { invoked = true; return nil, nil }}}
	clone := a.Clone()
	if _, err := clone.Registry(); err != nil {
		t.Fatal(err)
	}
	manifest, err := clone.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	if invoked {
		t.Fatal("manifest/validation initialized a sink")
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Assembly
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.EventSinks != nil || decoded.AttributeKeys != nil || decoded.AttributionProducer != nil {
		t.Fatal("runtime callbacks entered manifest")
	}
	a.EventSinks[0].ModuleID = "changed"
	a.AttributeKeys[0] = "changed"
	if clone.EventSinks[0].ModuleID != "online.agentguide.community.gateway" || clone.AttributeKeys[0] != "online.agentguide.community.gateway/region" {
		t.Fatal("mutable assembly ownership")
	}
	for _, edit := range []func(*Assembly){
		func(a *Assembly) { a.AttributeKeys = []string{"unlinked.owner/region"} },
		func(a *Assembly) { a.EventSinks[0].ModuleID = "unlinked.owner" },
		func(a *Assembly) { a.EventSinks = append(a.EventSinks, a.EventSinks[0]) },
		func(a *Assembly) { a.EventSinks[0].New = nil },
		func(a *Assembly) { a.EventSinks[0].QueueSize = 4097 },
		func(a *Assembly) { a.EventSinks[0].WriteBudget = 2 * time.Second },
	} {
		bad := clone.Clone()
		edit(&bad)
		if _, err := bad.Registry(); err == nil {
			t.Fatal("invalid explicit assembly accepted")
		}
	}
}
