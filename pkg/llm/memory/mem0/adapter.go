package mem0

import (
	"context"
	"fmt"

	"github.com/agent-guide/agent-gateway/pkg/llm/memory"
)

// Adapter adapts the Mem0 API to the MemoryStore interface.
type Adapter struct {
	apiKey    string
	projectID string
	baseURL   string
}

// New creates a new Mem0 adapter.
func New(apiKey, projectID string) *Adapter {
	return &Adapter{
		apiKey:    apiKey,
		projectID: projectID,
		baseURL:   "https://api.mem0.ai/v1",
	}
}

func (a *Adapter) Initialize(ctx context.Context, config *memory.BackendConfig) error {
	return nil
}

func (a *Adapter) Close() error {
	return nil
}

func (a *Adapter) Health() error {
	return nil
}

func (a *Adapter) Add(ctx context.Context, req *memory.AddRequest) (*memory.Memory, error) {
	// TODO: POST to /memories
	return nil, fmt.Errorf("mem0: Add not implemented")
}

func (a *Adapter) Get(ctx context.Context, id string) (*memory.Memory, error) {
	return nil, fmt.Errorf("mem0: Get not implemented")
}

func (a *Adapter) Update(ctx context.Context, id string, req *memory.UpdateRequest) error {
	return fmt.Errorf("mem0: Update not implemented")
}

func (a *Adapter) Delete(ctx context.Context, id string) error {
	return fmt.Errorf("mem0: Delete not implemented")
}

func (a *Adapter) Search(ctx context.Context, query string, opts *memory.SearchOptions) ([]*memory.Memory, error) {
	return nil, fmt.Errorf("mem0: Search not implemented")
}

func (a *Adapter) SearchSimilar(ctx context.Context, embedding []float64, opts *memory.SearchOptions) ([]*memory.Memory, error) {
	return nil, fmt.Errorf("mem0: SearchSimilar not implemented")
}

func (a *Adapter) ListByAgent(ctx context.Context, agentID string, opts *memory.ListOptions) ([]*memory.Memory, error) {
	return nil, fmt.Errorf("mem0: ListByAgent not implemented")
}

func (a *Adapter) ClearAgentMemory(ctx context.Context, agentID string) error {
	return fmt.Errorf("mem0: ClearAgentMemory not implemented")
}

func (a *Adapter) BatchAdd(ctx context.Context, reqs []*memory.AddRequest) ([]*memory.Memory, error) {
	return nil, fmt.Errorf("mem0: BatchAdd not implemented")
}
