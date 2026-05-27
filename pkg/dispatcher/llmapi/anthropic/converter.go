package anthropic

import (
	"encoding/json"
	"strings"

	einojsonschema "github.com/eino-contrib/jsonschema"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

// Converter converts between Anthropic API format and internal format.
type Converter struct{}

// ToInternal converts an Anthropic MessagesRequest to the internal ChatRequest.
func (c *Converter) ToInternal(req *MessagesRequest) *provider.ChatRequest {
	msgs := make([]*schema.Message, 0, len(req.Messages)+1)
	if systemText := req.System.Text(); systemText != "" {
		msgs = append(msgs, schema.SystemMessage(systemText))
	}
	for _, m := range req.Messages {
		msgs = append(msgs, convertMessageItem(m)...)
	}

	var opts []einomodel.Option
	if req.Temperature != 0 {
		opts = append(opts, einomodel.WithTemperature(float32(req.Temperature)))
	}
	if req.TopP != 0 {
		opts = append(opts, einomodel.WithTopP(float32(req.TopP)))
	}
	if req.MaxTokens > 0 {
		opts = append(opts, einomodel.WithMaxTokens(req.MaxTokens))
	}
	if len(req.StopSequences) > 0 {
		opts = append(opts, einomodel.WithStop(req.StopSequences))
	}

	if len(req.Tools) > 0 {
		opts = append(opts, einomodel.WithTools(toolDefsToToolInfos(req.Tools)))
	}
	if len(req.ToolChoice) > 0 {
		if tc, names, ok := parseAnthropicToolChoice(req.ToolChoice); ok {
			opts = append(opts, einomodel.WithToolChoice(tc, names...))
		}
	}

	if req.TopK > 0 {
		opts = append(opts, provider.WithTopK(req.TopK))
	}

	return &provider.ChatRequest{
		Model:    req.Model,
		Messages: msgs,
		Options:  opts,
	}
}

func toolDefsToToolInfos(defs []ToolDefinition) []*schema.ToolInfo {
	tools := make([]*schema.ToolInfo, 0, len(defs))
	for _, td := range defs {
		var js einojsonschema.Schema
		if err := json.Unmarshal(td.InputSchema, &js); err != nil {
			tools = append(tools, &schema.ToolInfo{Name: td.Name, Desc: td.Description})
			continue
		}
		tools = append(tools, &schema.ToolInfo{
			Name:        td.Name,
			Desc:        td.Description,
			ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&js),
		})
	}
	return tools
}

func parseAnthropicToolChoice(raw json.RawMessage) (schema.ToolChoice, []string, bool) {
	var tc struct {
		Type string `json:"type"`
		Name string `json:"name,omitempty"`
	}
	if err := json.Unmarshal(raw, &tc); err != nil {
		return "", nil, false
	}
	switch tc.Type {
	case "auto":
		return schema.ToolChoiceAllowed, nil, true
	case "any":
		return schema.ToolChoiceForced, nil, true
	case "tool":
		return schema.ToolChoiceForced, []string{tc.Name}, true
	case "none":
		return schema.ToolChoiceForbidden, nil, true
	default:
		return "", nil, false
	}
}

// convertMessageItem converts one Anthropic MessageItem to one or more schema.Messages.
func convertMessageItem(m MessageItem) []*schema.Message {
	switch m.Role {
	case "assistant":
		return convertAssistantItem(m.Content)
	case "user":
		return convertUserItem(m.Content)
	default:
		// Treat unknown roles as user.
		return convertUserItem(m.Content)
	}
}

func convertAssistantItem(content MessageContent) []*schema.Message {
	var textParts []string
	var toolCalls []schema.ToolCall
	for _, block := range content {
		switch block.Type {
		case "text":
			if block.Text != "" {
				textParts = append(textParts, block.Text)
			}
		case "tool_use":
			inputStr := ""
			if len(block.Input) > 0 {
				inputStr = string(block.Input)
			}
			toolCalls = append(toolCalls, schema.ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: schema.FunctionCall{
					Name:      block.Name,
					Arguments: inputStr,
				},
			})
		}
	}

	if len(textParts) == 0 && len(toolCalls) == 0 {
		return nil
	}
	return []*schema.Message{{
		Role:      schema.Assistant,
		Content:   strings.Join(textParts, "\n"),
		ToolCalls: toolCalls,
	}}
}

func convertUserItem(content MessageContent) []*schema.Message {
	var inputParts []schema.MessageInputPart
	var result []*schema.Message

	flushInputParts := func() {
		if len(inputParts) == 0 {
			return
		}
		msg := &schema.Message{Role: schema.User}
		if len(inputParts) == 1 && inputParts[0].Type == schema.ChatMessagePartTypeText {
			msg.Content = inputParts[0].Text
		} else {
			msg.UserInputMultiContent = inputParts
		}
		result = append(result, msg)
		inputParts = nil
	}

	for _, block := range content {
		switch block.Type {
		case "text":
			if block.Text != "" {
				inputParts = append(inputParts, schema.MessageInputPart{
					Type: schema.ChatMessagePartTypeText,
					Text: block.Text,
				})
			}
		case "image":
			if block.Source != nil {
				part := schema.MessageInputPart{
					Type:  schema.ChatMessagePartTypeImageURL,
					Image: &schema.MessageInputImage{},
				}
				switch block.Source.Type {
				case "base64":
					part.Image.Base64Data = &block.Source.Data
					part.Image.MIMEType = block.Source.MediaType
				case "url":
					part.Image.URL = &block.Source.URL
				}
				inputParts = append(inputParts, part)
			}
		case "tool_result":
			flushInputParts()
			result = append(result, &schema.Message{
				Role:       schema.Tool,
				Content:    block.Content.Text(),
				ToolCallID: block.ToolUseID,
			})
		}
	}
	flushInputParts()
	return result
}

// FromInternal converts an internal ChatResponse to an Anthropic MessagesResponse.
func (c *Converter) FromInternal(resp *provider.ChatResponse, model string) *MessagesResponse {
	content := contentFromMessage(resp.Message)
	usage := provider.UsageFromMessage(resp.Message)
	stopReason := mapFinishReason(provider.FinishReason(resp.Message))
	if stopReason == "" && resp.Message != nil && len(resp.Message.ToolCalls) > 0 {
		stopReason = "tool_use"
	}
	return &MessagesResponse{
		ID:         "",
		Type:       "message",
		Role:       "assistant",
		Model:      model,
		Content:    content,
		StopReason: stopReason,
		Usage: UsageResponse{
			InputTokens:  usage.InputTokens,
			OutputTokens: usage.OutputTokens,
		},
	}
}

// mapFinishReason maps OpenAI-style finish reasons to Anthropic stop reasons.
func mapFinishReason(reason string) string {
	switch reason {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls", "function_call", "tool_use":
		return "tool_use"
	case "content_filter":
		return "end_turn"
	case "end_turn", "max_tokens", "stop_sequence":
		return reason
	default:
		if reason == "" {
			return ""
		}
		return "end_turn"
	}
}

func contentFromMessage(msg *schema.Message) []ContentBlockResponse {
	if msg == nil {
		return []ContentBlockResponse{}
	}
	var blocks []ContentBlockResponse
	if msg.Content != "" {
		blocks = append(blocks, ContentBlockResponse{Type: "text", Text: msg.Content})
	}
	for _, tc := range msg.ToolCalls {
		// Arguments is already a JSON string; pass it through as raw JSON.
		var inputRaw json.RawMessage
		if tc.Function.Arguments != "" {
			inputRaw = json.RawMessage(tc.Function.Arguments)
		} else {
			inputRaw = json.RawMessage("{}")
		}
		blocks = append(blocks, ContentBlockResponse{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: inputRaw,
		})
	}
	return blocks
}

func contentText(blocks []ContentBlock) string {
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// --- Anthropic API types ---

type MessagesRequest struct {
	Model         string           `json:"model"`
	MaxTokens     int              `json:"max_tokens"`
	Messages      []MessageItem    `json:"messages"`
	System        MessageContent   `json:"system,omitempty"`
	Temperature   float64          `json:"temperature,omitempty"`
	TopP          float64          `json:"top_p,omitempty"`
	TopK          int              `json:"top_k,omitempty"`
	Stream        bool             `json:"stream,omitempty"`
	StopSequences []string         `json:"stop_sequences,omitempty"`
	Tools         []ToolDefinition `json:"tools,omitempty"`
	ToolChoice    json.RawMessage  `json:"tool_choice,omitempty"`
	Metadata      json.RawMessage  `json:"metadata,omitempty"`
}

type MessageItem struct {
	Role    string         `json:"role"`
	Content MessageContent `json:"content"`
}

// MessageContent holds the content of a message. Per the Anthropic API spec,
// content may be either a plain string or an array of content blocks.
type MessageContent []ContentBlock

// UnmarshalJSON accepts both a JSON string and a JSON array of content blocks.
func (mc *MessageContent) UnmarshalJSON(data []byte) error {
	var blocks []ContentBlock
	if err := json.Unmarshal(data, &blocks); err == nil {
		*mc = blocks
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	if s != "" {
		*mc = []ContentBlock{{Type: "text", Text: s}}
	}
	return nil
}

// Text joins all text blocks into a single string.
func (mc MessageContent) Text() string {
	return contentText(mc)
}

type ContentBlock struct {
	Type string `json:"type"`
	// type=text
	Text string `json:"text,omitempty"`
	// type=image
	Source *ImageSource `json:"source,omitempty"`
	// type=tool_use (assistant messages)
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// type=tool_result (user messages)
	ToolUseID string         `json:"tool_use_id,omitempty"`
	Content   MessageContent `json:"content,omitempty"`
	IsError   bool           `json:"is_error,omitempty"`
	// shared
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

type ImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

type CacheControl struct {
	Type string `json:"type"`
}

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type MessagesResponse struct {
	ID         string                 `json:"id"`
	Type       string                 `json:"type"`
	Role       string                 `json:"role"`
	Content    []ContentBlockResponse `json:"content"`
	Model      string                 `json:"model"`
	StopReason string                 `json:"stop_reason,omitempty"`
	Usage      UsageResponse          `json:"usage"`
}

type ContentBlockResponse struct {
	Type string `json:"type"`
	// type=text
	Text string `json:"text,omitempty"`
	// type=tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type UsageResponse struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}
