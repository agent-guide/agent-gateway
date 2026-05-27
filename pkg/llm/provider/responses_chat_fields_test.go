package provider

import (
	"reflect"
	"testing"

	einomodel "github.com/cloudwego/eino/components/model"
)

func TestChatCompletionsExtraFieldsFromOptionsEffortStyle(t *testing.T) {
	opts := []einomodel.Option{
		WithResponsesRequestContext(&ResponsesRequestContext{
			Text: map[string]any{
				"format": map[string]any{"type": "json_object"},
			},
			Metadata:          map[string]any{"trace_id": "abc123"},
			User:              "user-1",
			Reasoning:         map[string]any{"effort": "high", "summary": "auto"},
			ParallelToolCalls: boolRef(true),
			Store:             boolRef(false),
		}),
	}

	fields := ChatCompletionsExtraFieldsFromOptions(ReasoningEffortField, opts...)
	if fields["user"] != "user-1" {
		t.Fatalf("user = %#v, want user-1", fields["user"])
	}
	if fields["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort = %#v, want high", fields["reasoning_effort"])
	}
	if fields["parallel_tool_calls"] != true || fields["store"] != false {
		t.Fatalf("fields = %+v, want parallel_tool_calls/store preserved", fields)
	}
	metadata, _ := fields["metadata"].(map[string]any)
	if metadata["trace_id"] != "abc123" {
		t.Fatalf("metadata = %+v, want trace_id", metadata)
	}
	format, _ := fields["response_format"].(map[string]any)
	if format["type"] != "json_object" {
		t.Fatalf("response_format = %+v, want json_object passthrough", format)
	}
	if _, ok := fields["reasoning"]; ok {
		t.Fatalf("effort style should not include raw reasoning object: %+v", fields)
	}
}

func TestChatCompletionsExtraFieldsFromOptionsObjectStyle(t *testing.T) {
	opts := []einomodel.Option{
		WithResponsesRequestContext(&ResponsesRequestContext{
			Metadata: map[string]any{"trace_id": "abc123"},
			User:     "user-1",
			Reasoning: map[string]any{
				"effort":     "medium",
				"max_tokens": 128,
			},
			ParallelToolCalls: boolRef(true),
			Store:             boolRef(true),
		}),
	}

	fields := ChatCompletionsExtraFieldsFromOptions(ReasoningObjectField, opts...)
	if fields["user"] != "user-1" {
		t.Fatalf("user = %#v, want user-1", fields["user"])
	}
	if fields["parallel_tool_calls"] != true || fields["store"] != true {
		t.Fatalf("fields = %+v, want parallel_tool_calls/store preserved", fields)
	}
	reasoning, _ := fields["reasoning"].(map[string]any)
	if reasoning["effort"] != "medium" || reasoning["max_tokens"] != 128 {
		t.Fatalf("reasoning = %+v, want preserved reasoning object", reasoning)
	}
	if _, ok := fields["reasoning_effort"]; ok {
		t.Fatalf("object style should not include reasoning_effort: %+v", fields)
	}
}

func TestChatCompletionsExtraFieldsFromOptionsReshapesJSONSchema(t *testing.T) {
	opts := []einomodel.Option{
		WithResponsesRequestContext(&ResponsesRequestContext{
			Text: map[string]any{
				"format": map[string]any{
					"type":   "json_schema",
					"name":   "weather",
					"schema": map[string]any{"type": "object"},
					"strict": true,
				},
			},
		}),
	}

	fields := ChatCompletionsExtraFieldsFromOptions(ReasoningEffortField, opts...)
	format, ok := fields["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("response_format = %#v, want map", fields["response_format"])
	}
	if format["type"] != "json_schema" {
		t.Fatalf("response_format type = %#v, want json_schema", format["type"])
	}
	jsonSchema, ok := format["json_schema"].(map[string]any)
	if !ok {
		t.Fatalf("response_format = %+v, want nested json_schema object", format)
	}
	want := map[string]any{
		"name":   "weather",
		"schema": map[string]any{"type": "object"},
		"strict": true,
	}
	if !reflect.DeepEqual(jsonSchema, want) {
		t.Fatalf("json_schema = %+v, want %+v", jsonSchema, want)
	}
}

func TestChatCompletionsExtraFieldsFromOptionsChatExtraOverlay(t *testing.T) {
	responseFormat := map[string]any{
		"type":        "json_schema",
		"json_schema": map[string]any{"name": "weather"},
	}
	parallelToolCalls := false
	store := true
	opts := []einomodel.Option{
		WithChatExtraFields(&ChatExtraFields{
			ResponseFormat:    responseFormat,
			Reasoning:         map[string]any{"effort": "medium", "exclude": true},
			ReasoningEffort:   "low",
			User:              "user-1",
			Metadata:          map[string]any{"trace_id": "abc123"},
			ParallelToolCalls: &parallelToolCalls,
			Store:             &store,
		}),
	}

	effort := ChatCompletionsExtraFieldsFromOptions(ReasoningEffortField, opts...)
	if !reflect.DeepEqual(effort["response_format"], responseFormat) {
		t.Fatalf("response_format = %+v, want chat-shaped passthrough", effort["response_format"])
	}
	if effort["reasoning_effort"] != "low" {
		t.Fatalf("reasoning_effort = %#v, want low", effort["reasoning_effort"])
	}
	if effort["user"] != "user-1" {
		t.Fatalf("user = %#v, want user-1", effort["user"])
	}
	metadata, _ := effort["metadata"].(map[string]any)
	if metadata["trace_id"] != "abc123" {
		t.Fatalf("metadata = %+v, want trace_id", effort["metadata"])
	}
	if effort["parallel_tool_calls"] != false {
		t.Fatalf("parallel_tool_calls = %#v, want false", effort["parallel_tool_calls"])
	}
	if effort["store"] != true {
		t.Fatalf("store = %#v, want true", effort["store"])
	}

	object := ChatCompletionsExtraFieldsFromOptions(ReasoningObjectField, opts...)
	reasoning, _ := object["reasoning"].(map[string]any)
	if reasoning["effort"] != "low" {
		t.Fatalf("reasoning = %+v, want effort low for object style", object["reasoning"])
	}
}

func boolRef(v bool) *bool {
	return &v
}
