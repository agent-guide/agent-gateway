// Package anthropicbase provides shared Anthropic Messages API wire helpers.
package anthropicbase

import "encoding/json"

type MessagesRequest struct {
	Model             string             `json:"model"`
	MaxTokens         int                `json:"max_tokens"`
	Messages          []MessageItem      `json:"messages"`
	System            []SystemBlock      `json:"system,omitempty"`
	Tools             []ToolDef          `json:"tools"`
	ToolChoice        json.RawMessage    `json:"tool_choice,omitempty"`
	Metadata          *RequestMetadata   `json:"metadata,omitempty"`
	Thinking          *ThinkingConfig    `json:"thinking,omitempty"`
	ContextManagement *ContextManagement `json:"context_management,omitempty"`
	OutputConfig      *OutputConfig      `json:"output_config,omitempty"`
	Temperature       float64            `json:"temperature,omitempty"`
	TopP              float64            `json:"top_p,omitempty"`
	TopK              int                `json:"top_k,omitempty"`
	StopSequences     []string           `json:"stop_sequences,omitempty"`
	Stream            bool               `json:"stream,omitempty"`
}

type MessageItem struct {
	Role    string         `json:"role"`
	Content []ContentBlock `json:"content"`
}

type ContentBlock struct {
	Type         string          `json:"type"`
	Text         string          `json:"text,omitempty"`
	CacheControl *CacheControl   `json:"cache_control,omitempty"`
	Source       *ImageSource    `json:"source,omitempty"`
	ID           string          `json:"id,omitempty"`
	Name         string          `json:"name,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	ToolUseID    string          `json:"tool_use_id,omitempty"`
	Content      string          `json:"content,omitempty"`
}

type ImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

type SystemBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text,omitempty"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

type CacheControl struct {
	Type string `json:"type"`
}

type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type RequestMetadata struct {
	UserID string `json:"user_id"`
}

type ThinkingConfig struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens,omitempty"`
}

type ContextManagement struct {
	Edits []ContextManagementEdit `json:"edits,omitempty"`
}

type ContextManagementEdit struct {
	Type string `json:"type"`
	Keep string `json:"keep"`
}

type OutputConfig struct {
	Effort string        `json:"effort,omitempty"`
	Format *OutputFormat `json:"format,omitempty"`
}

// OutputFormat carries an Anthropic structured-output format. Only the
// json_schema type is supported by the Messages API.
type OutputFormat struct {
	Type   string          `json:"type"`
	Schema json.RawMessage `json:"schema,omitempty"`
}

type ResponseBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type MessagesResponse struct {
	Content    []ResponseBlock `json:"content"`
	StopReason string          `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type ModelsResponse struct {
	Data []ModelData `json:"data"`
}

type ModelData struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}
