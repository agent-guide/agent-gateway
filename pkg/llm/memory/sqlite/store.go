package sqlite

import (
	"context"
	"fmt"

	"github.com/agent-guide/agent-gateway/pkg/llm/memory"
)

// Store is the SQLite + sqlite-vec memory backend.
type Store struct {
	dsn string
	// db  *sql.DB  // TODO: add once sqlite-vec dependency is settled
}

// New creates a new SQLite memory store.
func New(dsn string) *Store {
	if dsn == "" {
		dsn = "llm_memory.db"
	}
	return &Store{dsn: dsn}
}

func (s *Store) Initialize(ctx context.Context, config *memory.BackendConfig) error {
	// TODO: open database, run migrations, enable sqlite-vec extension
	return fmt.Errorf("sqlite memory: Initialize not implemented")
}

func (s *Store) Close() error {
	return nil
}

func (s *Store) Health() error {
	return nil
}

func (s *Store) Add(ctx context.Context, req *memory.AddRequest) (*memory.Memory, error) {
	return nil, fmt.Errorf("sqlite memory: Add not implemented")
}

func (s *Store) Get(ctx context.Context, id string) (*memory.Memory, error) {
	return nil, fmt.Errorf("sqlite memory: Get not implemented")
}

func (s *Store) Update(ctx context.Context, id string, req *memory.UpdateRequest) error {
	return fmt.Errorf("sqlite memory: Update not implemented")
}

func (s *Store) Delete(ctx context.Context, id string) error {
	return fmt.Errorf("sqlite memory: Delete not implemented")
}

func (s *Store) Search(ctx context.Context, query string, opts *memory.SearchOptions) ([]*memory.Memory, error) {
	return nil, fmt.Errorf("sqlite memory: Search not implemented")
}

func (s *Store) SearchSimilar(ctx context.Context, embedding []float64, opts *memory.SearchOptions) ([]*memory.Memory, error) {
	return nil, fmt.Errorf("sqlite memory: SearchSimilar not implemented")
}

func (s *Store) ListByAgent(ctx context.Context, agentID string, opts *memory.ListOptions) ([]*memory.Memory, error) {
	return nil, fmt.Errorf("sqlite memory: ListByAgent not implemented")
}

func (s *Store) ClearAgentMemory(ctx context.Context, agentID string) error {
	return fmt.Errorf("sqlite memory: ClearAgentMemory not implemented")
}

func (s *Store) BatchAdd(ctx context.Context, reqs []*memory.AddRequest) ([]*memory.Memory, error) {
	return nil, fmt.Errorf("sqlite memory: BatchAdd not implemented")
}
