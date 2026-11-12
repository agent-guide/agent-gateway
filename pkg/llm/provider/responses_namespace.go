package provider

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/agent-guide/agent-gateway/internal/statuserr"
	"github.com/cloudwego/eino/schema"
)

// ResponsesNamespaceMode validates the native Responses endpoint's namespace
// policy. Chat adapters always flatten; native endpoints preserve by default.
func ResponsesNamespaceMode(options map[string]any) (string, error) {
	v, ok := options["responses_tool_namespaces"]
	if !ok {
		return "native", nil
	}
	s, ok := v.(string)
	if !ok || (s != "native" && s != "flatten") {
		return "", fmt.Errorf("responses_tool_namespaces must be native or flatten")
	}
	return s, nil
}

type responsesToolIdentity struct {
	Namespace string
	Name      string
}

// ResponsesToolNames is an immutable execution-local mapping. No mapping is
// stored on a shared provider or in a process-global/session cache.
type ResponsesToolNames struct {
	names    map[string]responsesToolIdentity
	callable map[responsesToolIdentity]string
	groups   map[string]json.RawMessage
}

// ResponsesRestorationError means generation already succeeded and only local
// response reconstruction failed. Retrying with another candidate would start
// a second generation, so execution coordinators must stop fallback.
type ResponsesRestorationError struct{ Err error }

func (e *ResponsesRestorationError) Error() string { return e.Err.Error() }
func (e *ResponsesRestorationError) Unwrap() error { return e.Err }

func namespaceError(message string) error {
	return statuserr.New(http.StatusBadRequest, "responses tools: "+message)
}

func (p *ResponsesToolNames) alias(id responsesToolIdentity) (string, error) {
	if strings.TrimSpace(id.Name) == "" {
		return "", namespaceError("function name is required")
	}
	b, _ := json.Marshal(id)
	sum := sha256.Sum256(b)
	alias := "agw_fn_" + hex.EncodeToString(sum[:20])
	if old, ok := p.names[alias]; ok && old != id {
		return "", namespaceError("tool alias collision")
	}
	p.names[alias] = id
	return alias, nil
}

// FlattenResponsesNamespaces copies and lowers a request without changing the
// caller's tools, choice or history. Historical identities remain replayable
// even after their definitions are removed from the current callable set.
func FlattenResponsesNamespaces(req *ResponsesRequest) (*ResponsesRequest, *ResponsesToolNames, error) {
	if req == nil {
		return nil, nil, namespaceError("request is required")
	}
	active := false
	for _, t := range req.Tools {
		if t.Type == "namespace" {
			active = true
		}
	}
	inputJSON, err := json.Marshal(req.Input)
	if err != nil {
		return nil, nil, err
	}
	var input any
	dec := json.NewDecoder(bytes.NewReader(inputJSON))
	dec.UseNumber()
	if err = dec.Decode(&input); err != nil {
		return nil, nil, err
	}
	if items, ok := input.([]any); ok {
		for _, item := range items {
			if m, ok := item.(map[string]any); ok && m["type"] == "function_call" {
				if _, exists := m["namespace"]; exists {
					active = true
				}
			}
		}
	}
	if !active {
		return req, nil, nil
	}
	if req.PreviousResponseID != "" {
		return nil, nil, namespaceError("flattening requires explicit input history; previous_response_id is unsupported")
	}
	p := &ResponsesToolNames{names: map[string]responsesToolIdentity{}, callable: map[responsesToolIdentity]string{}, groups: map[string]json.RawMessage{}}
	out := *req
	out.Tools = nil
	out.Input = input
	out.ToolChoice = append(json.RawMessage(nil), req.ToolChoice...)
	add := func(t ResponsesToolDefinition, ns string, groupDescription string) error {
		if t.Type != "function" && t.Type != "" {
			return namespaceError("cannot flatten tool type " + t.Type)
		}
		id := responsesToolIdentity{ns, t.Name}
		if t.Function != nil && id.Name == "" {
			id.Name = t.Function.Name
		}
		if _, exists := p.callable[id]; exists {
			return namespaceError("duplicate function identity " + ns + "/" + id.Name)
		}
		alias, err := p.alias(id)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(t)
		if err != nil {
			return namespaceError("invalid function definition")
		}
		var obj map[string]json.RawMessage
		if err = json.Unmarshal(raw, &obj); err != nil {
			return err
		}
		if t.Function != nil {
			raw, err = json.Marshal(t.Function)
			if err != nil {
				return namespaceError("invalid function definition")
			}
			var function map[string]json.RawMessage
			if err = json.Unmarshal(raw, &function); err != nil {
				return err
			}
			for k, v := range function {
				if (k == "name" || k == "description" || k == "parameters") && len(obj[k]) > 0 {
					continue
				}
				obj[k] = v
			}
			delete(obj, "function")
		}
		// These require native semantics or are not represented by the chat bridge.
		for _, field := range []string{"defer_loading"} {
			if v, ok := obj[field]; ok && string(v) != "false" && string(v) != "null" {
				return namespaceError(field + " requires native Responses support")
			}
		}
		obj["type"] = json.RawMessage(`"function"`)
		obj["name"], _ = json.Marshal(alias)
		desc := t.Description
		if t.Function != nil && desc == "" {
			desc = t.Function.Description
		}
		label := id.Name
		if ns != "" {
			label = ns + "." + id.Name
		}
		obj["description"], _ = json.Marshal("Tool " + label + ".\n" + groupDescription + "\n" + desc)

		raw, _ = json.Marshal(obj)
		var flat ResponsesToolDefinition
		if err = json.Unmarshal(raw, &flat); err != nil {
			return err
		}
		p.callable[id] = alias
		out.Tools = append(out.Tools, flat)
		return nil
	}
	groups := map[string]bool{}
	for _, t := range req.Tools {
		if t.Type != "namespace" {
			if err = add(t, "", ""); err != nil {
				return nil, nil, err
			}
			continue
		}
		if strings.TrimSpace(t.Name) == "" {
			return nil, nil, namespaceError("namespace name is required")
		}
		if groups[t.Name] {
			return nil, nil, namespaceError("duplicate namespace " + t.Name)
		}
		groups[t.Name] = true
		raw, err := json.Marshal(t)
		if err != nil {
			return nil, nil, namespaceError("invalid namespace definition")
		}
		var fields map[string]json.RawMessage
		if err = json.Unmarshal(raw, &fields); err != nil {
			return nil, nil, err
		}
		for key, value := range fields {
			switch key {
			case "type", "name", "description", "tools":
			default:
				if string(value) != "null" {
					return nil, nil, namespaceError("namespace field " + key + " requires native Responses support")
				}
			}
		}
		if len(t.Tools) == 0 {
			return nil, nil, namespaceError("namespace tools must not be empty")
		}
		delete(fields, "tools")
		p.groups[t.Name], _ = json.Marshal(fields)
		for _, child := range t.Tools {
			if err = add(child, t.Name, t.Description); err != nil {
				return nil, nil, err
			}
		}
	}

	if items, ok := input.([]any); ok {
		for _, item := range items {
			m, ok := item.(map[string]any)
			if !ok || m["type"] != "function_call" {
				continue
			}
			name, _ := m["name"].(string)
			ns, _ := m["namespace"].(string)
			if v, exists := m["namespace"]; exists && (v == nil || ns == "") {
				return nil, nil, namespaceError("invalid history namespace")
			}
			alias, e := p.alias(responsesToolIdentity{ns, name})
			if e != nil {
				return nil, nil, e
			}
			m["name"] = alias
			delete(m, "namespace")
		}
	}
	if len(out.ToolChoice) > 0 {
		var choice any
		if err = json.Unmarshal(out.ToolChoice, &choice); err != nil {
			return nil, nil, namespaceError("invalid tool_choice")
		}
		if obj, ok := choice.(map[string]any); ok {
			if obj["type"] == "allowed_tools" {
				list, ok := obj["tools"].([]any)
				if !ok || len(list) == 0 {
					return nil, nil, namespaceError("invalid allowed_tools")
				}
				for _, v := range list {
					m, ok := v.(map[string]any)
					if !ok {
						return nil, nil, namespaceError("invalid allowed tool")
					}
					if err = p.rewriteChoice(m); err != nil {
						return nil, nil, err
					}
				}
			} else if err = p.rewriteChoice(obj); err != nil {
				return nil, nil, err
			}
			out.ToolChoice, _ = json.Marshal(obj)
		} else {
			mode, ok := choice.(string)
			if !ok || (mode != "auto" && mode != "none" && mode != "required") {
				return nil, nil, namespaceError("invalid tool_choice")
			}
		}
	}
	return &out, p, nil
}

func (p *ResponsesToolNames) rewriteChoice(obj map[string]any) error {
	if obj["type"] != "function" && obj["type"] != "tool" {
		return namespaceError("unsupported tool_choice for namespace flattening")
	}
	name, _ := obj["name"].(string)
	ns, _ := obj["namespace"].(string)
	if f, ok := obj["function"].(map[string]any); ok {
		name, _ = f["name"].(string)
	}
	if v, exists := obj["namespace"]; exists {
		if _, ok := v.(string); !ok || ns == "" {
			return namespaceError("invalid tool_choice namespace")
		}
	}
	id := responsesToolIdentity{ns, name}
	alias, ok := p.callable[id]
	if !ok && ns == "" {
		count := 0
		for candidate, v := range p.callable {
			if candidate.Name == name {
				alias = v
				count++
			}
		}
		ok = count == 1
	}
	if !ok {
		return namespaceError("unknown or ambiguous tool_choice " + name)
	}
	obj["name"] = alias
	delete(obj, "namespace")
	delete(obj, "function")
	return nil
}

// restoreJSON patches only protocol tool identities; arguments, outputs and
// arbitrary unknown fields are never searched or replaced as strings.
func (p *ResponsesToolNames) restoreJSON(raw []byte) ([]byte, error) {
	if p == nil {
		return raw, nil
	}
	var obj map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&obj); err != nil {
		return nil, err
	}
	var restore func(map[string]any) error
	restore = func(m map[string]any) error {
		if m["type"] == nil || m["object"] == "response" {
			if err := p.restoreEcho(m); err != nil {
				return err
			}
		}
		if m["type"] == "function_call" {
			name, _ := m["name"].(string)
			id, ok := p.callableAlias(name)
			if !ok {
				return fmt.Errorf("responses tools: unknown upstream function %q", name)
			}
			m["name"] = id.Name
			if id.Namespace != "" {
				m["namespace"] = id.Namespace
			} else {
				delete(m, "namespace")
			}
		}
		if items, ok := m["output"].([]any); ok {
			for _, v := range items {
				if item, ok := v.(map[string]any); ok {
					if err := restore(item); err != nil {
						return err
					}
				}
			}
		}
		for _, key := range []string{"item", "response"} {
			if child, ok := m[key].(map[string]any); ok {
				if err := restore(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := restore(obj); err != nil {
		return nil, err
	}
	return json.Marshal(obj)
}

func (p *ResponsesToolNames) RestoreResponse(resp *ResponsesResponse) (*ResponsesResponse, error) {
	out, err := p.restoreResponse(resp)
	if err != nil {
		return nil, &ResponsesRestorationError{Err: err}
	}
	return out, nil
}

func (p *ResponsesToolNames) restoreResponse(resp *ResponsesResponse) (*ResponsesResponse, error) {
	if p == nil || resp == nil {
		return resp, nil
	}
	raw := resp.RawJSON
	var err error
	if len(raw) == 0 {
		raw, err = json.Marshal(resp)
		if err != nil {
			return nil, err
		}
	}
	raw, err = p.restoreJSON(raw)
	if err != nil {
		return nil, err
	}
	var out ResponsesResponse
	if err = json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	out.RawJSON = raw
	return &out, nil
}

// Native response envelopes echo the request's tools and tool choice. Restore
// those protocol fields as well as calls, retaining upstream tool extensions.
func (p *ResponsesToolNames) restoreEcho(obj map[string]any) error {
	if tools, ok := obj["tools"].([]any); ok {
		out := make([]any, 0, len(tools))
		groups := map[string]map[string]any{}
		for _, value := range tools {
			tool, ok := value.(map[string]any)
			if !ok || tool["type"] != "function" {
				return fmt.Errorf("responses tools: invalid echoed tool")
			}
			name, _ := tool["name"].(string)
			id, ok := p.callableAlias(name)
			if !ok {
				return fmt.Errorf("responses tools: unknown echoed function %q", name)
			}
			tool["name"] = id.Name
			if id.Namespace == "" {
				out = append(out, tool)
				continue
			}
			group := groups[id.Namespace]
			if group == nil {
				dec := json.NewDecoder(bytes.NewReader(p.groups[id.Namespace]))
				dec.UseNumber()
				if err := dec.Decode(&group); err != nil {
					return err
				}
				group["tools"] = []any{}
				groups[id.Namespace] = group
				out = append(out, group)
			}
			group["tools"] = append(group["tools"].([]any), tool)
		}
		obj["tools"] = out
	}
	var restoreChoice func(map[string]any) error
	restoreChoice = func(choice map[string]any) error {
		if choice["type"] == "allowed_tools" {
			list, ok := choice["tools"].([]any)
			if !ok {
				return fmt.Errorf("responses tools: invalid echoed allowed_tools")
			}
			for _, value := range list {
				child, ok := value.(map[string]any)
				if !ok {
					return fmt.Errorf("responses tools: invalid echoed allowed tool")
				}
				if err := restoreChoice(child); err != nil {
					return err
				}
			}
			return nil
		}
		name, _ := choice["name"].(string)
		id, ok := p.callableAlias(name)
		if !ok {
			return fmt.Errorf("responses tools: unknown echoed tool_choice %q", name)
		}
		choice["name"] = id.Name
		if id.Namespace != "" {
			choice["namespace"] = id.Namespace
		} else {
			delete(choice, "namespace")
		}
		return nil
	}
	if choice, ok := obj["tool_choice"].(map[string]any); ok {
		return restoreChoice(choice)
	}
	return nil
}

func (p *ResponsesToolNames) RestoreEvent(event *ResponsesStreamEvent) (*ResponsesStreamEvent, error) {
	if p == nil || event == nil {
		return event, nil
	}
	raw := event.RawJSON
	var err error
	if len(raw) == 0 {
		raw, err = json.Marshal(event)
		if err != nil {
			return nil, err
		}
	}
	raw, err = p.restoreJSON(raw)
	if err != nil {
		return nil, err
	}
	var out ResponsesStreamEvent
	if err = json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	out.RawJSON = raw
	return &out, nil
}

// prepareChatStream waits only for a fragmented function name, not for the
// complete call or response. It bounds pending identity/argument data and never
// exposes a partial alias in an output_item.added event.
func (p *ResponsesToolNames) prepareChatStream(stream *schema.StreamReader[*schema.Message]) *schema.StreamReader[*schema.Message] {
	if p == nil {
		return stream
	}
	type pendingCall struct {
		call    schema.ToolCall
		emitted bool
	}
	pending := map[string]*pendingCall{}
	fallback := ""
	pendingBytes := 0
	return schema.StreamReaderWithConvert(stream, func(msg *schema.Message) (*schema.Message, error) {
		if msg == nil {
			return nil, schema.ErrNoValue
		}
		out := *msg
		out.ToolCalls = nil
		for _, tc := range msg.ToolCalls {
			key := toolCallKey(tc, &fallback)
			state := pending[key]
			if state == nil {
				if len(pending) >= 4096 {
					return nil, fmt.Errorf("responses tools: too many streamed calls")
				}
				state = &pendingCall{call: tc}
				state.call.Function.Name = ""
				state.call.Function.Arguments = ""
				pending[key] = state
			}
			if state.emitted {
				if tc.Function.Name != "" && tc.Function.Name != state.call.Function.Name {
					return nil, fmt.Errorf("responses tools: name changed after publication")
				}
				tc.Function.Name = ""
				out.ToolCalls = append(out.ToolCalls, tc)
				continue
			}
			if state.call.ID == "" {
				state.call.ID = tc.ID
			}
			pendingBytes += len(tc.Function.Name) + len(tc.Function.Arguments)
			if pendingBytes > 1<<20 {
				return nil, fmt.Errorf("responses tools: pending stream data exceeds limit")
			}
			state.call.Function.Name += tc.Function.Name
			state.call.Function.Arguments += tc.Function.Arguments
			if len(state.call.Function.Name) > 64 || len(state.call.Function.Arguments) > 1<<20 {
				return nil, fmt.Errorf("responses tools: pending call exceeds limit")
			}
			if _, ok := p.callableAlias(state.call.Function.Name); ok {
				state.emitted = true
				out.ToolCalls = append(out.ToolCalls, state.call)
				pendingBytes -= len(state.call.Function.Name) + len(state.call.Function.Arguments)
				state.call.Function.Arguments = ""
				continue
			}
			possible := false
			for _, alias := range p.callable {
				if strings.HasPrefix(alias, state.call.Function.Name) {
					possible = true
					break
				}
			}
			if !possible {
				return nil, fmt.Errorf("responses tools: unknown upstream function %q", state.call.Function.Name)
			}
		}
		if len(out.ToolCalls) == 0 && len(msg.ToolCalls) > 0 && msg.Content == "" && msg.ReasoningContent == "" && msg.ResponseMeta == nil {
			return nil, schema.ErrNoValue
		}
		return &out, nil
	}, schema.WithOnEOF(func() (any, error) {
		for _, s := range pending {
			if !s.emitted {
				return nil, fmt.Errorf("responses tools: incomplete streamed function name: %w", io.ErrUnexpectedEOF)
			}
		}
		return nil, io.EOF
	}))
}

func (p *ResponsesToolNames) callableAlias(alias string) (responsesToolIdentity, bool) {
	id, ok := p.names[alias]
	if !ok {
		return id, false
	}
	return id, p.callable[id] == alias
}

// Chat's shared tool model cannot enforce strict decoding or deferred discovery.
// Native flattening retains strict and unknown function fields on the wire.
func validateFlattenedChatTools(req *ResponsesRequest) error {
	for _, tool := range req.Tools {
		raw, err := json.Marshal(tool)
		if err != nil {
			return err
		}
		var fields map[string]json.RawMessage
		if err = json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		for key, value := range fields {
			switch key {
			case "type", "name", "description", "parameters":
			case "strict", "defer_loading":
				if string(value) != "false" && string(value) != "null" {
					return namespaceError(key + " requires native Responses support")
				}
			default:
				if string(value) != "null" {
					return namespaceError("function field " + key + " requires native Responses support")
				}
			}
		}
	}
	return nil
}
