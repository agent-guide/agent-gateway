package extensionconsumer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/agent-guide/agent-gateway/pkg/event"
	"github.com/agent-guide/agent-gateway/pkg/extension"
	"github.com/google/uuid"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
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
	resets  atomic.Int64
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
		// Each prepared module owns one immutable declaration set and its own handles.
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

func (r *moduleResource) ServeAdminHTTP(w http.ResponseWriter, req *http.Request, d extension.AdminDependencies) {
	if req.Context().Err() != nil {
		extension.WriteAdminError(w, 503, "closed")
		return
	}
	switch req.Method {
	case "GET":
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"message": r.Message, "resets": r.resets.Load()})
	case "POST":
		var body struct{}
		if json.NewDecoder(req.Body).Decode(&body) != nil {
			extension.WriteAdminError(w, 400, "invalid_config")
			return
		}
		if strings.HasSuffix(req.URL.Path, "/probe") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"message": r.Message})
			return
		}
		r.resets.Add(1)
		d.Events.Publish(req.Context(), event.Envelope{SchemaVersion: 1, ID: uuid.NewString(), Family: "extension.mutation", Timestamp: time.Now().UTC(), Outcome: "success", Mutation: &event.ExtensionMutation{ModuleID: d.Config.ModuleID, Operation: "reset"}})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "reset"})
	default:
		extension.WriteAdminError(w, 405, "method_not_allowed")
	}
}
