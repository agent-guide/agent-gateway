package memory

import "context"

// Backend is a MemoryStore with lifecycle management.
type Backend interface {
	MemoryStore
	Initialize(ctx context.Context, config *BackendConfig) error
	Close() error
	Health() error
}

// BackendConfig configures a memory backend.
type BackendConfig struct {
	Type    string         `json:"type"` // sqlite, postgres, mem0, chroma
	DSN     string         `json:"dsn,omitempty"`
	Options map[string]any `json:"options,omitempty"`
}

// VectorStore handles vector operations for similarity search.
type VectorStore interface {
	Insert(ctx context.Context, id string, embedding []float64, metadata map[string]any) error
	Search(ctx context.Context, embedding []float64, k int) ([]*SearchResult, error)
	Delete(ctx context.Context, id string) error
}

// SearchResult is a result from a vector similarity search.
type SearchResult struct {
	ID       string
	Score    float64
	Metadata map[string]any
}
