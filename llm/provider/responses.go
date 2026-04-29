package provider

import (
	"context"

	"github.com/cloudwego/eino/schema"
)

// ResponsesProvider is an optional interface for providers that expose
// OpenAI-compatible Responses API semantics directly.
type ResponsesProvider interface {
	Provider
	CreateResponses(ctx context.Context, req *ResponsesRequest) (*ResponsesResponse, error)
	StreamResponses(ctx context.Context, req *ResponsesRequest) (*schema.StreamReader[*ResponsesStreamEvent], error)
}

// ResponsesRequest is the minimal provider-level request model for the OpenAI
// Responses API. Input is intentionally preserved as structured JSON to avoid
// collapsing it into the chat-only ChatRequest abstraction.
type ResponsesRequest struct {
	Model              string         `json:"model"`
	Input              any            `json:"input"`
	MaxOutputTokens    int            `json:"max_output_tokens,omitempty"`
	Temperature        float64        `json:"temperature,omitempty"`
	TopP               float64        `json:"top_p,omitempty"`
	Stream             bool           `json:"stream,omitempty"`
	Instructions       string         `json:"instructions,omitempty"`
	PreviousResponseID string         `json:"previous_response_id,omitempty"`
	Store              *bool          `json:"store,omitempty"`
	Text               map[string]any `json:"text,omitempty"`
}

// ResponsesResponse mirrors the OpenAI-compatible Responses API envelope.
type ResponsesResponse struct {
	ID        string                    `json:"id"`
	Object    string                    `json:"object"`
	CreatedAt int64                     `json:"created_at"`
	Model     string                    `json:"model"`
	Output    []ResponsesResponseOutput `json:"output"`
	Usage     *ResponsesResponseUsage   `json:"usage,omitempty"`
}

type ResponsesResponseOutput struct {
	ID      string                         `json:"id,omitempty"`
	Type    string                         `json:"type"`
	Role    string                         `json:"role"`
	Status  string                         `json:"status,omitempty"`
	Content []ResponsesResponseContentPart `json:"content"`
}

type ResponsesResponseContentPart struct {
	Type        string `json:"type"`
	Text        string `json:"text"`
	Annotations []any  `json:"annotations"`
}

type ResponsesResponseUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// ResponsesStreamEvent is the minimal event model currently required by the
// gateway for OpenAI-compatible Responses API streaming.
type ResponsesStreamEvent struct {
	Type         string             `json:"type"`
	Response     *ResponsesResponse `json:"response,omitempty"`
	Delta        string             `json:"delta,omitempty"`
	ItemID       string             `json:"item_id,omitempty"`
	OutputIndex  int                `json:"output_index,omitempty"`
	ContentIndex int                `json:"content_index,omitempty"`
}
