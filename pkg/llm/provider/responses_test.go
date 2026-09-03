package provider

import (
	"encoding/json"
	"testing"
)

func TestResponsesToolDefinitionPreservesUnknownFields(t *testing.T) {
	raw := []byte(`{
		"type":"web_search",
		"filters":{"allowed_domains":["x.ai"]},
		"enable_image_understanding":true,
		"enable_image_search":false
	}`)
	var tool ResponsesToolDefinition
	if err := json.Unmarshal(raw, &tool); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if tool.Type != "web_search" {
		t.Fatalf("Type = %q, want web_search", tool.Type)
	}
	encoded, err := json.Marshal(tool)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("decode marshaled tool: %v", err)
	}
	if got["type"] != "web_search" {
		t.Fatalf("type = %#v", got["type"])
	}
	filters, _ := got["filters"].(map[string]any)
	domains, _ := filters["allowed_domains"].([]any)
	if len(domains) != 1 || domains[0] != "x.ai" {
		t.Fatalf("filters = %#v, want allowed_domains [x.ai]", got["filters"])
	}
	if got["enable_image_understanding"] != true {
		t.Fatalf("enable_image_understanding = %#v", got["enable_image_understanding"])
	}
	if got["enable_image_search"] != false {
		t.Fatalf("enable_image_search = %#v", got["enable_image_search"])
	}
}

func TestResponsesToolDefinitionMarshalsConstructedTools(t *testing.T) {
	encoded, err := json.Marshal(ResponsesToolDefinition{Type: "web_search", Name: "search"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("decode marshaled tool: %v", err)
	}
	if got["type"] != "web_search" || got["name"] != "search" {
		t.Fatalf("got = %#v", got)
	}
}

func TestResponsesToolDefinitionClearsKnownRawFields(t *testing.T) {
	var tool ResponsesToolDefinition
	if err := json.Unmarshal([]byte(`{
		"type":"function",
		"function":{"name":"lookup","parameters":{"type":"object"}},
		"vendor_extension":true
	}`), &tool); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	tool.Name = tool.Function.Name
	tool.Parameters = tool.Function.Parameters
	tool.Function = nil

	encoded, err := json.Marshal(tool)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("decode marshaled tool: %v", err)
	}
	if _, ok := got["function"]; ok {
		t.Fatalf("function should stay cleared: %#v", got["function"])
	}
	if got["name"] != "lookup" || got["vendor_extension"] != true {
		t.Fatalf("got = %#v, want flattened function with extension preserved", got)
	}
}
