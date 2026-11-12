package builtin

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/agent-guide/agent-gateway/internal/observability/usage"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type usageStreamModel struct {
	model.ToolCallingChatModel
	chunks []*schema.Message
}

func (m usageStreamModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return schema.StreamReaderFromArray(m.chunks), nil
}

func TestObservedModelStreamMergesUsageSnapshots(t *testing.T) {
	for _, tc := range []struct {
		name                                    string
		usages                                  []*schema.TokenUsage
		input, output, total, cached, reasoning int
	}{
		{"split", []*schema.TokenUsage{
			{PromptTokens: 42, CompletionTokens: 1, TotalTokens: 43, PromptTokenDetails: schema.PromptTokenDetails{CachedTokens: 12}},
			{CompletionTokens: 7, TotalTokens: 7, CompletionTokensDetails: schema.CompletionTokensDetails{ReasoningTokens: 3}},
		}, 42, 7, 49, 12, 3},
		{"cumulative", []*schema.TokenUsage{{PromptTokens: 42, CompletionTokens: 1, TotalTokens: 43}, {PromptTokens: 42, CompletionTokens: 7, TotalTokens: 49}, {}}, 42, 7, 49, 0, 0},
		{"total_only", []*schema.TokenUsage{{TotalTokens: 49}}, 0, 0, 49, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := &usage.InMemorySink{}
			var chunks []*schema.Message
			for _, u := range tc.usages {
				chunks = append(chunks, &schema.Message{Role: schema.Assistant, ResponseMeta: &schema.ResponseMeta{Usage: u}})
			}
			m := newObservedModel(usageStreamModel{chunks: chunks}, usage.NewObserver(sink), "route", "agent")
			sr, err := m.Stream(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer sr.Close()
			for {
				_, err := sr.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if len(sink.Events) != 1 {
				t.Fatalf("events = %v", sink.Events)
			}
			ev := sink.Events[0].(usage.LLMUsageEvent)
			if ev.InputTokens != tc.input || ev.OutputTokens != tc.output || ev.TotalTokens != tc.total || ev.CachedTokens != tc.cached || ev.ReasoningTokens != tc.reasoning || !ev.UsageFinalized {
				t.Fatalf("usage = %+v", ev)
			}
		})
	}
}

func TestBuiltinUsageEventReconcilesSplitTotal(t *testing.T) {
	chunks := []*schema.Message{
		{Role: schema.Assistant, ResponseMeta: &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 42, CompletionTokens: 1, TotalTokens: 43}}},
		{Role: schema.Assistant, ResponseMeta: &schema.ResponseMeta{Usage: &schema.TokenUsage{CompletionTokens: 7, TotalTokens: 7}}},
	}
	msg, err := schema.ConcatMessages(chunks)
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]int
	if err := json.Unmarshal(usageEventData(msg), &data); err != nil {
		t.Fatal(err)
	}
	if data["input_tokens"] != 42 || data["output_tokens"] != 7 || data["total_tokens"] != 49 {
		t.Fatalf("usage=%v", data)
	}
}
