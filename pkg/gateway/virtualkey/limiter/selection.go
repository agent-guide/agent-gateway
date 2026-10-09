package limiter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/extension"
	"github.com/google/uuid"
)

const ModuleID = "online.agentguide.community.limiter"
const MemoryBackendType = "memory"

// Config is the Community-owned selection envelope (schema 1).
// No policy bypass is implicit: failure_mode defaults to closed.
type Config struct {
	BackendModuleID string `json:"backend_module_id"`
	BackendType     string `json:"backend_type"`
	TimeoutMS       int    `json:"timeout_ms"`
	FailureMode     string `json:"failure_mode"`
}

var backendTypePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

func decodeConfig(raw json.RawMessage) (Config, error) {
	c := Config{BackendModuleID: ModuleID, BackendType: MemoryBackendType, TimeoutMS: 100, FailureMode: "closed"}
	if _, err := extension.DecodeObject(raw); err != nil {
		return c, ErrLimiterUnavailable
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return c, ErrLimiterUnavailable
	}
	if d.Decode(new(any)) != io.EOF || !extension.ValidID(c.BackendModuleID) || !backendTypePattern.MatchString(c.BackendType) || c.TimeoutMS < 1 || c.TimeoutMS > 1000 || (c.FailureMode != "closed" && c.FailureMode != "open") || c.BackendModuleID == ModuleID && c.BackendType != MemoryBackendType {
		return c, ErrLimiterUnavailable
	}
	return c, nil
}

// Factory provides strict syntax/dependency validation and prepares the
// selected linked backend. runtime=false is the schema-only CLI projection.
func Factory(runtime bool) *extension.Factory {
	f := &extension.Factory{
		Validate: func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			c, e := decodeConfig(raw)
			if e != nil {
				return nil, e
			}
			return json.Marshal(c)
		},
		Dependencies: func(ctx context.Context, raw json.RawMessage) ([]string, error) {
			c, e := decodeConfig(raw)
			if e != nil {
				return nil, e
			}
			if c.BackendModuleID == ModuleID {
				return nil, nil
			}
			return []string{c.BackendModuleID}, nil
		},
	}
	if runtime {
		f.Prepare = func(ctx context.Context, cfg extension.PreparedConfig, deps extension.Snapshot) (extension.Resource, error) {
			c, err := decodeConfig(cfg.Envelope.Config)
			if err != nil {
				return nil, err
			}
			s := &Selection{config: c, identity: uuid.NewString()}
			// Timeout and failure handling do not reset tokens. Backend configuration and
			// type identify bucket authority; changes reset without attempting transfer.
			input := []byte(c.BackendModuleID + "/" + c.BackendType)
			if c.BackendModuleID != ModuleID {
				r, ok := deps.Resource(c.BackendModuleID)
				if !ok {
					return nil, ErrLimiterUnavailable
				}
				p, ok := r.(LimiterProvider)
				if !ok {
					return nil, ErrLimiterUnavailable
				}
				s.backend, err = p.LimiterBackend(c.BackendType)
				if err != nil || nilBackend(s.backend) {
					return nil, ErrLimiterUnavailable
				}
				e, _ := deps.Config(c.BackendModuleID)
				input = append(input, e.Config...)
				if err := s.backend.Health(ctx); err != nil {
					return nil, ErrLimiterUnavailable
				}
			}
			s.fingerprint = sha256.Sum256(input)
			return s, nil
		}
	}
	return f
}
func nilBackend(b LimiterBackend) bool {
	if b == nil {
		return true
	}
	v := reflect.ValueOf(b)
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return v.IsNil()
	}
	return false
}

type Selection struct {
	config      Config
	backend     LimiterBackend
	fingerprint [32]byte
	identity    string
}

func (s *Selection) Health(ctx context.Context) error {
	if s.backend != nil {
		return s.backend.Health(ctx)
	}
	return ctx.Err()
}
func (s *Selection) Close(context.Context) error { return nil } // Dependency owns its backend handle.
func (s *Selection) StateResetComparedTo(ctx context.Context, previous extension.Resource) (bool, error) {
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	old, ok := previous.(*Selection)
	if !ok || s.fingerprint != old.fingerprint {
		return true, nil
	}
	s.identity = old.identity // Only the unpublished candidate is changed.
	return false, nil
}
func (s *Selection) String() string {
	return fmt.Sprint(s.config.BackendModuleID, "/", s.config.BackendType)
}
func (s *Selection) budget() time.Duration {
	return time.Duration(s.config.TimeoutMS) * time.Millisecond
}

// Selection is Community runtime wiring, not an extension implementation API.
// It provides an immutable prepared backend selection to the VirtualKey owner.
func (s *Selection) Configuration() Config   { return s.config }
func (s *Selection) Backend() LimiterBackend { return s.backend }
func (s *Selection) Identity() string        { return s.identity }
func (s *Selection) Budget() time.Duration   { return s.budget() }
func DefaultSelection() *Selection {
	return &Selection{identity: "local", config: Config{BackendModuleID: ModuleID, BackendType: MemoryBackendType, TimeoutMS: 100, FailureMode: "closed"}}
}
