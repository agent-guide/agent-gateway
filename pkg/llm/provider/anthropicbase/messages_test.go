package anthropicbase

import (
	"testing"

	"github.com/cloudwego/eino/schema"
)

// TestConvertMessagesMergesParallelAssistantToolCalls reproduces the Codex
// parallel-tool-call replay: two separate assistant messages each carrying one
// tool_use, followed by the batched tool_results. The Anthropic API rejects an
// assistant tool_use that is not immediately followed by its tool_result, so
// the two assistant messages must merge into one with both tool_use blocks.
func TestConvertMessagesMergesParallelAssistantToolCalls(t *testing.T) {
	msgs := []*schema.Message{
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{
			{ID: "t1", Function: schema.FunctionCall{Name: "Bash", Arguments: `{"cmd":"a"}`}},
		}},
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{
			{ID: "t2", Function: schema.FunctionCall{Name: "Bash", Arguments: `{"cmd":"b"}`}},
		}},
		{Role: schema.Tool, ToolCallID: "t1", Content: "out1"},
		{Role: schema.Tool, ToolCallID: "t2", Content: "out2"},
	}

	out := ConvertMessages(msgs, &MessagesRequest{}, false)

	if len(out) != 2 {
		t.Fatalf("message count = %d, want 2 (merged assistant + tool_result user)", len(out))
	}
	if out[0].Role != "assistant" || len(out[0].Content) != 2 {
		t.Fatalf("assistant message = %+v, want one message with 2 tool_use blocks", out[0])
	}
	if out[0].Content[0].Type != "tool_use" || out[0].Content[0].ID != "t1" ||
		out[0].Content[1].Type != "tool_use" || out[0].Content[1].ID != "t2" {
		t.Fatalf("assistant tool_use blocks = %+v, want t1 then t2", out[0].Content)
	}
	if out[1].Role != "user" || len(out[1].Content) != 2 {
		t.Fatalf("user message = %+v, want 2 tool_result blocks", out[1])
	}
	if out[1].Content[0].ToolUseID != "t1" || out[1].Content[1].ToolUseID != "t2" {
		t.Fatalf("tool_result ids = %+v, want t1 then t2", out[1].Content)
	}
}

// TestConvertMessagesMergesConsecutiveUserMessages verifies that leading
// same-role user turns (Codex sends developer + multiple user items) collapse
// into a single alternating user turn.
func TestConvertMessagesMergesConsecutiveUserMessages(t *testing.T) {
	msgs := []*schema.Message{
		{Role: schema.User, Content: "first"},
		{Role: schema.User, Content: "second"},
		{Role: schema.Assistant, Content: "reply"},
	}

	out := ConvertMessages(msgs, &MessagesRequest{}, false)

	if len(out) != 2 {
		t.Fatalf("message count = %d, want 2", len(out))
	}
	if out[0].Role != "user" || len(out[0].Content) != 2 {
		t.Fatalf("user message = %+v, want 2 merged text blocks", out[0])
	}
	if out[0].Content[0].Text != "first" || out[0].Content[1].Text != "second" {
		t.Fatalf("merged user text = %+v, want first then second", out[0].Content)
	}
	if out[1].Role != "assistant" {
		t.Fatalf("second message role = %q, want assistant", out[1].Role)
	}
}
