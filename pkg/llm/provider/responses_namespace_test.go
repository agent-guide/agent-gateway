package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func namespaceFixture(t *testing.T) *ResponsesRequest {
	t.Helper()
	var req ResponsesRequest
	err := json.Unmarshal([]byte(`{"model":"fixture","input":"inspect","tools":[{"type":"namespace","name":"browser","description":"Browser tools","tools":[{"type":"function","name":"get_status","description":"Status","parameters":{"type":"object","properties":{"path":{"type":"string"}}}}]},{"type":"namespace","name":"computer","tools":[{"type":"function","name":"get_status","parameters":{"type":"object"}}]},{"type":"function","name":"get_status","parameters":{"type":"object"}}]}`), &req)
	if err != nil {
		t.Fatal(err)
	}
	return &req
}

func TestNamespaceFlattenIdentityChoiceHistoryAndImmutability(t *testing.T) {
	req := namespaceFixture(t)
	req.Input = []any{map[string]any{"type": "function_call", "call_id": "old", "namespace": "removed", "name": "get_status", "arguments": "{}"}, map[string]any{"type": "function_call_output", "call_id": "old", "output": "ready"}}
	req.ToolChoice = json.RawMessage(`{"type":"function","name":"get_status","namespace":"computer"}`)
	before, _ := json.Marshal(req)
	flat, p, err := FlattenResponsesNamespaces(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(flat.Tools) != 3 {
		t.Fatalf("lost tools: %+v", flat.Tools)
	}
	seen := map[string]bool{}
	for _, tool := range flat.Tools {
		if seen[tool.Name] || len(tool.Name) > 64 {
			t.Fatalf("invalid alias %q", tool.Name)
		}
		seen[tool.Name] = true
	}
	if !strings.Contains(string(flat.ToolChoice), flat.Tools[1].Name) {
		t.Fatalf("choice=%s", flat.ToolChoice)
	}
	items := flat.Input.([]any)
	history := items[0].(map[string]any)
	if history["namespace"] != nil || history["name"] == "get_status" {
		t.Fatalf("history=%v", history)
	}
	if len(p.callable) != 3 || len(p.names) != 4 {
		t.Fatalf("history must not become callable: %+v", p)
	}
	after, _ := json.Marshal(req)
	if string(before) != string(after) {
		t.Fatal("mutated caller")
	}
	// Reordering and changing the callable set cannot change aliases.
	req.Tools = []ResponsesToolDefinition{req.Tools[1], req.Tools[0]}
	reordered, _, err := FlattenResponsesNamespaces(req)
	if err != nil {
		t.Fatal(err)
	}
	if reordered.Tools[0].Name != flat.Tools[1].Name {
		t.Fatal("alias depends on array order")
	}
	resp, err := p.RestoreResponse(&ResponsesResponse{Output: []ResponsesResponseOutput{{Type: "function_call", CallID: "call", Name: flat.Tools[1].Name, Arguments: `{"literal":"get_status"}`}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Output[0].Namespace != "computer" || resp.Output[0].Name != "get_status" || resp.Output[0].CallID != "call" || resp.Output[0].Arguments != `{"literal":"get_status"}` {
		t.Fatalf("restored=%+v", resp)
	}
}

func TestNamespaceRejectsLossyAndAmbiguousDefinitions(t *testing.T) {
	cases := map[string]string{
		"empty":        "{\"type\":\"namespace\",\"name\":\"a\",\"tools\":[]}",
		"missing_name": `{"type":"namespace","tools":[{"type":"function","name":"x"}]}`,
		"duplicate":    `{"type":"namespace","name":"a","tools":[{"type":"function","name":"x"},{"type":"function","name":"x"}]}`,
		"custom":       `{"type":"namespace","name":"a","tools":[{"type":"custom","name":"x"}]}`,
		"nested":       `{"type":"namespace","name":"a","tools":[{"type":"namespace","name":"b","tools":[{"type":"function","name":"x"}]}]}`,
		"deferred":     `{"type":"namespace","name":"a","tools":[{"type":"function","name":"x","defer_loading":true}]}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			var tool ResponsesToolDefinition
			if err := json.Unmarshal([]byte(raw), &tool); err != nil {
				t.Fatal(err)
			}
			_, _, err := FlattenResponsesNamespaces(&ResponsesRequest{Input: "hi", Tools: []ResponsesToolDefinition{tool}})
			if err == nil {
				t.Fatal("accepted lossy definition")
			}
		})
	}
	req := namespaceFixture(t)
	req.Tools = req.Tools[:2]
	req.ToolChoice = json.RawMessage(`{"type":"function","name":"get_status"}`)
	if _, _, err := FlattenResponsesNamespaces(req); err == nil {
		t.Fatal("accepted ambiguous choice")
	}
	req.ToolChoice = nil
	req.PreviousResponseID = "resp_previous"
	if _, _, err := FlattenResponsesNamespaces(req); err == nil {
		t.Fatal("accepted unavailable server history")
	}
}

func TestNamespaceRestoresRawJSONAndEventsWithoutReplacingArguments(t *testing.T) {
	flat, p, err := FlattenResponsesNamespaces(namespaceFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"type":"response.completed","vendor_counter":9007199254740993,"response":{"output":[{"type":"function_call","call_id":"c","name":"` + flat.Tools[0].Name + `","arguments":"` + flat.Tools[0].Name + `","future":true}]}}`)
	event, err := p.RestoreEvent(&ResponsesStreamEvent{RawJSON: raw})
	if err != nil {
		t.Fatal(err)
	}
	if event.Response.Output[0].Namespace != "browser" {
		t.Fatalf("event=%s", event.RawJSON)
	}
	if !strings.Contains(string(event.RawJSON), `"future":true`) || !strings.Contains(string(event.RawJSON), `"vendor_counter":9007199254740993`) || event.Response.Output[0].Arguments != flat.Tools[0].Name {
		t.Fatalf("lost raw data: %s", event.RawJSON)
	}
	if _, err = p.RestoreResponse(&ResponsesResponse{Output: []ResponsesResponseOutput{{Type: "function_call", Name: "unknown"}}}); err == nil {
		t.Fatal("accepted unknown function")
	}
}

type namespaceProvider struct {
	testResponsesCompatProvider
	chat   func(*ChatRequest) (*ChatResponse, error)
	stream func(*ChatRequest) (*schema.StreamReader[*schema.Message], error)
}

func (p *namespaceProvider) Chat(_ context.Context, r *ChatRequest) (*ChatResponse, error) {
	return p.chat(r)
}
func (p *namespaceProvider) StreamChat(_ context.Context, r *ChatRequest) (*schema.StreamReader[*schema.Message], error) {
	return p.stream(r)
}

func TestNamespaceChatRoundtripAndFragmentedStream(t *testing.T) {
	req := namespaceFixture(t)
	calls := 0
	p := &namespaceProvider{chat: func(r *ChatRequest) (*ChatResponse, error) {
		calls++
		opts := einomodel.GetCommonOptions(nil, r.Options...)
		if len(opts.Tools) != 3 {
			t.Fatalf("tools=%v", opts.Tools)
		}
		if calls == 2 {
			if r.Messages[0].ToolCalls[0].Function.Name != opts.Tools[1].Name || r.Messages[1].ToolCallID != "call" {
				t.Fatalf("history=%+v", r.Messages)
			}
			return &ChatResponse{Message: schema.AssistantMessage("OK", nil)}, nil
		}
		return &ChatResponse{Message: &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "call", Type: "function", Function: schema.FunctionCall{Name: opts.Tools[1].Name, Arguments: "{}"}}}}}, nil
	}}
	resp, err := CreateResponsesViaChat(context.Background(), p, req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(resp.Output[0])
	var history map[string]any
	json.Unmarshal(b, &history)
	req.Input = []any{history, map[string]any{"type": "function_call_output", "call_id": "call", "output": "ready"}}
	if _, err = CreateResponsesViaChat(context.Background(), p, req); err != nil {
		t.Fatal(err)
	}
	p.stream = func(r *ChatRequest) (*schema.StreamReader[*schema.Message], error) {
		opts := einomodel.GetCommonOptions(nil, r.Options...)
		alias := opts.Tools[1].Name
		i := 0
		return schema.StreamReaderFromArray([]*schema.Message{
			{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{Index: &i, ID: "call", Type: "function", Function: schema.FunctionCall{Name: alias[:12], Arguments: `{"p":`}}}},
			{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{Index: &i, Function: schema.FunctionCall{Name: alias[12:], Arguments: `1}`}}}},
		}), nil
	}
	stream, err := StreamResponsesViaChat(context.Background(), p, req)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	seen := map[string]bool{}
	assembledArguments := ""
	for {
		e, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if e.Item != nil && e.Item.Type == "function_call" {
			if e.Item.Namespace != "computer" || e.Item.Name != "get_status" {
				t.Fatalf("item=%+v", e.Item)
			}
			seen[e.Type] = true
			if e.Type == "response.output_item.added" {
				assembledArguments = e.Item.Arguments
			}
		}
		if e.Type == "response.function_call_arguments.delta" {
			assembledArguments += e.Delta
		}
		if e.Type == "response.completed" {
			item := e.Response.Output[0]
			if item.Arguments != `{"p":1}` || item.Namespace != "computer" {
				t.Fatalf("final=%+v", item)
			}
			seen[e.Type] = true
		}
	}
	if assembledArguments != `{"p":1}` {
		t.Fatalf("streamed arguments duplicated: %s", assembledArguments)
	}
	for _, event := range []string{"response.output_item.added", "response.output_item.done", "response.completed"} {
		if !seen[event] {
			t.Errorf("missing %s", event)
		}
	}
}

func TestNamespaceConcurrentMappings(t *testing.T) {
	req := namespaceFixture(t)
	before, _ := json.Marshal(req)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Go(func() {
			flat, p, err := FlattenResponsesNamespaces(req)
			if err != nil {
				t.Error(err)
				return
			}
			_, err = p.RestoreResponse(&ResponsesResponse{Output: []ResponsesResponseOutput{{Type: "function_call", Name: flat.Tools[0].Name}}})
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	after, _ := json.Marshal(req)
	if string(before) != string(after) {
		t.Fatal("mutated shared request")
	}
}

func TestNamespacePolicyValidation(t *testing.T) {
	for _, value := range []any{"native", "flatten"} {
		if _, err := ResponsesNamespaceMode(map[string]any{"responses_tool_namespaces": value}); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []any{nil, true, "", "auto", "FLATTEN"} {
		if _, err := ResponsesNamespaceMode(map[string]any{"responses_tool_namespaces": value}); err == nil {
			t.Errorf("accepted %v", value)
		}
	}
	if mode, err := ResponsesNamespaceMode(nil); err != nil || mode != "native" {
		t.Fatalf("default=%s %v", mode, err)
	}
}

func TestNamespaceIncompleteAndUnknownStreamFailClosed(t *testing.T) {
	flat, p, err := FlattenResponsesNamespaces(namespaceFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{flat.Tools[0].Name[:10], "unknown"} {
		t.Run(name, func(t *testing.T) {
			i := 0
			source := schema.StreamReaderFromArray([]*schema.Message{{ToolCalls: []schema.ToolCall{{Index: &i, ID: "c", Function: schema.FunctionCall{Name: name}}}}})
			stream := p.prepareChatStream(source)
			defer stream.Close()
			failed := false
			for {
				_, err := stream.Recv()
				if err != nil {
					failed = err != io.EOF
					break
				}
			}
			if !failed {
				t.Fatal("accepted incomplete/unknown call")
			}
		})
	}
}

func TestNamespaceRejectsUnsupportedFieldsBeforeChatCall(t *testing.T) {
	for _, nested := range []bool{false, true} {
		for _, field := range []string{`"strict":true`, `"future_semantics":9007199254740993`} {
			t.Run(fmt.Sprintf("nested=%v/%s", nested, field), func(t *testing.T) {
				req := namespaceFixture(t)
				raw := `{"type":"function","name":"get_status",` + field + `}`
				if nested {
					raw = `{"type":"function","function":{"name":"get_status",` + field + `}}`
				}
				var child ResponsesToolDefinition
				if err := json.Unmarshal([]byte(raw), &child); err != nil {
					t.Fatal(err)
				}
				req.Tools[0].Tools = []ResponsesToolDefinition{child}
				before, _ := json.Marshal(req)
				p := &namespaceProvider{
					chat: func(*ChatRequest) (*ChatResponse, error) { t.Fatal("called upstream"); return nil, nil },
					stream: func(*ChatRequest) (*schema.StreamReader[*schema.Message], error) {
						t.Fatal("opened upstream stream")
						return nil, nil
					},
				}
				if _, err := CreateResponsesViaChat(t.Context(), p, req); err == nil {
					t.Fatal("accepted lossy field")
				}
				if _, err := StreamResponsesViaChat(t.Context(), p, req); err == nil {
					t.Fatal("accepted lossy streamed field")
				}
				// Native lowering retains the exact unknown field, including large integers.
				flat, _, err := FlattenResponsesNamespaces(req)
				if err != nil {
					t.Fatal(err)
				}
				wire, err := json.Marshal(flat.Tools[0])
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(wire), field) {
					t.Fatalf("lost native field: %s", wire)
				}
				after, _ := json.Marshal(req)
				if string(before) != string(after) {
					t.Fatal("mutated caller")
				}
			})
		}
	}
}

func TestNamespaceAllowedToolsAndRemovedHistoryCannotBeCalled(t *testing.T) {
	req := namespaceFixture(t)
	req.ToolChoice = json.RawMessage(`{"type":"allowed_tools","mode":"auto","tools":[{"type":"function","namespace":"computer","name":"get_status"}]}`)
	flat, p, err := FlattenResponsesNamespaces(req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(flat.ToolChoice), flat.Tools[1].Name) || strings.Contains(string(flat.ToolChoice), "computer") {
		t.Fatalf("choice=%s", flat.ToolChoice)
	}
	historical, err := p.alias(responsesToolIdentity{"removed", "old"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.RestoreResponse(&ResponsesResponse{Output: []ResponsesResponseOutput{{Type: "function_call", Name: historical}}}); err == nil {
		t.Fatal("accepted removed history as a new tool call")
	}
	// Chat has no equivalent allowed_tools semantics, so reject before sending.
	if _, err = ResponsesToChatRequest(req); err == nil {
		t.Fatal("silently weakened allowed_tools")
	}
}

func TestNamespaceInterleavedParallelStream(t *testing.T) {
	req := namespaceFixture(t)
	p := &namespaceProvider{stream: func(r *ChatRequest) (*schema.StreamReader[*schema.Message], error) {
		opts := einomodel.GetCommonOptions(nil, r.Options...)
		chunks := []*schema.Message{}
		indexes := []int{0, 1}
		for part := 0; part < 2; part++ {
			for i := 0; i < 2; i++ {
				alias := opts.Tools[i].Name
				name := alias[:15]
				id := "call-" + string(rune('a'+i))
				args := "{"
				if part == 1 {
					name = alias[15:]
					id = ""
					args = "}"
				}
				chunks = append(chunks, &schema.Message{ToolCalls: []schema.ToolCall{{Index: &indexes[i], ID: id, Type: "function", Function: schema.FunctionCall{Name: name, Arguments: args}}}})
			}
		}
		return schema.StreamReaderFromArray(chunks), nil
	}}
	stream, err := StreamResponsesViaChat(t.Context(), p, req)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	completed := false
	for {
		event, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if event.Type != "response.completed" {
			continue
		}
		completed = true
		items := event.Response.Output
		if len(items) != 2 {
			t.Fatalf("items=%+v", items)
		}
		for i, ns := range []string{"browser", "computer"} {
			if items[i].Namespace != ns || items[i].Arguments != "{}" || items[i].CallID != "call-"+string(rune('a'+i)) {
				t.Fatalf("item %d=%+v", i, items[i])
			}
		}
	}
	if !completed {
		t.Fatal("missing completion")
	}
}

func TestNamespaceTopLevelFunctionPrecedence(t *testing.T) {
	req := namespaceFixture(t)
	var tool ResponsesToolDefinition
	if err := json.Unmarshal([]byte(`{"type":"function","name":"lookup","description":"top","parameters":{"type":"object"},"function":{"name":"nested","description":"inner","parameters":{"type":"string"},"strict":true}}`), &tool); err != nil {
		t.Fatal(err)
	}
	req.Tools = append(req.Tools, tool)
	flat, names, err := FlattenResponsesNamespaces(req)
	if err != nil {
		t.Fatal(err)
	}
	id, ok := names.callableAlias(flat.Tools[3].Name)
	if !ok || id.Name != "lookup" {
		t.Fatalf("identity=%+v", id)
	}
	wire, _ := json.Marshal(flat.Tools[3])
	if !strings.Contains(string(wire), `"strict":true`) || !strings.Contains(string(wire), `"type":"object"`) || !strings.Contains(flat.Tools[3].Description, "top") {
		t.Fatalf("precedence=%s", wire)
	}
	tool.Function.Name = ""
	req.Tools[3] = tool
	if _, _, err := FlattenResponsesNamespaces(req); err != nil {
		t.Fatalf("top-level name rejected: %v", err)
	}
}

func TestNamespaceRestoresAllowedChoiceEcho(t *testing.T) {
	req := namespaceFixture(t)
	req.ToolChoice = json.RawMessage(`{"type":"allowed_tools","mode":"auto","tools":[{"type":"function","namespace":"computer","name":"get_status"}]}`)
	flat, names, err := FlattenResponsesNamespaces(req)
	if err != nil {
		t.Fatal(err)
	}
	tools, _ := json.Marshal(flat.Tools)
	raw := []byte(`{"output":[],"tools":` + string(tools) + `,"tool_choice":` + string(flat.ToolChoice) + `}`)
	out, err := names.RestoreResponse(&ResponsesResponse{RawJSON: raw})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out.RawJSON), "agw_fn_") || !strings.Contains(string(out.RawJSON), `"namespace":"computer"`) || !strings.Contains(string(out.RawJSON), `"type":"namespace"`) {
		t.Fatalf("unrestored echo: %s", out.RawJSON)
	}
}
