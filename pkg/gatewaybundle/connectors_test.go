package gatewaybundle

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/agent"
	"github.com/agent-guide/agent-gateway/pkg/connector"
	connectormcp "github.com/agent-guide/agent-gateway/pkg/connector/mcp"
)

func TestBundleValidatesExplicitHTTPToolSelections(t *testing.T) {
	for _, nested := range []bool{false, true} {
		name := "root"
		if nested {
			name = "nested"
		}
		t.Run(name, func(t *testing.T) {
			var b GatewayBundle
			if err := json.Unmarshal([]byte(`{"apiVersion":"gateway.agw/v1alpha1","kind":"GatewayBundle","connectors":[{"id":"api","name":"API","type":"http_api","http_api":{"base_url":"https://example.org","tools":[{"name":"lookup","description":"Lookup","input_schema":{"type":"object"},"request":{"method":"GET","path":"/lookup"}}]}}],"agents":[{"id":"worker","name":"Worker","runtime":{"type":"builtin","builtin":{"model":{"llm_route_id":"chat"}}},"routes":{"llm_route_ids":["chat"]},"resources":{"connector_ids":["api"]}}]}`), &b); err != nil {
				t.Fatal(err)
			}
			selection := []agent.BuiltinToolSelection{{ConnectorID: "api", Tools: []string{"missing"}}}
			builtin := b.Agents[0].Runtime.Builtin
			if nested {
				builtin.Topology = agent.BuiltinTopology{Kind: agent.TopologyKindSequential, SubAgents: []agent.BuiltinSubAgent{{Name: "child", Tools: selection}}}
			} else {
				builtin.Tools = selection
			}
			if err := b.ValidateForConfigStore(); err == nil || !strings.Contains(err.Error(), "missing") || !strings.Contains(err.Error(), "worker") {
				t.Fatalf("unknown HTTP tool accepted: %v", err)
			}
			selection[0].Tools = []string{"lookup"}
			if err := b.ValidateForConfigStore(); err != nil {
				t.Fatalf("valid selection rejected: %v", err)
			}
			selection[0].Tools = nil
			if err := b.ValidateForConfigStore(); err != nil {
				t.Fatalf("select-all rejected: %v", err)
			}
			selection[0].Tools = []string{"missing"}
			b.Connectors[0].Type = "mcp"
			b.Connectors[0].HTTPAPI = nil
			b.Connectors[0].MCP = &connectormcp.Config{Transport: connector.TransportStdio, Command: "unused"}
			if err := b.ValidateForConfigStore(); err != nil {
				t.Fatalf("unknown remote MCP catalog rejected: %v", err)
			}
			b.Connectors = nil
			if err := b.ValidateForConfigStore(); err != nil {
				t.Fatalf("partial bundle external reference rejected: %v", err)
			}
		})
	}
}

func TestConnectorBundleStrictnessAndLosslessNumbers(t *testing.T) {
	for _, body := range []string{`apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
connectors: []
connectors: []`, `{"apiVersion":"gateway.agw/v1alpha1","kind":"GatewayBundle","mcpServices":[]}`, `apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
connectors:
 - id: api
   name: API
   type: http_api
   http_api:
     base_url: https://example.org
     tools:
      - name: precise
        description: Precision
        input_schema:
          type: object
          properties:
            n: {type: integer, minimum: 9007199254740993}
        request:
          method: GET
          path: /value
          query:
            n: {argument: n}`} {
		b, err := DecodeYAML([]byte(body))
		if strings.Contains(body, "name: precise") {
			if err != nil {
				t.Fatal(err)
			}
			schema := b.Connectors[0].HTTPAPI.Tools[0].InputSchema
			num := schema["properties"].(map[string]any)["n"].(map[string]any)["minimum"]
			if num != json.Number("9007199254740993") {
				t.Fatalf("YAML number rounded: %#v", num)
			}
			if err := b.ValidateForStaticConfig(); err == nil {
				t.Fatal("static connector admitted")
			}
		} else if err == nil {
			t.Fatalf("invalid bundle admitted: %s", body)
		}
	}
}
