package distributionmetrics

import (
	"encoding/json"
	"github.com/agent-guide/agent-gateway/pkg/distribution"
	"testing"
)

func TestHTTPClientSpanPublicConfigurationAdapter(t *testing.T) {
	public := distribution.MetricsConfig{OTLP: distribution.OTLPConfig{HTTPClientSpans: true, Headers: map[string]string{"test": "one"}}}
	raw, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	var decoded distribution.MetricsConfig
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	internal := From(decoded)
	decoded.OTLP.Headers["test"] = "two"
	if !internal.OTLP.HTTPClientSpans || internal.OTLP.Headers["test"] != "one" {
		t.Fatal("adapter lost HTTP span option or retained headers")
	}
}
