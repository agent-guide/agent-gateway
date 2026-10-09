package server

import (
	"context"
	"errors"
	"github.com/agent-guide/agent-gateway/pkg/distribution"
	"github.com/agent-guide/agent-gateway/pkg/event"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type failingCloseSink struct{}

func (failingCloseSink) Write(context.Context, event.Envelope) error { return nil }
func (failingCloseSink) Flush(context.Context) error                 { return nil }
func (failingCloseSink) Close(context.Context) error                 { return errors.New("private-sink-error") }
func TestRunReportsPublicSinkShutdownFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	a := distribution.CommunityRuntime()
	a.EventSinks = []event.Registration{{ModuleID: "online.agentguide.community.gateway", New: func(context.Context) (event.Sink, error) { return failingCloseSink{}, nil }}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	path := filepath.Join(t.TempDir(), "config.db")
	done := make(chan error, 1)
	go func() { done <- Run(ctx, Options{Assembly: a, Addr: addr, AdminAddr: addr, ConfigStorePath: path}) }()
	client := &http.Client{Timeout: 100 * time.Millisecond}
	deadline := time.After(5 * time.Second)
	for {
		resp, err := client.Get("http://" + addr + "/admin/manifest")
		if err == nil {
			_ = resp.Body.Close()
			break
		}
		select {
		case err := <-done:
			t.Fatal(err)
		case <-deadline:
			t.Fatal("server readiness")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "sink_failure") || strings.Contains(err.Error(), "private-sink-error") {
			t.Fatalf("shutdown result %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown timeout")
	}
}
