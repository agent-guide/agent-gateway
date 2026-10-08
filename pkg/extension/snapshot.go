package extension

import "context"

// Snapshot gives module preparation immutable access to already prepared
// dependencies. Resource values are module-owned concurrency-safe handles.
type Snapshot struct {
	ctx       context.Context
	configs   map[string]Envelope
	resources map[string]Resource
}

func (s Snapshot) Config(id string) (Envelope, bool) {
	e, ok := s.configs[id]
	return cloneEnvelope(e), ok
}
func (s Snapshot) Resource(id string) (Resource, bool) { r, ok := s.resources[id]; return r, ok }

// Context is the generation lifetime, canceled after retirement drains or times out.
func (s Snapshot) Context() context.Context {
	if s.ctx == nil {
		return context.Background()
	}
	return s.ctx
}
