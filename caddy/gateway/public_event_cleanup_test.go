package gateway

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/agent-guide/agent-gateway/internal/distributionruntime"
	"github.com/agent-guide/agent-gateway/internal/observability/pipeline"
	"github.com/agent-guide/agent-gateway/internal/observability/usage"
	"github.com/agent-guide/agent-gateway/pkg/attribution"
	"github.com/agent-guide/agent-gateway/pkg/distribution"
	"github.com/agent-guide/agent-gateway/pkg/event"
	"github.com/caddyserver/caddy/v2"
)

type cleanupSink struct {
	flushes atomic.Int32
	closes  atomic.Int32
}

func (*cleanupSink) Write(context.Context, event.Envelope) error { return nil }
func (s *cleanupSink) Flush(context.Context) error               { s.flushes.Add(1); return nil }
func (s *cleanupSink) Close(context.Context) error               { s.closes.Add(1); return nil }

func TestCaddyValidationClosesProvisionedPublicSink(t *testing.T) {
	previous := distributionruntime.Current()
	t.Cleanup(func() {
		if err := distributionruntime.Install(previous); err != nil {
			t.Error(err)
		}
	})
	sink := new(cleanupSink)
	var creates atomic.Int32
	a := distribution.CommunityRuntime()
	a.EventSinks = []event.Registration{{ModuleID: "online.agentguide.community.gateway", New: func(context.Context) (event.Sink, error) { creates.Add(1); return sink, nil }}}
	if err := distributionruntime.Install(a); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"config_store": map[string]any{"sqlite": map[string]string{"sqlite_path": filepath.Join(t.TempDir(), "config.db")}}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &caddy.Config{Admin: &caddy.AdminConfig{Disabled: true}, AppsRaw: caddy.ModuleMap{"agent_gateway": raw}}
	if err := caddy.Validate(cfg); err != nil {
		t.Fatal(err)
	}
	if creates.Load() != 1 || sink.flushes.Load() != 1 || sink.closes.Load() != 1 {
		t.Fatalf("validate lifecycle: creates=%d flushes=%d closes=%d", creates.Load(), sink.flushes.Load(), sink.closes.Load())
	}
}

func TestCleanupAfterStopDoesNotClosePublicSinkTwice(t *testing.T) {
	sink := new(cleanupSink)
	svc := usage.NewUsageService(pipeline.NewEventPipeline(1), nil)
	if err := svc.ConfigurePublic(attribution.Policy{}, []event.Registration{{ModuleID: "online.agentguide.community.gateway", New: func(context.Context) (event.Sink, error) { return sink, nil }}}); err != nil {
		t.Fatal(err)
	}
	a := &App{usageService: svc}
	t.Cleanup(func() { _ = a.Cleanup() })
	if err := a.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := a.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if sink.flushes.Load() != 1 || sink.closes.Load() != 1 {
		t.Fatalf("repeated teardown: flushes=%d closes=%d", sink.flushes.Load(), sink.closes.Load())
	}
}
