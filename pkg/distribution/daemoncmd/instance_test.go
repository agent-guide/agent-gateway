package daemoncmd

import (
	"github.com/agent-guide/agent-gateway/pkg/distribution"
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
}
