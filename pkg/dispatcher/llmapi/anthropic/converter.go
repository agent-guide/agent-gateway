package anthropic

import (
	"encoding/json"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

// Converter converts between Anthropic API format and internal format.
type Converter struct{}

// ToInternal converts an Anthropic MessagesRequest to the internal ChatRequest.
func (c *Converter) ToInternal(req *MessagesRequest) *provider.ChatRequest {
	msgs := make([]*schema.Message, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, schema.SystemMessage(req.System))
	}
	for _, m := range req.Messages {
		msgs = append(msgs, &schema.Message{
			Role:    schema.RoleType(m.Role),
			Content: contentText(m.Content),
		})
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

	chatReq := &provider.ChatRequest{
		Model:    req.Model,
		Messages: msgs,
		Options:  opts,
	}
	return chatReq
}

// FromInternal converts an internal ChatResponse to an Anthropic MessagesResponse.
func (c *Converter) FromInternal(resp *provider.ChatResponse, model string) *MessagesResponse {
	content := convertResponseContent(resp)
	usage := provider.UsageFromMessage(resp.Message)
	return &MessagesResponse{
		ID:         "",
		Type:       "message",
		Role:       "assistant",
		Model:      model,
		Content:    content,
		StopReason: provider.FinishReason(resp.Message),
		Usage: UsageResponse{
			InputTokens:  usage.InputTokens,
			OutputTokens: usage.OutputTokens,
		},
	}
}

func convertResponseContent(resp *provider.ChatResponse) []ContentBlockResponse {
	return contentFromMessage(resp.Message)
}

func contentFromMessage(msg *schema.Message) []ContentBlockResponse {
	if msg == nil {
		return nil
	}

	content := make([]ContentBlockResponse, 0, 1+len(msg.ToolCalls))
	if msg.Content != "" {
		content = append(content, ContentBlockResponse{Type: "text", Text: msg.Content})
	}
	for range msg.ToolCalls {
		content = append(content, ContentBlockResponse{Type: "tool_use"})
	}
	return content
}

func contentText(blocks []ContentBlock) string {
	var out string
	for i, b := range blocks {
		if i > 0 && out != "" && b.Text != "" {
			out += "\n"
		}
		out += b.Text
	}
	return out
}

// --- Anthropic API types ---

type MessagesRequest struct {
	Model         string        `json:"model"`
	MaxTokens     int           `json:"max_tokens"`
	Messages      []MessageItem `json:"messages"`
	System        string        `json:"system,omitempty"`
	Temperature   float64       `json:"temperature,omitempty"`
	TopP          float64       `json:"top_p,omitempty"`
	Stream        bool          `json:"stream,omitempty"`
	StopSequences []string      `json:"stop_sequences,omitempty"`
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
	// Try array first (most common from SDK >= 0.20).
	var blocks []ContentBlock
	if err := json.Unmarshal(data, &blocks); err == nil {
		*mc = blocks
		return nil
	}
	// Fall back to plain string (SDK may send this for simple user messages).
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	if s != "" {
		*mc = []ContentBlock{{Type: "text", Text: s}}
	}
	return nil
}

type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
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
	Text string `json:"text,omitempty"`
}

type UsageResponse struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}
