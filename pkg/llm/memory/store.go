package memory

import (
	"context"
	"time"
)

// MemoryStore defines the interface for agent memory.
type MemoryStore interface {
	Add(ctx context.Context, req *AddRequest) (*Memory, error)
	Get(ctx context.Context, id string) (*Memory, error)
	Update(ctx context.Context, id string, req *UpdateRequest) error
	Delete(ctx context.Context, id string) error
	Search(ctx context.Context, query string, opts *SearchOptions) ([]*Memory, error)
	SearchSimilar(ctx context.Context, embedding []float64, opts *SearchOptions) ([]*Memory, error)
	ListByAgent(ctx context.Context, agentID string, opts *ListOptions) ([]*Memory, error)
	ClearAgentMemory(ctx context.Context, agentID string) error
	BatchAdd(ctx context.Context, reqs []*AddRequest) ([]*Memory, error)
}

// Memory represents a single memory entry.
type Memory struct {
	ID        string
	AgentID   string
	Content   string
	Embedding []float64
	Metadata  map[string]any
	CreatedAt time.Time
	UpdatedAt time.Time
	Score     float64 // relevance score for search results
}

// AddRequest is the request to add a memory.
type AddRequest struct {
	AgentID  string
	Content  string
	Metadata map[string]any
	Embed    bool // whether to generate embedding
}

// UpdateRequest is the request to update a memory.
type UpdateRequest struct {
	Content  string
	Metadata map[string]any
}

// SearchOptions configures a memory search.
type SearchOptions struct {
	Limit     int
	Offset    int
	Threshold float64
	Filters   map[string]any
}

// ListOptions configures a memory list operation.
type ListOptions struct {
	Limit  int
	Offset int
}
