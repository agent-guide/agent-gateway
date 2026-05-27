// Package openaibase provides shared OpenAI-compatible wire types
// still used for model listing and embeddings.
package openaibase

type responseStreamEventEnvelope struct {
	Type         string `json:"type"`
	Delta        string `json:"delta,omitempty"`
	ItemID       string `json:"item_id,omitempty"`
	OutputIndex  int    `json:"output_index,omitempty"`
	ContentIndex int    `json:"content_index,omitempty"`
	Item         any    `json:"item,omitempty"`
	Response     any    `json:"response,omitempty"`
}

// Usage holds token counts from a response.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// --- Models list ---

// ModelsResponse is the response from GET /v1/models.
type ModelsResponse struct {
	Data []ModelData `json:"data"`
}

// ModelData describes a single model entry.
type ModelData struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// --- Embeddings ---

// EmbeddingRequest is the request body for POST /v1/embeddings.
type EmbeddingRequest struct {
	Model          string   `json:"model"`
	Input          []string `json:"input"`
	EncodingFormat string   `json:"encoding_format,omitempty"`
}

// EmbeddingResponse is the response from POST /v1/embeddings.
type EmbeddingResponse struct {
	Data  []EmbedData `json:"data"`
	Model string      `json:"model"`
	Usage Usage       `json:"usage"`
}

// EmbedData holds a single embedding vector with its index.
type EmbedData struct {
	Index     int       `json:"index"`
	Embedding []float64 `json:"embedding"`
}
