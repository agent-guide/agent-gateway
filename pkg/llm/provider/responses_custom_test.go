package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// Captured from a real Codex 0.167.1 request sent to a loopback mock endpoint.
func codexCustomRequest(t *testing.T) *ResponsesRequest {
	t.Helper()
	raw, err := os.ReadFile("testdata/codex_apply_patch_tool.json")
	if err != nil {
		t.Fatal(err)
	}
	var tool ResponsesToolDefinition
	if err := json.Unmarshal(raw, &tool); err != nil {
		t.Fatal(err)
	}
	return &ResponsesRequest{Model: "gpt-5.5", Input: "Create hello.txt", Tools: []ResponsesToolDefinition{tool}, ToolChoice: json.RawMessage(`{"type":"custom","name":"apply_patch"}`)}
}

func TestCodexCustomChatRoundTrip(t *testing.T) {
	req := codexCustomRequest(t)
	before, _ := json.Marshal(req)
	patch := "*** Begin Patch\n*** Add File: hello.txt\n+你好 \\\"world\\\"\n*** End Patch\n"
	args, _ := json.Marshal(map[string]string{"input": patch})
	p := &namespaceProvider{chat: func(r *ChatRequest) (*ChatResponse, error) {
		opts := einomodel.GetCommonOptions(nil, r.Options...)
		if len(opts.Tools) != 1 || !strings.Contains(opts.Tools[0].Desc, "start: begin_patch hunk+ end_patch") {
			t.Fatalf("lost grammar: %+v", opts.Tools)
		}
		params, err := opts.Tools[0].ParamsOneOf.ToJSONSchema()
		if err != nil {
			t.Fatal(err)
		}
		inputParam, ok := params.Properties.Get("input")
		if !ok || inputParam.Type != "string" {
			t.Fatalf("parameters=%+v err=%v", params, err)
		}
		if len(opts.AllowedToolNames) != 1 || opts.AllowedToolNames[0] != opts.Tools[0].Name {
			t.Fatalf("choice=%+v", opts)
		}
		return &ChatResponse{Message: &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "call_patch", Type: "function", Function: schema.FunctionCall{Name: opts.Tools[0].Name, Arguments: string(args)}}}}}, nil
	}}
	resp, err := CreateResponsesViaChat(t.Context(), withCustomGrammarPrompt(p), req)
	if err != nil {
		t.Fatal(err)
	}
	item := resp.Output[0]
	if item.Type != "custom_tool_call" || item.Name != "apply_patch" || item.CallID != "call_patch" || item.Input == nil || *item.Input != patch || item.Arguments != "" {
		t.Fatalf("item=%+v", item)
	}
	if strings.Contains(string(resp.RawJSON), "agw_custom_") {
		t.Fatalf("leaked wrapper: %s", resp.RawJSON)
	}
	after, _ := json.Marshal(req)
	if string(before) != string(after) {
		t.Fatal("mutated request")
	}
	// Simulate the next native Codex turn, including a removed tool declaration.
	req.Input = []any{map[string]any{"type": item.Type, "name": item.Name, "call_id": item.CallID, "input": *item.Input}, map[string]any{"type": "custom_tool_call_output", "call_id": item.CallID, "output": "Success"}}
	req.Tools = nil
	req.ToolChoice = nil
	chat, err := ResponsesToChatRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(chat.Messages) != 2 || chat.Messages[0].ToolCalls[0].Function.Arguments != string(args) || chat.Messages[1].ToolCallID != "call_patch" || chat.Messages[1].Content != "Success" {
		t.Fatalf("history=%+v", chat.Messages)
	}
}

func TestCodexCustomChatStream(t *testing.T) {
	for _, input := range []string{"", "*** Begin Patch\n*** Add File: hello.txt\n+你好\n*** End Patch\n"} {
		t.Run(input, func(t *testing.T) {
			req := codexCustomRequest(t)
			args, _ := json.Marshal(map[string]string{"input": input})
			p := &namespaceProvider{stream: func(r *ChatRequest) (*schema.StreamReader[*schema.Message], error) {
				name := einomodel.GetCommonOptions(nil, r.Options...).Tools[0].Name
				index := 0
				messages := []*schema.Message{{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{Index: &index, ID: "call_patch", Type: "function", Function: schema.FunctionCall{Name: name[:8]}}}}, {Role: schema.Assistant, ToolCalls: []schema.ToolCall{{Index: &index, Function: schema.FunctionCall{Name: name[8:]}}}}}
				for _, b := range args {
					messages = append(messages, &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{Index: &index, Function: schema.FunctionCall{Arguments: string([]byte{b})}}}})
				}
				return schema.StreamReaderFromArray(messages), nil
			}}
			stream, err := StreamResponsesViaChat(t.Context(), withCustomGrammarPrompt(p), req)
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			var delta strings.Builder
			var types []string
			for {
				event, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				types = append(types, event.Type)
				if strings.Contains(event.Type, "function_call_arguments") {
					t.Fatalf("leaked event: %+v", event)
				}
				switch event.Type {
				case "response.output_item.added":
					if event.Item.Type != "custom_tool_call" || event.Item.Input == nil || *event.Item.Input != "" {
						t.Fatalf("added=%+v", event.Item)
					}
				case "response.custom_tool_call_input.delta":
					delta.WriteString(event.Delta)
				case "response.custom_tool_call_input.done":
					if event.Input == nil || *event.Input != input {
						t.Fatalf("done=%+v", event)
					}
				case "response.completed":
					if event.Response.Output[0].Input == nil || *event.Response.Output[0].Input != input {
						t.Fatalf("completed=%+v", event.Response)
					}
				}
			}
			if delta.String() != input || types[len(types)-1] != "response.completed" || !strings.Contains(strings.Join(types, ","), "response.custom_tool_call_input.done,response.output_item.done") {
				t.Fatalf("events=%v delta=%q", types, delta.String())
			}
		})
	}
}

func TestCustomChatRejectsInvalidOutputWithoutRetry(t *testing.T) {
	for _, args := range []string{`{`, `{}`, `{"input":null}`, `{"input":3}`, `{"input":"ok","extra":true}`} {
		p := &namespaceProvider{chat: func(r *ChatRequest) (*ChatResponse, error) {
			return &ChatResponse{Message: &schema.Message{ToolCalls: []schema.ToolCall{{Function: schema.FunctionCall{Name: einomodel.GetCommonOptions(nil, r.Options...).Tools[0].Name, Arguments: args}}}}}, nil
		}}
		_, err := CreateResponsesViaChat(t.Context(), withCustomGrammarPrompt(p), codexCustomRequest(t))
		var restoration *ResponsesRestorationError
		if !errors.As(err, &restoration) {
			t.Fatalf("arguments=%s error=%v", args, err)
		}
	}
}

func TestCustomChatRejectsUnsupportedSemantics(t *testing.T) {
	for _, raw := range []string{`{"type":"custom","name":"x","defer_loading":true}`, `{"type":"custom","name":"x","future":true}`, `{"type":"custom","name":"x","format":{"type":"other"}}`} {
		var tool ResponsesToolDefinition
		if err := json.Unmarshal([]byte(raw), &tool); err != nil {
			t.Fatal(err)
		}
		if _, err := ResponsesToChatRequest(&ResponsesRequest{Input: "test", Tools: []ResponsesToolDefinition{tool}}); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestCustomChatAlongsideFunctionNamespaces(t *testing.T) {
	req := codexCustomRequest(t)
	req.Tools = append(req.Tools, ResponsesToolDefinition{Type: "namespace", Name: "files", Tools: []ResponsesToolDefinition{{Type: "function", Name: "read"}}})
	p := &namespaceProvider{chat: func(r *ChatRequest) (*ChatResponse, error) {
		tools := einomodel.GetCommonOptions(nil, r.Options...).Tools
		return &ChatResponse{Message: &schema.Message{ToolCalls: []schema.ToolCall{
			{ID: "patch", Function: schema.FunctionCall{Name: tools[0].Name, Arguments: `{"input":"patch text"}`}},
			{ID: "read", Function: schema.FunctionCall{Name: tools[1].Name, Arguments: `{}`}},
		}}}, nil
	}}
	resp, err := CreateResponsesViaChat(t.Context(), withCustomGrammarPrompt(p), req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Output[0].Type != "custom_tool_call" || resp.Output[0].Name != "apply_patch" || resp.Output[1].Type != "function_call" || resp.Output[1].Name != "read" || resp.Output[1].Namespace != "files" {
		t.Fatalf("output=%+v", resp.Output)
	}
	if strings.Contains(string(resp.RawJSON), "agw_") {
		t.Fatalf("leaked aliases: %s", resp.RawJSON)
	}
}

func TestCustomChatStreamRejectsInvalidArguments(t *testing.T) {
	for _, args := range []string{`{"input":null}`, strings.Repeat("x", (1<<20)+1)} {
		p := &namespaceProvider{stream: func(r *ChatRequest) (*schema.StreamReader[*schema.Message], error) {
			name := einomodel.GetCommonOptions(nil, r.Options...).Tools[0].Name
			return schema.StreamReaderFromArray([]*schema.Message{{ToolCalls: []schema.ToolCall{{ID: "call", Function: schema.FunctionCall{Name: name, Arguments: args}}}}}), nil
		}}
		stream, err := StreamResponsesViaChat(t.Context(), withCustomGrammarPrompt(p), codexCustomRequest(t))
		if err != nil {
			t.Fatal(err)
		}
		failed := false
		for {
			event, err := stream.Recv()
			if err != nil {
				failed = err != io.EOF
				break
			}
			if event.Type == "response.completed" {
				t.Fatal("published success for invalid custom input")
			}
		}
		stream.Close()
		if !failed {
			t.Fatal("accepted invalid custom arguments")
		}
	}
}

// Tests of Codex grammar emulation must explicitly select the lossy mode.
type customGrammarPromptProvider struct{ *namespaceProvider }

func (p *customGrammarPromptProvider) Config() ProviderConfig {
	return ProviderConfig{ProviderType: "test", Options: map[string]any{"responses_custom_tool_grammar": "prompt"}}
}
func withCustomGrammarPrompt(p *namespaceProvider) Provider {
	return &customGrammarPromptProvider{namespaceProvider: p}
}

func TestCustomGrammarRequiresExplicitPromptMode(t *testing.T) {
	for _, mode := range []any{nil, "reject", "prompt", "PROMPT", "", true, 1} {
		t.Run(fmt.Sprintf("%v", mode), func(t *testing.T) {
			for _, syntax := range []string{"lark", "regex"} {
				req := codexCustomRequest(t)
				raw := fmt.Sprintf(`{"type":"custom","name":"apply_patch","format":{"type":"grammar","syntax":%q,"definition":"start: text"}}`, syntax)
				if err := json.Unmarshal([]byte(raw), &req.Tools[0]); err != nil {
					t.Fatal(err)
				}
				var options map[string]any
				if mode != nil {
					options = map[string]any{"responses_custom_tool_grammar": mode}
				}
				before, _ := json.Marshal(req)
				lowered, _, err := lowerResponsesCustomTools(req, options)
				if mode == "prompt" {
					if err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(lowered.Tools[0].Description, syntax+" grammar:\nstart: text") {
						t.Fatal("lost grammar instruction")
					}
				} else if err == nil {
					t.Fatal("implicitly enabled grammar emulation")
				}
				after, _ := json.Marshal(req)
				if string(before) != string(after) {
					t.Fatal("mutated input")
				}
			}
		})
	}
}

func TestCustomGrammarRejectedBeforeUpstreamByDefault(t *testing.T) {
	p := &namespaceProvider{
		chat: func(*ChatRequest) (*ChatResponse, error) { t.Fatal("called upstream"); return nil, nil },
		stream: func(*ChatRequest) (*schema.StreamReader[*schema.Message], error) {
			t.Fatal("opened upstream stream")
			return nil, nil
		},
	}
	req := codexCustomRequest(t)
	for _, run := range []func() error{
		func() error { _, err := CreateResponsesViaChat(t.Context(), p, req); return err },
		func() error { _, err := StreamResponsesViaChat(t.Context(), p, req); return err },
		func() error { _, err := ResponsesToChatRequest(req); return err },
	} {
		err := run()
		if err == nil || !strings.Contains(err.Error(), `"apply_patch"`) || !strings.Contains(err.Error(), "responses_custom_tool_grammar=prompt") {
			t.Fatalf("unhelpful rejection: %v", err)
		}
	}
}

func TestCustomTextToolsWorkWithoutGrammarOptIn(t *testing.T) {
	for _, format := range []string{"", `,"format":{"type":"text"}`} {
		req := &ResponsesRequest{Input: "test", Tools: make([]ResponsesToolDefinition, 1)}
		if err := json.Unmarshal([]byte(`{"type":"custom","name":"echo"`+format+`}`), &req.Tools[0]); err != nil {
			t.Fatal(err)
		}
		p := &namespaceProvider{chat: func(r *ChatRequest) (*ChatResponse, error) {
			name := einomodel.GetCommonOptions(nil, r.Options...).Tools[0].Name
			return &ChatResponse{Message: &schema.Message{ToolCalls: []schema.ToolCall{{ID: "text", Function: schema.FunctionCall{Name: name, Arguments: `{"input":"hello"}`}}}}}, nil
		}}
		resp, err := CreateResponsesViaChat(t.Context(), p, req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.Output[0].Type != "custom_tool_call" || *resp.Output[0].Input != "hello" {
			t.Fatalf("output=%+v", resp.Output)
		}
	}
}

func TestCustomGrammarPromptDoesNotEnableOtherNativeTools(t *testing.T) {
	for _, raw := range []string{
		`{"type":"tool_search"}`,
		`{"type":"custom","name":"deferred","defer_loading":true}`,
		`{"type":"custom","name":"unknown","future_semantics":true}`,
	} {
		req := codexCustomRequest(t)
		var tool ResponsesToolDefinition
		if err := json.Unmarshal([]byte(raw), &tool); err != nil {
			t.Fatal(err)
		}
		req.Tools = append(req.Tools, tool)
		p := withCustomGrammarPrompt(&namespaceProvider{
			chat: func(*ChatRequest) (*ChatResponse, error) { t.Fatal("called upstream"); return nil, nil },
			stream: func(*ChatRequest) (*schema.StreamReader[*schema.Message], error) {
				t.Fatal("opened upstream stream")
				return nil, nil
			},
		})
		if _, err := CreateResponsesViaChat(t.Context(), p, req); err == nil {
			t.Fatalf("accepted %s", raw)
		}
		if _, err := StreamResponsesViaChat(t.Context(), p, req); err == nil {
			t.Fatalf("accepted streamed %s", raw)
		}
	}
}

func TestCustomChatStreamMixedCalls(t *testing.T) {
	alias := customToolAlias("edit")
	for _, lateID := range []string{"", "late-id"} {
		t.Run("id="+lateID, func(t *testing.T) {
			req := &ResponsesRequest{Input: "test", Tools: []ResponsesToolDefinition{
				{Type: "custom", Name: "edit"}, {Type: "function", Name: "read"}, {Type: "function", Name: "read_file"},
				{Type: "function", Name: alias + "_file"}, {Type: "function", Name: "agw_"},
			}}
			p := &namespaceProvider{stream: func(*ChatRequest) (*schema.StreamReader[*schema.Message], error) {
				var messages []*schema.Message
				// Separate indexes must remain distinct even when every ID is empty.
				for idx, fragments := range [][]string{{alias[:8], alias[8:]}, {"read", "_file"}, {alias, "_file"}, {"agw_"}} {
					index := idx
					for _, name := range fragments {
						messages = append(messages, &schema.Message{ToolCalls: []schema.ToolCall{{Index: &index, Function: schema.FunctionCall{Name: name}}}})
					}
					args := `{}`
					if idx == 0 {
						args = `{"input":"hello"}`
					}
					messages = append(messages, &schema.Message{ToolCalls: []schema.ToolCall{{Index: &index, ID: lateID, Function: schema.FunctionCall{Arguments: args}}}})
				}
				return schema.StreamReaderFromArray(messages), nil
			}}
			stream, err := StreamResponsesViaChat(t.Context(), p, req)
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			ordinaryDeltas := map[int]string{}
			var completed *ResponsesResponse
			for {
				e, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if e.Type == "response.function_call_arguments.delta" {
					if e.OutputIndex == 0 {
						t.Fatalf("leaked wrapper: %+v", e)
					}
					ordinaryDeltas[e.OutputIndex] += e.Delta
				}
				if e.Type == "response.completed" {
					completed = e.Response
				}
			}
			if completed == nil || len(completed.Output) != 4 {
				t.Fatalf("completed=%+v", completed)
			}
			if completed.Output[0].Input == nil || *completed.Output[0].Input != "hello" {
				t.Fatal(completed.Output[0])
			}
			for i, name := range []string{"read_file", alias + "_file", "agw_"} {
				if completed.Output[i+1].Name != name || ordinaryDeltas[i+1] != "{}" {
					t.Fatalf("output=%+v deltas=%v", completed.Output, ordinaryDeltas)
				}
			}
		})
	}
}

func TestCustomStreamArgumentLimit(t *testing.T) {
	alias := customToolAlias("edit")
	for _, delta := range []bool{false, true} {
		t.Run(fmt.Sprint(delta), func(t *testing.T) {
			args := `{"input":"` + strings.Repeat("x", 1<<20) + `"}`
			events := []*ResponsesStreamEvent{{Type: "response.output_item.added", OutputIndex: 0, Item: &ResponsesResponseOutput{Type: "function_call", Name: alias}}}
			if delta {
				events = append(events, &ResponsesStreamEvent{Type: "response.function_call_arguments.delta", OutputIndex: 0, ItemID: "late", Delta: args})
			}
			events = append(events, &ResponsesStreamEvent{Type: "response.output_item.done", OutputIndex: 0, Item: &ResponsesResponseOutput{Type: "function_call", Name: alias, ID: "late", Arguments: args}})
			stream := responsesCustomTools{alias: "edit"}.restoreStream(schema.StreamReaderFromArray(events))
			defer stream.Close()
			for {
				e, err := stream.Recv()
				if err != nil {
					var restoration *ResponsesRestorationError
					if !errors.As(err, &restoration) {
						t.Fatalf("expected restoration error, got %v", err)
					}
					break
				}
				if e.Type == "response.function_call_arguments.delta" || e.Type == "response.custom_tool_call_input.delta" {
					t.Fatalf("leaked oversized input: %s", e.Type)
				}
			}
		})
	}
}

func TestCustomChatStreamOrdinaryNameAtEOF(t *testing.T) {
	req := &ResponsesRequest{Tools: []ResponsesToolDefinition{{Type: "function", Name: "read"}, {Type: "function", Name: "read_file"}}}
	index := 0
	stream := responsesCustomTools{customToolAlias("edit"): "edit"}.prepareChatStream(schema.StreamReaderFromArray([]*schema.Message{{ToolCalls: []schema.ToolCall{{Index: &index, Function: schema.FunctionCall{Name: "read"}}}}}), req)
	defer stream.Close()
	msg, err := stream.Recv()
	if err != nil || len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Function.Name != "read" {
		t.Fatalf("msg=%+v err=%v", msg, err)
	}
	if _, err := stream.Recv(); err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestCustomChatStreamOrdinaryArgumentsAreNotCustomLimited(t *testing.T) {
	name := strings.Repeat("f", 65)
	args := strings.Repeat("x", (1<<20)+1)
	req := &ResponsesRequest{Tools: []ResponsesToolDefinition{{Type: "function", Name: name}}}
	index := 0
	stream := responsesCustomTools{customToolAlias("edit"): "edit"}.prepareChatStream(schema.StreamReaderFromArray([]*schema.Message{
		{ToolCalls: []schema.ToolCall{{Index: &index, Function: schema.FunctionCall{Name: name[:32]}}}},
		{ToolCalls: []schema.ToolCall{{Index: &index, Function: schema.FunctionCall{Name: name[32:], Arguments: args}}}},
	}), req)
	defer stream.Close()
	msg, err := stream.Recv()
	if err != nil || len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Function.Name != name || msg.ToolCalls[0].Function.Arguments != args {
		t.Fatalf("ordinary call altered or rejected: %v", err)
	}
}

func TestCustomChatStreamIncompleteAliasAtEOF(t *testing.T) {
	alias := customToolAlias("edit")
	stream := responsesCustomTools{alias: "edit"}.prepareChatStream(schema.StreamReaderFromArray([]*schema.Message{{ToolCalls: []schema.ToolCall{{Function: schema.FunctionCall{Name: alias[:8]}}}}}), &ResponsesRequest{})
	defer stream.Close()
	_, err := stream.Recv()
	var restoration *ResponsesRestorationError
	if !errors.As(err, &restoration) || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("expected incomplete alias restoration error, got %v", err)
	}
}

func TestCustomRestorationPreservesNamespacedAlias(t *testing.T) {
	alias := customToolAlias("edit")
	custom := responsesCustomTools{alias: "edit"}
	item := ResponsesResponseOutput{Type: "function_call", Name: alias, Namespace: "files", Arguments: `{}`}
	resp, err := custom.restoreResponse(&ResponsesResponse{Output: []ResponsesResponseOutput{item}})
	if err != nil || resp.Output[0].Type != "function_call" || resp.Output[0].Namespace != "files" {
		t.Fatalf("response=%+v err=%v", resp, err)
	}
	stream := custom.restoreStream(schema.StreamReaderFromArray([]*ResponsesStreamEvent{
		ResponsesOutputItemAddedEvent(0, &item),
		{Type: "response.function_call_arguments.delta", OutputIndex: 0, Delta: `{}`},
		ResponsesOutputItemDoneEvent(0, &item),
	}))
	defer stream.Close()
	count := 0
	for {
		event, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(event.Type, "custom") {
			t.Fatal(event.Type)
		}
		count++
	}
	if count != 3 {
		t.Fatal(count)
	}
}

func TestHistoryOnlyCustomCallRejected(t *testing.T) {
	req := &ResponsesRequest{Model: "test", Input: []any{map[string]any{"type": "custom_tool_call", "name": "edit", "input": "old", "call_id": "old"}, map[string]any{"type": "custom_tool_call_output", "call_id": "old", "output": "ok"}}}
	p := &namespaceProvider{chat: func(*ChatRequest) (*ChatResponse, error) {
		return &ChatResponse{Message: &schema.Message{ToolCalls: []schema.ToolCall{{ID: "new", Function: schema.FunctionCall{Name: customToolAlias("edit"), Arguments: `{"input":"new"}`}}}}}, nil
	}}
	_, err := CreateResponsesViaChat(t.Context(), p, req)
	var restoration *ResponsesRestorationError
	if !errors.As(err, &restoration) {
		t.Fatalf("expected restoration error, got %v", err)
	}
}
