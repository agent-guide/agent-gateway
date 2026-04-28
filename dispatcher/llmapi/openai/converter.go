package openai

import (
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
)

// Converter converts between OpenAI API format and internal format.
type Converter struct{}

// ChatCompletionRequest is the OpenAI chat completion request format.
type ChatCompletionRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature float64       `json:"temperature,omitempty"`
	TopP        float64       `json:"top_p,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
	Stop        []string      `json:"stop,omitempty"`
}

// ChatMessage is a single message in an OpenAI chat request.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatCompletionResponse is the OpenAI chat completion response format.
type ChatCompletionResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

// Choice is a single choice in an OpenAI chat completion response.
type Choice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

// Usage is token usage information in an OpenAI response.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ToInternal converts an OpenAI ChatCompletionRequest to the internal GenerateRequest.
func (c *Converter) ToInternal(req *ChatCompletionRequest) *provider.GenerateRequest {
	msgs := make([]*schema.Message, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = &schema.Message{Role: schema.RoleType(m.Role), Content: m.Content}
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
	if len(req.Stop) > 0 {
		opts = append(opts, einomodel.WithStop(req.Stop))
	}
	genReq := &provider.GenerateRequest{
		Model:    req.Model,
		Messages: msgs,
		Options:  opts,
	}
	return genReq
}

// FromInternal converts an internal GenerateResponse to a ChatCompletionResponse.
func (c *Converter) FromInternal(resp *provider.GenerateResponse, model string) *ChatCompletionResponse {
	content := messageText(resp.Message)
	usage := provider.UsageFromMessage(resp.Message)
	return &ChatCompletionResponse{
		ID:     "",
		Object: "chat.completion",
		Model:  model,
		Choices: []Choice{{
			Index:        0,
			Message:      ChatMessage{Role: "assistant", Content: content},
			FinishReason: provider.FinishReason(resp.Message),
		}},
		Usage: Usage{
			PromptTokens:     usage.InputTokens,
			CompletionTokens: usage.OutputTokens,
			TotalTokens:      usage.InputTokens + usage.OutputTokens,
		},
	}
}

func messageText(msg *schema.Message) string {
	if msg == nil {
		return ""
	}
	return msg.Content
}
