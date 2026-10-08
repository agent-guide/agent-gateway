package extensionconsumer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/agent-guide/agent-gateway/pkg/extension"
	"io"
	"sync/atomic"
)

// The fixture owns schema and resources independently of Community runtime code.
type moduleConfig struct {
	Message string          `json:"message"`
	Token   json.RawMessage `json:"token,omitempty"`
}
type moduleResource struct {
	Message string
	secret  string
	closed  atomic.Bool
}

func (r *moduleResource) Health(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if r.closed.Load() {
		return errors.New("closed")
	}
	return nil
}
func (r *moduleResource) Close(ctx context.Context) error { r.closed.Store(true); return nil }
func fixtureFactory(runtime bool) *extension.Factory {
	f := &extension.Factory{SecretFields: []string{"/token"}, Validate: func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var cfg moduleConfig
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&cfg); err != nil {
			return nil, errors.New("invalid config")
		}
		var trailing any
		if dec.Decode(&trailing) != io.EOF || len(cfg.Message) > 128 || cfg.Message == "" {
			return nil, errors.New("invalid message")
		}
		return json.Marshal(cfg)
	}}
	if runtime {
		f.Prepare = func(ctx context.Context, c extension.PreparedConfig, _ extension.Snapshot) (extension.Resource, error) {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			var cfg moduleConfig
			if err := json.Unmarshal(c.Envelope.Config, &cfg); err != nil {
				return nil, err
			}
			return &moduleResource{Message: cfg.Message, secret: c.Secrets["/token"]}, nil
		}
	}
	return f
}
