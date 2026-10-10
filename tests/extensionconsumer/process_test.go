package extensionconsumer

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/distribution/contracttest"
	"github.com/agent-guide/agent-gateway/pkg/extension"
	"github.com/agent-guide/agent-gateway/pkg/gateway/virtualkey/limiter"
)

func TestDistributionProcessContract(t *testing.T) {
	dir := os.Getenv("AGW_DISTRIBUTION_BIN_DIR")
	if dir == "" {
		t.Skip("run make test-extension-consumer")
	}
	contracttest.Distribution(t, contracttest.ProcessFixture{
		BinDir: dir, Runtime: Runtime(), Validation: Validation(),
		ProbePath: "/admin/extensions/example.fixture.entries/probe", EventFileEnv: "AGW_FIXTURE_EVENT_FILE",
		Envelopes: []extension.Envelope{
			{ModuleID: "example.fixture.entries", SchemaVersion: 1, Config: json.RawMessage(`{"message":"process-proof"}`)},
			{ModuleID: fixtureLimiterID, SchemaVersion: 1, Config: json.RawMessage(`{"namespace":"process","mode":"healthy"}`)},
			{ModuleID: limiter.ModuleID, SchemaVersion: 1, Config: json.RawMessage(`{"backend_module_id":"example.fixture.limiter","backend_type":"fixture"}`)},
		},
	})
}
