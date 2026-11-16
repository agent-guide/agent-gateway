package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// Custom tools are represented to Chat as functions with one string argument.
// Grammar descriptions are model instructions, not constrained decoding. Native
// Responses execution never enters this compatibility adapter.
type responsesCustomTools map[string]string // alias -> public name; empty means history-only

func customToolAlias(name string) string {
	sum := sha256.Sum256([]byte(name))
	return "agw_custom_" + hex.EncodeToString(sum[:20])
}

// Grammar emulation is deliberately opt-in: it cannot preserve constrained decoding.
func responsesCustomGrammarMode(options map[string]any) (string, error) {
	value, present := options["responses_custom_tool_grammar"]
	if !present {
		return "reject", nil
	}
	mode, ok := value.(string)
	if !ok || (mode != "reject" && mode != "prompt") {
		return "", namespaceError("option responses_custom_tool_grammar must be reject or prompt")
	}
	return mode, nil
}

func lowerResponsesCustomTools(req *ResponsesRequest, options map[string]any) (*ResponsesRequest, responsesCustomTools, error) {
	mode, err := responsesCustomGrammarMode(options)
	if err != nil {
		return nil, nil, err
	}
	if req == nil {
		return nil, nil, namespaceError("request is required")
	}
	active := false
	for _, tool := range req.Tools {
		active = active || tool.Type == "custom"
	}
	items, _ := req.Input.([]any)
	for _, value := range items {
		if item, ok := value.(map[string]any); ok {
			active = active || item["type"] == "custom_tool_call" || item["type"] == "custom_tool_call_output"
		}
	}
	if !active {
		return req, nil, nil
	}
	if req.PreviousResponseID != "" {
		return nil, nil, namespaceError("custom Chat adaptation requires explicit input history; previous_response_id is unsupported")
	}
	out := *req
	out.Tools = append([]ResponsesToolDefinition(nil), req.Tools...)
	custom := responsesCustomTools{}
	for i, tool := range out.Tools {
		if tool.Type != "custom" {
			continue
		}
		if strings.TrimSpace(tool.Name) == "" {
			return nil, nil, namespaceError("custom tool name is required")
		}
		alias := customToolAlias(tool.Name)
		if _, exists := custom[alias]; exists {
			return nil, nil, namespaceError("duplicate custom tool " + tool.Name)
		}
		for _, other := range req.Tools {
			name := other.Name
			if other.Function != nil {
				name = other.Function.Name
			}
			if other.Type != "custom" && (name == alias || name == tool.Name) {
				return nil, nil, namespaceError("ambiguous custom tool " + tool.Name)
			}
		}
		raw, err := json.Marshal(tool)
		if err != nil {
			return nil, nil, err
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return nil, nil, err
		}
		desc := tool.Description + "\nPass the complete, unmodified tool input in the input string."
		for key, value := range fields {
			switch key {
			case "type", "name", "description":
			case "format":
				if string(value) == "null" {
					continue
				}
				var format struct {
					Type       string `json:"type"`
					Syntax     string `json:"syntax"`
					Definition string `json:"definition"`
				}
				if err := json.Unmarshal(value, &format); err != nil {
					return nil, nil, namespaceError("invalid custom tool format")
				}
				var formatFields map[string]json.RawMessage
				_ = json.Unmarshal(value, &formatFields)
				if format.Type == "text" && len(formatFields) == 1 {
					continue
				}
				if format.Type != "grammar" || (format.Syntax != "lark" && format.Syntax != "regex") || format.Definition == "" || len(formatFields) != 3 {
					return nil, nil, namespaceError("unsupported custom tool format")
				}
				if mode != "prompt" {
					return nil, nil, namespaceError(fmt.Sprintf("custom tool %q uses %s grammar, which requires native Responses support; explicitly set options.responses_custom_tool_grammar=prompt to emulate it as model instructions without grammar constrained decoding or validation", tool.Name, format.Syntax))
				}
				desc += "\nThe input must conform to this " + format.Syntax + " grammar:\n" + format.Definition
			case "defer_loading":
				if string(value) != "false" && string(value) != "null" {
					return nil, nil, namespaceError("deferred custom tools require native Responses support")
				}
			default:
				if string(value) != "null" {
					return nil, nil, namespaceError("custom tool field " + key + " requires native Responses support")
				}
			}
		}
		custom[alias] = tool.Name
		out.Tools[i] = ResponsesToolDefinition{Type: "function", Name: alias, Description: desc,
			Parameters: json.RawMessage(`{"type":"object","properties":{"input":{"type":"string"}},"required":["input"],"additionalProperties":false}`)}
	}
	if items != nil {
		outItems := make([]any, len(items))
		for i, value := range items {
			outItems[i] = value
			item, ok := value.(map[string]any)
			if !ok || (item["type"] != "custom_tool_call" && item["type"] != "custom_tool_call_output") {
				continue
			}
			copy := make(map[string]any, len(item))
			for key, value := range item {
				copy[key] = value
			}
			if item["type"] == "custom_tool_call" {
				if ns, ok := item["namespace"]; ok && ns != nil && ns != "" {
					return nil, nil, namespaceError("namespaced custom history requires native Responses support")
				}
				name, ok := item["name"].(string)
				input, inputOK := item["input"].(string)
				if !ok || name == "" || !inputOK {
					return nil, nil, namespaceError("invalid custom tool history")
				}
				alias := customToolAlias(name)
				if _, declared := custom[alias]; !declared {
					custom[alias] = "" // Historical calls do not declare callable tools.
				}
				arguments, _ := json.Marshal(map[string]string{"input": input})
				copy["type"], copy["name"], copy["arguments"] = "function_call", customToolAlias(name), string(arguments)
				delete(copy, "input")
			} else {
				copy["type"] = "function_call_output"
			}
			outItems[i] = copy
		}
		out.Input = outItems
	}
	if len(req.ToolChoice) > 0 {
		var choice map[string]any
		if json.Unmarshal(req.ToolChoice, &choice) == nil && choice["type"] == "custom" {
			name, _ := choice["name"].(string)
			alias := customToolAlias(name)
			if custom[alias] != name || name == "" || len(choice) != 2 {
				return nil, nil, namespaceError("invalid custom tool_choice")
			}
			out.ToolChoice, _ = json.Marshal(map[string]string{"type": "function", "name": alias})
		}
	}
	return &out, custom, nil
}

// prepareChatStream buffers custom aliases until their identity is complete.
// Ordinary names may have arbitrary fragments and prefix relationships, so wait
// for arguments (or EOF) when a name is ambiguous rather than guessing.
func (c responsesCustomTools) prepareChatStream(stream *schema.StreamReader[*schema.Message], req *ResponsesRequest) *schema.StreamReader[*schema.Message] {
	sr, sw := schema.Pipe[*schema.Message](16)
	go func() {
		defer stream.Close()
		defer sw.Close()
		type pendingCall struct {
			call    schema.ToolCall
			emitted bool
		}
		pending := map[string]*pendingCall{}
		var order []string
		fallback := ""
		pendingBytes := 0
		ordinary := map[string]bool{}
		for _, tool := range req.Tools {
			name := tool.Name
			if tool.Function != nil {
				name = tool.Function.Name
			}
			if _, custom := c[name]; !custom {
				ordinary[name] = true
			}
		}
		validateName := func(name string) error {
			if _, custom := c[name]; custom || ordinary[name] {
				return nil
			}
			for alias := range c {
				if strings.HasPrefix(alias, name) {
					return &ResponsesRestorationError{Err: fmt.Errorf("incomplete custom tool name: %w", io.ErrUnexpectedEOF)}
				}
			}
			return &ResponsesRestorationError{Err: fmt.Errorf("unknown upstream function %q", name)}
		}
		for {
			msg, err := stream.Recv()
			if err != nil {
				if err == io.EOF {
					for _, key := range order {
						state := pending[key]
						if !state.emitted {
							if err := validateName(state.call.Function.Name); err != nil {
								sw.Send(nil, err)
								return
							}
							if sw.Send(&schema.Message{ToolCalls: []schema.ToolCall{state.call}}, nil) {
								return
							}
						}
					}
				} else {
					sw.Send(nil, err)
				}
				return
			}
			if msg == nil {
				continue
			}
			out := *msg
			out.ToolCalls = nil
			for _, tc := range msg.ToolCalls {
				key := toolCallKey(tc, &fallback)
				state := pending[key]
				if state == nil {
					if len(pending) >= 4096 {
						sw.Send(nil, &ResponsesRestorationError{Err: fmt.Errorf("too many streamed calls")})
						return
					}
					state = &pendingCall{call: tc}
					state.call.Function.Name, state.call.Function.Arguments = "", ""
					pending[key] = state
					order = append(order, key)
				}
				if state.emitted {
					if tc.Function.Name != "" && tc.Function.Name != state.call.Function.Name {
						sw.Send(nil, &ResponsesRestorationError{Err: fmt.Errorf("tool name changed after publication")})
						return
					}
					tc.Function.Name = ""
					out.ToolCalls = append(out.ToolCalls, tc)
					continue
				}
				if state.call.ID == "" {
					state.call.ID = tc.ID
				}
				state.call.Function.Name += tc.Function.Name
				state.call.Function.Arguments += tc.Function.Arguments
				pendingBytes += len(tc.Function.Name)
				if pendingBytes > 1<<20 {
					sw.Send(nil, &ResponsesRestorationError{Err: fmt.Errorf("pending custom stream data exceeds 1 MiB")})
					return
				}
				_, custom := c[state.call.Function.Name]
				ambiguous := false
				for alias := range c {
					if alias != state.call.Function.Name && strings.HasPrefix(alias, state.call.Function.Name) {
						ambiguous = true
						break
					}
				}
				for name := range ordinary {
					if name != state.call.Function.Name && strings.HasPrefix(name, state.call.Function.Name) {
						ambiguous = true
						break
					}
				}
				if ((custom || ordinary[state.call.Function.Name]) && !ambiguous) || state.call.Function.Arguments != "" {
					if err := validateName(state.call.Function.Name); err != nil {
						sw.Send(nil, err)
						return
					}
					state.emitted = true
					out.ToolCalls = append(out.ToolCalls, state.call)
					pendingBytes -= len(state.call.Function.Name)
					state.call.Function.Arguments = ""
				}
			}
			if len(out.ToolCalls) == 0 && len(msg.ToolCalls) > 0 && msg.Content == "" && msg.ReasoningContent == "" && msg.ResponseMeta == nil {
				continue
			}
			if sw.Send(&out, nil) {
				return
			}
		}
	}()
	return sr
}

func (c responsesCustomTools) restoreItem(item *ResponsesResponseOutput, complete bool) error {
	if item.Type != "function_call" || item.Namespace != "" {
		return nil
	}
	name, ok := c[item.Name]
	if !ok {
		return nil
	}
	if name == "" {
		return &ResponsesRestorationError{Err: fmt.Errorf("upstream called a history-only custom tool")}
	}
	input := ""
	if complete {
		var args map[string]json.RawMessage
		if err := json.Unmarshal([]byte(item.Arguments), &args); err != nil || len(args) != 1 {
			return &ResponsesRestorationError{Err: fmt.Errorf("custom tool %q: expected an input string argument", name)}
		}
		raw, ok := args["input"]
		if !ok || string(raw) == "null" || json.Unmarshal(raw, &input) != nil {
			return &ResponsesRestorationError{Err: fmt.Errorf("custom tool %q: invalid input string", name)}
		}
	}
	item.Type, item.Name, item.Arguments, item.Input = "custom_tool_call", name, "", &input
	return nil
}

func (c responsesCustomTools) restoreResponse(resp *ResponsesResponse) (*ResponsesResponse, error) {
	if c == nil || resp == nil {
		return resp, nil
	}
	out := cloneResponsesResponse(resp)
	for i := range out.Output {
		if err := c.restoreItem(&out.Output[i], true); err != nil {
			return nil, err
		}
	}
	// The Chat bridge produces this envelope locally; rebuild any namespace raw
	// view after restoring custom identities so it cannot leak function wrappers.
	var err error
	out.RawJSON = nil
	out.RawJSON, err = json.Marshal(out)
	return out, err
}

func (c responsesCustomTools) restoreStream(stream *schema.StreamReader[*ResponsesStreamEvent]) *schema.StreamReader[*ResponsesStreamEvent] {
	if c == nil {
		return stream
	}
	sr, sw := schema.Pipe[*ResponsesStreamEvent](16)
	go func() {
		defer stream.Close()
		defer sw.Close()
		customIndexes := map[int]int{}
		for {
			event, err := stream.Recv()
			if err != nil {
				if err != io.EOF {
					sw.Send(nil, err)
				}
				return
			}
			if event == nil {
				continue
			}
			out := *event
			out.RawJSON = nil
			if out.Type == "response.function_call_arguments.delta" {
				if size, ok := customIndexes[out.OutputIndex]; ok {
					size += len(out.Delta)
					if size > 1<<20 {
						sw.Send(nil, &ResponsesRestorationError{Err: fmt.Errorf("custom tool input exceeds 1 MiB")})
						return
					}
					customIndexes[out.OutputIndex] = size
					continue // JSON escapes cannot be exposed as custom input deltas.
				}
			}
			if out.Item != nil {
				item := *out.Item
				out.Item = &item
				_, custom := c[item.Name]
				if item.Type == "function_call" && item.Namespace == "" && custom {
					complete := out.Type == "response.output_item.done"
					if complete && len(item.Arguments) > 1<<20 {
						sw.Send(nil, &ResponsesRestorationError{Err: fmt.Errorf("custom tool input exceeds 1 MiB")})
						return
					}
					if err := c.restoreItem(&item, complete); err != nil {
						sw.Send(nil, err)
						return
					}
					if complete {
						if *item.Input != "" && sw.Send(&ResponsesStreamEvent{Type: "response.custom_tool_call_input.delta", ItemID: item.ID, OutputIndex: out.OutputIndex, Delta: *item.Input}, nil) {
							return
						}
						if sw.Send(&ResponsesStreamEvent{Type: "response.custom_tool_call_input.done", ItemID: item.ID, OutputIndex: out.OutputIndex, Input: item.Input}, nil) {
							return
						}
						delete(customIndexes, out.OutputIndex)
					} else {
						customIndexes[out.OutputIndex] = 0
					}
				}
			}
			if out.Response != nil {
				out.Response, err = c.restoreResponse(out.Response)
				if err != nil {
					sw.Send(nil, err)
					return
				}
			}
			if sw.Send(&out, nil) {
				return
			}
		}
	}()
	return sr
}
