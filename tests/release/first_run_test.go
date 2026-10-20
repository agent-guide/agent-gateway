package release_test

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/distribution"
	"github.com/agent-guide/agent-gateway/pkg/gatewaybundle"
)

var prometheusMetricPattern = regexp.MustCompile(`agentgateway_[a-z_]+_total`)

func TestFirstRunBundleIsLocallyValid(t *testing.T) {
	root := repositoryRoot(t)
	registry, err := distribution.CommunityValidation().Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(root, "examples", "first-run", "gateway.bundle.yaml"),
		filepath.Join(root, "examples", "gateway.bundle.acp-evaluation.yaml"),
	} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			bundle, err := gatewaybundle.LoadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := bundle.ValidateWithRegistry(t.Context(), registry); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPackagedFirstRunBundleMatchesSource(t *testing.T) {
	packaged := os.Getenv("AGW_PACKAGED_FIRST_RUN_DIR")
	if packaged == "" {
		t.Skip("set AGW_PACKAGED_FIRST_RUN_DIR when verifying the release asset")
	}
	root := repositoryRoot(t)
	source, err := os.ReadFile(filepath.Join(root, "examples", "first-run", "gateway.bundle.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	copy, err := os.ReadFile(filepath.Join(packaged, "gateway.bundle.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(source, copy) {
		t.Fatal("packaged first-run Bundle differs from its checked-in source")
	}
}

func TestFirstRunDashboardUsesPublishedMetrics(t *testing.T) {
	root := repositoryRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "examples", "first-run", "observability", "grafana", "dashboards", "agent-gateway.json"))
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		"agentgateway_usage_requests_total":       true,
		"agentgateway_usage_failures_total":       true,
		"agentgateway_usage_tokens_total":         true,
		"agentgateway_usage_dropped_events_total": true,
		"agentgateway_usage_write_failures_total": true,
		"agentgateway_limiter_admissions_total":   true,
	}
	for _, metric := range prometheusMetricPattern.FindAllString(string(raw), -1) {
		if !allowed[metric] {
			t.Errorf("dashboard references unpublished metric %q", metric)
		}
	}
	if bytes.Contains(raw, []byte("agentgateway_usage_latency")) {
		t.Fatal("dashboard invents a Prometheus latency series")
	}
}

func TestFirstRunPublishesGrafanaOnlyWithObservabilityOverride(t *testing.T) {
	root := repositoryRoot(t)
	base := readFile(t, filepath.Join(root, "examples", "first-run", "compose.yaml"))
	override := readFile(t, filepath.Join(root, "examples", "first-run", "compose.observability.yaml"))
	port := "127.0.0.1:3000:3000"
	if strings.Contains(base, port) {
		t.Fatal("base first-run Compose project reserves the optional Grafana port")
	}
	if !strings.Contains(override, port) {
		t.Fatal("observability Compose override does not publish Grafana on host loopback")
	}
	if !strings.Contains(base, "GF_SERVER_HTTP_ADDR: 0.0.0.0") {
		t.Fatal("Grafana cannot receive traffic forwarded to the shared container namespace")
	}
}
