package runtimecore

import (
	"fmt"
	"sync"
)

type BuildFunc[T any] func() (T, error)

type materializedEntry[T any] struct {
	version string
	value   T
}

type Materializer[T any] struct {
	mu    sync.RWMutex
	cache map[string]materializedEntry[T]
}

func NewMaterializer[T any]() *Materializer[T] {
	return &Materializer[T]{
		cache: map[string]materializedEntry[T]{},
	}
}

func (m *Materializer[T]) Resolve(key string, version string, build BuildFunc[T]) (T, error) {
	var zero T
	if m == nil {
		return zero, fmt.Errorf("materializer is not configured")
	}
	if key == "" {
		return zero, fmt.Errorf("cache key is required")
	}
	if build == nil {
		return zero, fmt.Errorf("build function is required")
	}

	m.mu.RLock()
	cached, ok := m.cache[key]
	m.mu.RUnlock()
	if ok && cached.version == version {
		return cached.value, nil
	}

	value, err := build()
	if err != nil {
		return zero, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cache == nil {
		m.cache = map[string]materializedEntry[T]{}
	}
	m.cache[key] = materializedEntry[T]{
		version: version,
		value:   value,
	}
	return value, nil
}

func (m *Materializer[T]) Invalidate(key string) {
	if m == nil || key == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.cache, key)
}

func (m *Materializer[T]) Reset() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cache = map[string]materializedEntry[T]{}
}
