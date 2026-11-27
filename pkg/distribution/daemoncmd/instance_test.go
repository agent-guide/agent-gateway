package daemoncmd

import (
	"context"
	"github.com/agent-guide/agent-gateway/pkg/connector/egress"
	"github.com/agent-guide/agent-gateway/pkg/distribution"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewCommandOwnsOptionsAndFlags(t *testing.T) {
	opts := Options{Server: ServerOptions{ProviderTypes: []string{"openai"}, Metrics: distribution.MetricsConfig{OTLP: distribution.OTLPConfig{Headers: map[string]string{"key": "original"}}}}}
	a, b := NewCommand(opts), NewCommand(opts)
	opts.Server.ProviderTypes[0] = "mutated"
	opts.Server.Metrics.OTLP.Headers["key"] = "mutated"
	if got, _ := a.Flags().GetStringArray("provider-type"); len(got) != 1 || got[0] != "openai" {
		t.Fatalf("caller slice retained: %v", got)
	}
	if got, _ := a.Flags().GetStringToString("metrics-otlp-header"); got["key"] != "original" {
		t.Fatalf("caller map retained: %v", got)
	}
	if err := a.Flags().Set("addr", "127.0.0.1:9001"); err != nil {
		t.Fatal(err)
	}
	if got, _ := b.Flags().GetString("addr"); got != "127.0.0.1:8080" {
		t.Fatalf("shared flags: %q", got)
	}
	if err := a.Flags().Set("max-llm-request-body-bytes", "67108864"); err != nil {
		t.Fatal(err)
	}
	if got, _ := b.Flags().GetInt64("max-llm-request-body-bytes"); got != 16<<20 {
		t.Fatalf("shared body-limit flag or wrong default: %d", got)
	}
	custom := NewCommand(Options{Server: ServerOptions{MaxLLMRequestBodyBytes: 32 << 20}})
	if got, _ := custom.Flags().GetInt64("max-llm-request-body-bytes"); got != 32<<20 {
		t.Fatalf("distribution body limit lost: %d", got)
	}
}

func TestBodyLimitFlagReachesServerValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.db")
	cmd := NewCommand(Options{Assembly: distribution.CommunityRuntime()})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--max-llm-request-body-bytes", "-1", "--config-store", path})
	// Bound the failure path if the option is accidentally dropped by assembly.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := cmd.ExecuteContext(ctx)
	if err == nil || !strings.Contains(err.Error(), "max_llm_request_body_bytes must not be negative") {
		t.Fatalf("body-limit flag was not validated by server: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("invalid flag created storage: %v", err)
	}
}

func TestConnectorEgressFlagsOwnOrigins(t *testing.T) {
	opts := Options{Server: ServerOptions{ConnectorEgress: egress.Policy{Mode: "public_only", AllowOrigins: []string{"http://orders.internal:8080"}}}}
	a, b := NewCommand(opts), NewCommand(opts)
	opts.Server.ConnectorEgress.AllowOrigins[0] = "http://mutated"
	origins, _ := a.Flags().GetStringArray("connector-egress-allow-origin")
	if len(origins) != 1 || origins[0] != "http://orders.internal:8080" {
		t.Fatalf("caller origins retained: %v", origins)
	}
	if err := a.Flags().Set("connector-egress-allow-origin", "http://another.internal"); err != nil {
		t.Fatal(err)
	}
	origins, _ = b.Flags().GetStringArray("connector-egress-allow-origin")
	if len(origins) != 1 || origins[0] != "http://orders.internal:8080" {
		t.Fatalf("shared origins: %v", origins)
	}
	if a.Flags().Lookup("http-api-policy-file") != nil {
		t.Fatal("legacy policy flag retained")
	}
}

func TestConnectorEgressFlagReachesServerValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.db")
	cmd := NewCommand(Options{Assembly: distribution.CommunityRuntime()})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--connector-egress-mode", "invalid", "--config-store", path})
	err := cmd.ExecuteContext(t.Context())
	if err == nil || !strings.Contains(err.Error(), "connector_egress mode") {
		t.Fatalf("egress flag not validated: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("invalid egress created storage: %v", err)
	}
}

func TestRejectLegacyACPLaunchersFileFlag(t *testing.T) {
	cmd := NewCommand(Options{Assembly: distribution.CommunityRuntime()})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--acp-launchers-file", "legacy.json"})
	if err := cmd.ExecuteContext(t.Context()); err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("legacy ACP launcher flag error = %v", err)
	}
}
