package provider

import "context"

// EmbeddingProvider is an optional interface for providers that support embeddings.
// The memory module uses this to generate vectors for storage and search.
type EmbeddingProvider interface {
	Provider
	Embedding(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error)
}

// EmbeddingRequest is the request to generate vector embeddings.
type EmbeddingRequest struct {
	// Model is the embedding model to use. Leave empty to use provider default.
	Model string
	// Texts are the strings to embed.
	Texts []string
}

// EmbeddingResponse contains the generated embeddings.
type EmbeddingResponse struct {
	Embeddings [][]float64
	Model      string
	Usage      Usage
}
