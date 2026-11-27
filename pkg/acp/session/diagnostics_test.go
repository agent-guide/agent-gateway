package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	acptransport "github.com/agent-guide/agent-gateway/pkg/acp/transport"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestPromptDiagnosticsCorrelateWithoutPayloads(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	ctx := WithDiagnosticLogger(context.Background(), zap.New(core).With(zap.String("run_id", "run-test"), zap.String("trace_id", "trace-test")))
	updates := make(chan acptransport.Message, 2)
	updates <- chunkMessage("agent_message_chunk", "private answer")
	updates <- acptransport.Message{Method: "session/update", Params: json.RawMessage(`{"update":{"sessionUpdate":"tool_call","status":"in_progress","rawInput":{"secret":"private tool argument"},"title":"private title"}}`)}
	inst := &instance{agent: stubAgent{}, sessionID: "session-test", t: &scriptedTransport{updates: updates, promptResult: json.RawMessage(`{"stopReason":"end_turn"}`)}}
	if _, err := inst.prompt(ctx, TurnRequest{Input: "private prompt"}, func(TurnEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	for _, message := range []string{"acp: request started", "acp: request completed", "acp: first update", "acp: tool update", "acp: result drain started", "acp: prompt finished"} {
		if logs.FilterMessage(message).Len() == 0 {
			t.Fatalf("missing diagnostic %q", message)
		}
	}
	for _, entry := range logs.All() {
		fields := entry.ContextMap()
		if fields["run_id"] != "run-test" || fields["trace_id"] != "trace-test" {
			t.Fatalf("missing correlation: %v", fields)
		}
		stamp, ok := fields["timestamp"].(string)
		if _, err := time.Parse(time.RFC3339Nano, stamp); !ok || err != nil || !strings.HasSuffix(stamp, "Z") {
			t.Fatalf("missing UTC timestamp: %v", fields)
		}
		raw, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "private") {
			t.Fatalf("payload leaked in diagnostic: %s", raw)
		}
	}
}

func TestRequestDiagnosticsRedactErrorsAndReportTimeout(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	ctx := WithDiagnosticLogger(context.Background(), zap.New(core))
	inst := &instance{t: &scriptedTransport{promptErr: errors.New("private upstream response")}}
	_, _ = inst.request(ctx, "session/prompt", "private input")
	entry := logs.FilterMessage("acp: request completed").All()[0]
	raw, _ := json.Marshal(entry.ContextMap())
	if strings.Contains(string(raw), "private") || entry.ContextMap()["success"] != false {
		t.Fatalf("incorrect failure record: %s", raw)
	}
	finish := diagnosticPhase(ctx, "timeout")
	finish(context.DeadlineExceeded)
	if logs.FilterMessage("acp: timeout completed").All()[0].ContextMap()["deadline_exceeded"] != true {
		t.Fatal("timeout not identified")
	}
}

func TestPermissionDiagnosticsUseActiveTurnLogger(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	ctx := WithDiagnosticLogger(context.Background(), zap.New(core).With(zap.String("run_id", "current-run")))
	inst := &instance{cfg: Config{}, turnContext: ctx, permissions: newPermissionBroker()}
	inst.setTurnSink(func(ev TurnEvent) error {
		return inst.permissions.resolve(ev.RequestID, acptransport.PermissionResponse{Outcome: acptransport.PermissionOutcomeSelected, SelectedOptionID: "private-option"})
	})
	response := inst.interactivePermission(context.Background(), json.RawMessage(`{"private":"permission arguments"}`))
	if response.Outcome != acptransport.PermissionOutcomeSelected {
		t.Fatalf("unexpected response: %+v", response)
	}
	for _, entry := range logs.All() {
		fields := entry.ContextMap()
		raw, _ := json.Marshal(fields)
		if fields["run_id"] != "current-run" || strings.Contains(string(raw), "private") {
			t.Fatalf("incorrect permission diagnostic: %s", raw)
		}
	}
	if logs.FilterMessage("acp: permission wait completed").Len() != 1 {
		t.Fatal("missing permission wait completion")
	}
}

func TestPermissionDiagnosticsFailures(t *testing.T) {
	for _, mode := range []string{"no sink", "no broker", "emit failure", "cancelled", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			core, logs := observer.New(zap.DebugLevel)
			diagnosticCtx := WithDiagnosticLogger(context.Background(), zap.New(core))
			inst := &instance{turnContext: diagnosticCtx, permissions: newPermissionBroker()}
			inst.setTurnSink(func(TurnEvent) error { return nil })
			params := json.RawMessage(`{"options":[{"optionId":"allow","kind":"allow_once"}]}`)
			ctx := context.Background()
			switch mode {
			case "no sink":
				inst.setTurnSink(nil)
			case "no broker":
				inst.permissions = nil
			case "emit failure":
				inst.setTurnSink(func(TurnEvent) error { return errors.New("private client error") })
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "deadline":
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
			}
			response := inst.interactivePermission(ctx, params)
			if response.Outcome != acptransport.PermissionOutcomeCancelled {
				t.Fatal(response)
			}
			fields := logs.FilterMessage("acp: permission wait completed").All()[0].ContextMap()
			if fields["success"] != false || fields["cancelled"] != (mode == "cancelled") || fields["deadline_exceeded"] != (mode == "deadline") {
				t.Fatal(fields)
			}
		})
	}
}

func TestSessionModelRequestDiagnostics(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	ctx := WithDiagnosticLogger(context.Background(), zap.New(core))
	inst := &instance{agent: stubAgent{}, model: "private-model", sessionID: "session", t: &scriptedTransport{}}
	if err := inst.applySessionModel(ctx, json.RawMessage(`{"configOptions":[{"id":"model","type":"select","category":"model"}]}`)); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"started", "completed"} {
		entries := logs.FilterMessage("acp: request " + phase).All()
		if len(entries) != 1 || entries[0].ContextMap()["method"] != "session/set_config_option" {
			t.Fatalf("entries=%v", entries)
		}
	}
}

func TestPromptCancellationDiagnostics(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(fmt.Sprint(deadline), func(t *testing.T) {
			core, logs := observer.New(zap.DebugLevel)
			ctx := WithDiagnosticLogger(context.Background(), zap.New(core))
			var cancel context.CancelFunc
			if deadline {
				ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
			} else {
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			defer cancel()
			inst := &instance{agent: stubAgent{}, t: &scriptedTransport{updates: make(chan acptransport.Message)}}
			stop, err := inst.prompt(ctx, TurnRequest{}, nil)
			if stop != "cancelled" || err != nil {
				t.Fatalf("stop=%s err=%v", stop, err)
			}
			fields := logs.FilterMessage("acp: prompt finished").All()[0].ContextMap()
			if fields["success"] != false || fields["cancelled"] != !deadline || fields["deadline_exceeded"] != deadline {
				t.Fatal(fields)
			}
		})
	}
}

func TestAgentReportedCancellationDiagnostics(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	ctx := WithDiagnosticLogger(context.Background(), zap.New(core))
	inst := &instance{agent: stubAgent{}, t: &scriptedTransport{updates: make(chan acptransport.Message), promptResult: json.RawMessage(`{"stopReason":"cancelled"}`)}}
	stop, err := inst.prompt(ctx, TurnRequest{}, nil)
	if stop != "cancelled" || err != nil || ctx.Err() != nil {
		t.Fatalf("stop=%s err=%v context=%v", stop, err, ctx.Err())
	}
	fields := logs.FilterMessage("acp: prompt finished").All()[0].ContextMap()
	if fields["success"] != false || fields["cancelled"] != true || fields["deadline_exceeded"] != false {
		t.Fatal(fields)
	}
}
