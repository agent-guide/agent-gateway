package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	agentpkg "github.com/agent-guide/agent-gateway/pkg/agent"
	agentruntime "github.com/agent-guide/agent-gateway/pkg/agent/runtime"
	"github.com/google/uuid"
)

const (
	httpBindingTTL = 24 * time.Hour
	httpBindingCap = 1024
)

type httpSessionBinding struct {
	contextID  string
	taskID     a2a.TaskID
	claimed    bool
	expiresAt  time.Time
	lastUsedAt time.Time
}

type httpSessionBindings struct {
	mu      sync.Mutex
	entries map[string]httpSessionBinding
	now     func() time.Time
}

func newHTTPSessionBindings() *httpSessionBindings {
	return &httpSessionBindings{entries: map[string]httpSessionBinding{}, now: time.Now}
}

type httpBindingClaim struct {
	sessionID   string
	original    httpSessionBinding
	hadOriginal bool
	resumed     bool
	resetReason string
}

func (b *httpSessionBindings) claim(sessionID string, supplied bool) (httpBindingClaim, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now().UTC()
	claim := httpBindingClaim{sessionID: sessionID}
	existing, ok := b.entries[sessionID]
	if ok && !existing.expiresAt.IsZero() && !now.Before(existing.expiresAt) {
		delete(b.entries, sessionID)
		ok = false
		claim.resetReason = "binding_expired"
	}
	if ok {
		if existing.claimed {
			return claim, agentruntime.NewError(agentruntime.ErrorSessionBusy, "HTTP Agent session is busy")
		}
		claim.original, claim.hadOriginal, claim.resumed = existing, true, true
		existing.claimed = true
		b.entries[sessionID] = existing
		return claim, nil
	}
	if supplied && claim.resetReason == "" {
		claim.resetReason = "binding_missing"
	}
	b.entries[sessionID] = httpSessionBinding{claimed: true, lastUsedAt: now}
	return claim, nil
}

func (b *httpSessionBindings) finish(claim httpBindingClaim, contextID string, taskID a2a.TaskID) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now().UTC()
	b.entries[claim.sessionID] = httpSessionBinding{
		contextID: contextID, taskID: taskID, expiresAt: now.Add(httpBindingTTL), lastUsedAt: now,
	}
	b.evictLocked()
}

func (b *httpSessionBindings) drop(claim httpBindingClaim) {
	b.mu.Lock()
	delete(b.entries, claim.sessionID)
	b.mu.Unlock()
}

func (b *httpSessionBindings) evictLocked() {
	for len(b.entries) > httpBindingCap {
		var oldestID string
		var oldest time.Time
		for id, entry := range b.entries {
			if entry.claimed {
				continue
			}
			if oldestID == "" || entry.lastUsedAt.Before(oldest) {
				oldestID, oldest = id, entry.lastUsedAt
			}
		}
		if oldestID == "" {
			return
		}
		delete(b.entries, oldestID)
	}
}

// HTTPBackend translates the common Agent turn contract to A2A 1.0 JSON-RPC.
type HTTPBackend struct {
	manager *HTTPRuntimeManager
	runs    *agentruntime.RunRegistry
}

func NewHTTPBackend(manager *HTTPRuntimeManager, controls ...RuntimeControls) *HTTPBackend {
	backend := &HTTPBackend{manager: manager}
	if len(controls) > 0 {
		backend.runs = controls[0].Runs
	}
	return backend
}

func (*HTTPBackend) RuntimeType() string { return agentpkg.RuntimeTypeHTTP }

func (b *HTTPBackend) Capabilities(_ context.Context, agent agentpkg.Agent) (agentruntime.Capabilities, error) {
	if err := validateBackendAgent(agent, agentpkg.RuntimeTypeHTTP); err != nil {
		return agentruntime.Capabilities{}, err
	}
	execution, err := b.resolve(agent.ID)
	if err != nil {
		return agentruntime.Capabilities{}, err
	}
	return agentruntime.Capabilities{
		Executable:   true,
		Turn:         agentruntime.TurnCapabilities{Streaming: execution.Streaming},
		Sessions:     agentruntime.SessionCapabilities{Resume: true},
		Cancellation: agentruntime.CancelCapabilities{Force: true},
		Events: []string{
			agentruntime.EventSession, agentruntime.EventContent, agentruntime.EventDelta,
			agentruntime.EventDone, agentruntime.EventError,
		},
	}, nil
}

func (b *HTTPBackend) ServeTurn(ctx context.Context, agent agentpkg.Agent, req agentruntime.TurnRequest, emit agentruntime.EventSink) error {
	if err := validateBackendAgent(agent, agentpkg.RuntimeTypeHTTP); err != nil {
		return err
	}
	if err := validateRuntimeOptionsVersion(req.Options); err != nil {
		return err
	}
	var noOptions struct{}
	if err := agentruntime.DecodeRuntimeOptions(req.Options.Runtime, &noOptions); err != nil {
		return err
	}
	if req.Permission != nil {
		return agentruntime.NewError(agentruntime.ErrorCapabilityNotSupported, "HTTP Agent permissions are not supported")
	}
	if strings.TrimSpace(req.Input) == "" {
		return agentruntime.NewError(agentruntime.ErrorInvalidRequest, "input is required")
	}
	execution, err := b.resolve(agent.ID)
	if err != nil {
		return err
	}
	suppliedSession := strings.TrimSpace(req.SessionID) != ""
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		sessionID = uuid.NewString()
	}
	claim, err := execution.bindings.claim(sessionID, suppliedSession)
	if err != nil {
		return err
	}
	finished := false
	defer func() {
		if !finished {
			execution.bindings.drop(claim)
		}
	}()
	sessionData, _ := json.Marshal(map[string]any{
		"resumed": claim.resumed, "reset_reason": claim.resetReason,
	})
	if err := emit(agentruntime.TurnEvent{Event: agentruntime.EventSession, SessionID: sessionID, Data: sessionData}); err != nil {
		return err
	}

	message := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(req.Input))
	if claim.hadOriginal {
		message.ContextID = claim.original.contextID
		message.TaskID = claim.original.taskID
	}
	sendRequest := &a2a.SendMessageRequest{Message: message}
	turnCtx, cancel := context.WithTimeout(ctx, execution.Timeout)
	defer cancel()

	var terminal terminalBinding
	if execution.Streaming {
		terminal, err = b.serveStreaming(turnCtx, execution, sendRequest, emit)
	} else {
		var result a2a.SendMessageResult
		result, err = execution.Client.SendMessage(turnCtx, sendRequest)
		if err == nil {
			terminal, err = b.serveResult(result, emit)
		}
	}
	if err != nil {
		if terminal.contextID != "" {
			execution.bindings.finish(claim, terminal.contextID, "")
			finished = true
		}
		return mapHTTPError(err)
	}
	if terminal.direct {
		if claim.hadOriginal {
			execution.bindings.finish(claim, claim.original.contextID, "")
		} else {
			execution.bindings.drop(claim)
		}
	} else {
		execution.bindings.finish(claim, terminal.contextID, terminal.interruptedTaskID)
	}
	finished = true
	return nil
}

type terminalBinding struct {
	contextID         string
	interruptedTaskID a2a.TaskID
	direct            bool
}

func (b *HTTPBackend) serveResult(result a2a.SendMessageResult, emit agentruntime.EventSink) (terminalBinding, error) {
	switch event := result.(type) {
	case *a2a.Message:
		if err := emitA2AMessage(event, emit); err != nil {
			return terminalBinding{}, err
		}
		if err := emitHTTPDone(emit, "stop", nil); err != nil {
			return terminalBinding{}, err
		}
		return terminalBinding{direct: true}, nil
	case *a2a.Task:
		binding, terminal, err := emitA2ATask(event, emit)
		if err != nil {
			return binding, err
		}
		if !terminal {
			return terminalBinding{}, agentruntime.NewError(agentruntime.ErrorTurnFailed, "synchronous A2A turn returned before terminal state")
		}
		return binding, nil
	default:
		return terminalBinding{}, agentruntime.NewError(agentruntime.ErrorTurnFailed, "A2A response has an unsupported result")
	}
}

func (b *HTTPBackend) serveStreaming(ctx context.Context, execution *HTTPExecution, request *a2a.SendMessageRequest, emit agentruntime.EventSink) (terminalBinding, error) {
	first := true
	direct := false
	var taskID a2a.TaskID
	var contextID string
	var result terminalBinding
	terminal := false
	for event, err := range execution.Client.SendStreamingMessage(ctx, request) {
		if err != nil {
			return terminalBinding{}, err
		}
		if first {
			first = false
			switch initial := event.(type) {
			case *a2a.Message:
				direct = true
				if err := emitA2AMessage(initial, emit); err != nil {
					return terminalBinding{}, err
				}
				continue
			case *a2a.Task:
				taskID, contextID = initial.ID, initial.ContextID
				var statusTerminal bool
				result, statusTerminal, err = emitA2ATask(initial, emit)
				if err != nil {
					return result, err
				}
				terminal = statusTerminal
				continue
			default:
				return terminalBinding{}, agentruntime.NewError(agentruntime.ErrorTurnFailed, "A2A stream must begin with task or message")
			}
		}
		if direct || terminal {
			return terminalBinding{}, agentruntime.NewError(agentruntime.ErrorTurnFailed, "A2A stream continued after a terminal event")
		}
		switch update := event.(type) {
		case *a2a.TaskStatusUpdateEvent:
			if update.TaskID != taskID || update.ContextID != contextID {
				return terminalBinding{}, agentruntime.NewError(agentruntime.ErrorTurnFailed, "A2A task identity changed during stream")
			}
			result, terminal, err = emitA2AStatus(update.Status, taskID, contextID, emit)
			if err != nil {
				return result, err
			}
		case *a2a.TaskArtifactUpdateEvent:
			if update.TaskID != taskID || update.ContextID != contextID || update.Artifact == nil {
				return terminalBinding{}, agentruntime.NewError(agentruntime.ErrorTurnFailed, "invalid A2A artifact update")
			}
			if err := emitA2AArtifact(update.Artifact, emit); err != nil {
				return terminalBinding{}, err
			}
		default:
			return terminalBinding{}, agentruntime.NewError(agentruntime.ErrorTurnFailed, "unsupported A2A stream event")
		}
	}
	if first {
		return terminalBinding{}, agentruntime.NewError(agentruntime.ErrorTurnFailed, "A2A stream closed without an event")
	}
	if direct {
		if err := emitHTTPDone(emit, "stop", nil); err != nil {
			return terminalBinding{}, err
		}
		return terminalBinding{direct: true}, nil
	}
	if !terminal {
		return terminalBinding{}, agentruntime.NewError(agentruntime.ErrorTurnFailed, "A2A task stream closed before terminal state")
	}
	return result, nil
}

func emitA2ATask(task *a2a.Task, emit agentruntime.EventSink) (terminalBinding, bool, error) {
	if task == nil || strings.TrimSpace(string(task.ID)) == "" || strings.TrimSpace(task.ContextID) == "" {
		return terminalBinding{}, false, agentruntime.NewError(agentruntime.ErrorTurnFailed, "A2A task is missing id or contextId")
	}
	if task.Status.Message != nil {
		if err := emitA2AMessage(task.Status.Message, emit); err != nil {
			return terminalBinding{}, false, err
		}
	}
	for _, artifact := range task.Artifacts {
		if artifact == nil {
			return terminalBinding{}, false, agentruntime.NewError(agentruntime.ErrorTurnFailed, "A2A task contains a nil artifact")
		}
		if err := emitA2AArtifact(artifact, emit); err != nil {
			return terminalBinding{}, false, err
		}
	}
	return emitA2AStatus(a2a.TaskStatus{State: task.Status.State}, task.ID, task.ContextID, emit)
}

func emitA2AStatus(status a2a.TaskStatus, taskID a2a.TaskID, contextID string, emit agentruntime.EventSink) (terminalBinding, bool, error) {
	binding := terminalBinding{contextID: contextID}
	if status.Message != nil {
		if err := emitA2AMessage(status.Message, emit); err != nil {
			return terminalBinding{}, false, err
		}
	}
	switch status.State {
	case a2a.TaskStateSubmitted, a2a.TaskStateWorking:
		return binding, false, nil
	case a2a.TaskStateCompleted:
		return binding, true, emitHTTPDone(emit, "stop", nil)
	case a2a.TaskStateCanceled:
		return binding, true, emitHTTPDone(emit, agentruntime.StopReasonCancelled, nil)
	case a2a.TaskStateInputRequired:
		binding.interruptedTaskID = taskID
		return binding, true, emitHTTPDone(emit, "input_required", map[string]any{"task_id": taskID, "context_id": contextID})
	case a2a.TaskStateAuthRequired:
		return binding, true, agentruntime.NewError(agentruntime.ErrorTurnFailed, "A2A task requires unsupported authentication interaction")
	case a2a.TaskStateFailed:
		return binding, true, agentruntime.NewError(agentruntime.ErrorTurnFailed, "A2A task failed")
	case a2a.TaskStateRejected:
		return binding, true, agentruntime.NewError(agentruntime.ErrorTurnFailed, "A2A task was rejected")
	default:
		return terminalBinding{}, false, agentruntime.NewError(agentruntime.ErrorTurnFailed, "A2A task returned an unknown state")
	}
}

func emitA2AMessage(message *a2a.Message, emit agentruntime.EventSink) error {
	if message == nil || message.Role != a2a.MessageRoleAgent {
		return agentruntime.NewError(agentruntime.ErrorTurnFailed, "A2A response message must be agent-authored")
	}
	var text strings.Builder
	for _, part := range message.Parts {
		if part == nil {
			return agentruntime.NewError(agentruntime.ErrorTurnFailed, "A2A message contains a nil part")
		}
		if value, ok := part.Content.(a2a.Text); ok {
			text.WriteString(string(value))
		}
	}
	data, err := json.Marshal(map[string]any{"parts": message.Parts})
	if err != nil {
		return agentruntime.WrapError(agentruntime.ErrorTurnFailed, "encode A2A message parts", err)
	}
	return emit(agentruntime.TurnEvent{Event: agentruntime.EventContent, Text: text.String(), Data: data})
}

func emitA2AArtifact(artifact *a2a.Artifact, emit agentruntime.EventSink) error {
	var text strings.Builder
	for _, part := range artifact.Parts {
		if part == nil {
			return agentruntime.NewError(agentruntime.ErrorTurnFailed, "A2A artifact contains a nil part")
		}
		if value, ok := part.Content.(a2a.Text); ok {
			text.WriteString(string(value))
		}
	}
	data, err := json.Marshal(map[string]any{"artifact": artifact})
	if err != nil {
		return agentruntime.WrapError(agentruntime.ErrorTurnFailed, "encode A2A artifact", err)
	}
	return emit(agentruntime.TurnEvent{Event: agentruntime.EventContent, Text: text.String(), Data: data})
}

func emitHTTPDone(emit agentruntime.EventSink, stopReason string, extra map[string]any) error {
	payload := map[string]any{"stop_reason": stopReason}
	for key, value := range extra {
		payload[key] = value
	}
	data, _ := json.Marshal(payload)
	return emit(agentruntime.TurnEvent{Event: agentruntime.EventDone, Data: data})
}

func (b *HTTPBackend) resolve(agentID string) (*HTTPExecution, error) {
	if b == nil || b.manager == nil {
		return nil, agentruntime.NewError(agentruntime.ErrorRuntimeNotExecutable, "HTTP runtime is not executable")
	}
	execution, err := b.manager.ResolveExecution(agentID)
	if err != nil {
		return nil, agentruntime.WrapError(agentruntime.ErrorBackendUnavailable, "HTTP runtime is not ready", err)
	}
	return execution, nil
}

func mapHTTPError(err error) error {
	if err == nil || agentruntime.IsNormalized(err) {
		return err
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return agentruntime.WrapError(agentruntime.ErrorBackendTimeout, "HTTP Agent turn timed out", err)
	case errors.Is(err, context.Canceled):
		return agentruntime.WrapError(agentruntime.ErrorTurnCancelled, "HTTP Agent turn cancelled", err)
	default:
		return agentruntime.WrapError(agentruntime.ErrorTurnFailed, "HTTP Agent turn failed", err)
	}
}

var _ agentruntime.Backend = (*HTTPBackend)(nil)

func validateHTTPTaskIdentity(taskID a2a.TaskID, contextID string) error {
	if strings.TrimSpace(string(taskID)) == "" || strings.TrimSpace(contextID) == "" {
		return fmt.Errorf("missing task identity")
	}
	return nil
}
