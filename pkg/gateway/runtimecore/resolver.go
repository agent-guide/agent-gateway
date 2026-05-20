package runtimecore

import (
	"context"
	"encoding/json"
	"fmt"
)

type ConfigDecoder[C any, T any] func(C) (T, error)

type ConfigFingerprinter[C any] func(C) (string, error)

type ConfigIDFunc[C any] func(C) string

type ConfigSource[C any, L any] interface {
	Get(ctx context.Context, id string) (C, error)
	List(ctx context.Context, opts L) ([]C, error)
}

type FuncSource[C any, L any] struct {
	GetFunc  func(ctx context.Context, id string) (C, error)
	ListFunc func(ctx context.Context, opts L) ([]C, error)
}

func (s FuncSource[C, L]) Get(ctx context.Context, id string) (C, error) {
	var zero C
	if s.GetFunc == nil {
		return zero, fmt.Errorf("config source get is not configured")
	}
	return s.GetFunc(ctx, id)
}

func (s FuncSource[C, L]) List(ctx context.Context, opts L) ([]C, error) {
	if s.ListFunc == nil {
		return nil, fmt.Errorf("config source list is not configured")
	}
	return s.ListFunc(ctx, opts)
}

type Resolver[C any, T any, L any] struct {
	source       ConfigSource[C, L]
	materializer *Materializer[T]
	decode       ConfigDecoder[C, T]
	fingerprint  ConfigFingerprinter[C]
	configID     ConfigIDFunc[C]
}

func NewResolver[C any, T any, L any](
	source ConfigSource[C, L],
	configID ConfigIDFunc[C],
	fingerprint ConfigFingerprinter[C],
	decode ConfigDecoder[C, T],
) *Resolver[C, T, L] {
	return &Resolver[C, T, L]{
		source:       source,
		materializer: NewMaterializer[T](),
		decode:       decode,
		fingerprint:  fingerprint,
		configID:     configID,
	}
}

func (r *Resolver[C, T, L]) Source() ConfigSource[C, L] {
	if r == nil {
		return nil
	}
	return r.source
}

func (r *Resolver[C, T, L]) Get(ctx context.Context, id string) (T, error) {
	var zero T
	if r == nil || r.source == nil {
		return zero, fmt.Errorf("config source is not configured")
	}
	cfg, err := r.source.Get(ctx, id)
	if err != nil {
		return zero, err
	}
	return r.ResolveConfig(cfg)
}

func (r *Resolver[C, T, L]) List(ctx context.Context, opts L) ([]T, error) {
	if r == nil || r.source == nil {
		return nil, fmt.Errorf("config source is not configured")
	}
	configs, err := r.source.List(ctx, opts)
	if err != nil {
		return nil, err
	}

	out := make([]T, 0, len(configs))
	for _, cfg := range configs {
		item, err := r.ResolveConfig(cfg)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func (r *Resolver[C, T, L]) Invalidate(id string) {
	if r == nil {
		return
	}
	r.materializer.Invalidate(id)
}

func (r *Resolver[C, T, L]) ResolveConfig(cfg C) (T, error) {
	var zero T
	if r == nil || r.decode == nil || r.materializer == nil || r.fingerprint == nil || r.configID == nil {
		return zero, fmt.Errorf("resolver is not configured")
	}

	id := r.configID(cfg)
	if id == "" {
		return zero, fmt.Errorf("config id is required")
	}

	version, err := r.fingerprint(cfg)
	if err != nil {
		return zero, err
	}

	return r.materializer.Resolve(id, version, func() (T, error) {
		return r.decode(cfg)
	})
}

func FingerprintJSON[T any](id string, kind string, cfg T) (string, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		if kind == "" {
			kind = "config"
		}
		return "", fmt.Errorf("fingerprint %s %q: %w", kind, id, err)
	}
	return string(data), nil
}
