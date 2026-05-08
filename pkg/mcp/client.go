package mcp

import "context"

// ClientConfig is the configuration for an MCP client.
type ClientConfig struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Transport  TransportType     `json:"transport"`
	Command    string            `json:"command,omitempty"` // stdio only
	Args       []string          `json:"args,omitempty"`    // stdio only
	URL        string            `json:"url,omitempty"`     // sse/websocket
	Env        map[string]string `json:"env,omitempty"`
	AutoAuth   bool              `json:"auto_auth,omitempty"`
	AuthConfig *AuthConfig       `json:"auth,omitempty"`
}

// AuthConfig contains MCP authentication configuration.
type AuthConfig struct {
	Type     string `json:"type"` // api_key, oauth2, basic
	APIKey   string `json:"api_key,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// Tool represents a tool exposed by an MCP server.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// ToolResult is the result of calling an MCP tool.
type ToolResult struct {
	Content string `json:"content"`
	IsError bool   `json:"is_error"`
}

// Resource represents a resource exposed by an MCP server.
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mime_type,omitempty"`
}

// ResourceContent is the content of a resource.
type ResourceContent struct {
	URI      string `json:"uri"`
	MimeType string `json:"mime_type"`
	Text     string `json:"text,omitempty"`
	Blob     []byte `json:"blob,omitempty"`
}

// Prompt represents a prompt template exposed by an MCP server.
type Prompt struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// PromptResult is the result of getting a prompt.
type PromptResult struct {
	Description string `json:"description,omitempty"`
	Messages    []any  `json:"messages"`
}

// Client represents a connection to an MCP server.
type Client interface {
	ID() string
	Name() string
	Status() ClientStatus
	Connect(ctx context.Context) error
	Disconnect(ctx context.Context) error
	ListTools(ctx context.Context) ([]Tool, error)
	CallTool(ctx context.Context, name string, args map[string]any) (*ToolResult, error)
	ListResources(ctx context.Context) ([]Resource, error)
	ReadResource(ctx context.Context, uri string) (*ResourceContent, error)
	ListPrompts(ctx context.Context) ([]Prompt, error)
	GetPrompt(ctx context.Context, name string, args map[string]any) (*PromptResult, error)
}
