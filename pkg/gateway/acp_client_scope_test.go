package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/agent-guide/agent-gateway/internal/observability/usage"
	acphost "github.com/agent-guide/agent-gateway/pkg/acp/host"
	"github.com/agent-guide/agent-gateway/pkg/acp/hostconfig"
	agentpkg "github.com/agent-guide/agent-gateway/pkg/agent"
	agentruntime "github.com/agent-guide/agent-gateway/pkg/agent/runtime"
)

type scopedACPRuntime struct {
	requests  []acphost.TurnRequest
	decisions int
}

func (r *scopedACPRuntime) ServeConfiguredTurn(_ context.Context, _ string, _ hostconfig.Config, req acphost.TurnRequest, emit acphost.EventSink) error {
	r.requests = append(r.requests, req)
	id := "session-" + req.ThreadID
	if err := emit(acphost.TurnEvent{Event: agentruntime.EventSession, SessionID: id}); err != nil {
		return err
	}
	return emit(acphost.TurnEvent{Event: agentruntime.EventPermission, SessionID: id, RequestID: "pending-" + req.RunID,
		Data: json.RawMessage(`{"options":[{"optionId":"allow-once","kind":"allow_once"}]}`)})
}
func (r *scopedACPRuntime) ResolvePermission(acphost.PermissionDecision) error {
	r.decisions++
	return nil
}

func TestACPClientScopesGuardNativeSessionsAndPermissions(t *testing.T) {
	native := &scopedACPRuntime{}
	broker := agentruntime.NewPermissionBroker()
	t.Cleanup(func() { broker.Close(t.Context()) })
	backend := NewACPBackend(native, RuntimeControls{Permissions: broker})
	a := agentpkg.Agent{ID: "agent", Runtime: agentpkg.Runtime{Type: agentpkg.RuntimeTypeACP, ACP: inlineACPRuntime(hostconfig.Config{AgentType: "opencode", CWD: "/workspace", PermissionMode: "interactive"})}}
	backend.RefreshRuntimeConfigs(t.Context(), []agentpkg.Agent{a})
	client := func(key, route string) context.Context {
		return usage.ContextWithDimensions(t.Context(), usage.InteractionDimensions{VirtualKeyID: key, RouteID: route})
	}
	one, two := client("key-one", "route"), client("key-two", "route")
	sessions := []string{}
	turn := func(ctx context.Context, session string, number int) error {
		return backend.ServeTurn(ctx, a, agentruntime.TurnRequest{RunID: fmt.Sprintf("run-%032d", number), Input: "test", SessionID: session,
			Options: agentruntime.TurnOptions{Runtime: json.RawMessage(`{"thread_id":"same-thread"}`)}}, func(ev agentruntime.TurnEvent) error {
			if ev.Event == agentruntime.EventSession {
				sessions = append(sessions, ev.SessionID)
			}
			if ev.Event == agentruntime.EventPermission {
				other := two
				if ctx == two {
					other = one
				}
				decision := agentruntime.PermissionDecision{RequestID: ev.RequestID, Outcome: "selected", OptionID: "allow-once"}
				if err := backend.ResolvePermission(other, a, decision); !errors.Is(err, agentruntime.ErrPermissionNotFound) {
					t.Fatalf("foreign permission: %v", err)
				}
				if err := backend.ResolvePermission(ctx, a, decision); err != nil {
					t.Fatalf("own permission: %v", err)
				}
			}
			return nil
		})
	}
	if err := turn(one, "", 1); err != nil {
		t.Fatal(err)
	}
	if err := turn(two, "", 2); err != nil {
		t.Fatal(err)
	}
	if sessions[0] == sessions[1] || native.requests[0].ThreadID == "same-thread" {
		t.Fatal("client threads were not isolated")
	}
	if err := turn(two, sessions[0], 3); !errors.Is(err, agentruntime.ErrSessionNotFound) {
		t.Fatalf("foreign resume: %v", err)
	}
	if len(native.requests) != 2 {
		t.Fatal("foreign resume reached native runtime")
	}
	if err := turn(one, sessions[0], 4); err != nil {
		t.Fatal(err)
	}
	if sessions[2] != sessions[0] {
		t.Fatal("same-client thread continuity lost")
	}
	listed, err := backend.ListSessions(one, a, agentruntime.ListSessionsRequest{})
	if err != nil || len(listed.Sessions) != 1 || listed.Sessions[0].SessionID != sessions[0] {
		t.Fatalf("list leaked: %+v %v", listed, err)
	}
	if _, err := backend.LoadTranscript(two, a, agentruntime.TranscriptRequest{SessionID: sessions[0]}); !errors.Is(err, agentruntime.ErrSessionNotFound) {
		t.Fatalf("foreign transcript: %v", err)
	}
	if err := turn(client("key-one", "other-route"), sessions[0], 5); !errors.Is(err, agentruntime.ErrSessionNotFound) {
		t.Fatalf("foreign route: %v", err)
	}
	if native.decisions != 3 {
		t.Fatalf("decisions = %d", native.decisions)
	}
	a.Runtime.ACP.CWD = "/other"
	if err := turn(one, sessions[0], 6); !errors.Is(err, agentruntime.ErrSessionNotFound) {
		t.Fatalf("changed runtime accepted old session: %v", err)
	}
}

func (r *scopedACPRuntime) ListConfiguredSessions(_ context.Context, _ string, _ hostconfig.Config, req acphost.ListSessionsRequest) (acphost.ListSessionsResponse, error) {
	seen := map[string]bool{}
	out := acphost.ListSessionsResponse{NextCursor: req.Cursor}
	for _, request := range r.requests {
		id := "session-" + request.ThreadID
		if !seen[id] {
			seen[id] = true
			out.Sessions = append(out.Sessions, acphost.SessionInfo{SessionID: id, Title: "retained metadata"})
		}
	}
	return out, nil
}
func TestACPScopedSessionListingRetainsPaginationAndMetadata(t *testing.T) {
	native := &scopedACPRuntime{}
	backend := NewACPBackend(native)
	a := agentpkg.Agent{ID: "agent", Runtime: agentpkg.Runtime{Type: "acp", ACP: inlineACPRuntime(hostconfig.Config{AgentType: "opencode", CWD: "/workspace"})}}
	backend.RefreshRuntimeConfigs(t.Context(), []agentpkg.Agent{a})
	ctx := usage.ContextWithDimensions(t.Context(), usage.InteractionDimensions{RouteID: "route", VirtualKeyID: "key"})
	if err := backend.ServeTurn(ctx, a, agentruntime.TurnRequest{Input: "hello", Options: agentruntime.TurnOptions{Runtime: json.RawMessage(`{"thread_id":"thread"}`)}}, func(agentruntime.TurnEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	result, err := backend.ListSessions(ctx, a, agentruntime.ListSessionsRequest{CWD: "/workspace", Cursor: "native-page"})
	if err != nil || result.NextCursor != "native-page" || len(result.Sessions) != 1 || result.Sessions[0].Title != "retained metadata" {
		t.Fatalf("lost native list contract: %+v %v", result, err)
	}
}
func TestACPSessionOwnershipEvictionDoesNotGrantForeignAccess(t *testing.T) {
	scopes := acpClientScopes{}
	for i := 0; i <= httpBindingCap; i++ {
		if err := scopes.rememberSession("agent", fmt.Sprint(i), "owner"); err != nil {
			t.Fatal(err)
		}
	}
	if scopes.ownsSession("agent", "0", "owner") || scopes.ownsSession("agent", "0", "foreign") {
		t.Fatal("evicted ownership became authority")
	}
	if !scopes.ownsSession("agent", fmt.Sprint(httpBindingCap), "owner") {
		t.Fatal("lost current owner")
	}
	if len(scopes.sessions) > httpBindingCap {
		t.Fatal("unbounded ownership")
	}
}
