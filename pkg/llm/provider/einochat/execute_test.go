package einochat

import (
	"context"
	"errors"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/llm/provider/openaibase"

	"github.com/agent-guide/agent-gateway/internal/statuserr"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type modelStub struct {
	generate func(context.Context, []*schema.Message, ...einomodel.Option) (*schema.Message, error)
	stream   func(context.Context, []*schema.Message, ...einomodel.Option) (*schema.StreamReader[*schema.Message], error)
}

func (m *modelStub) Generate(ctx context.Context, messages []*schema.Message, opts ...einomodel.Option) (*schema.Message, error) {
	return m.generate(ctx, messages, opts...)
}
func (m *modelStub) Stream(ctx context.Context, messages []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	return m.stream(ctx, messages, opts...)
}
func (m *modelStub) WithTools([]*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	return m, nil
}

func TestGenerateRetainsRetryConstructionAndMessage(t *testing.T) {
	builds, calls := 0, 0
	input := schema.UserMessage("hello")
	output := schema.AssistantMessage("", []schema.ToolCall{{ID: "call", Type: "function", Function: schema.FunctionCall{Name: "lookup", Arguments: `{"q":"x"}`}}})
	model := &modelStub{generate: func(ctx context.Context, messages []*schema.Message, opts ...einomodel.Option) (*schema.Message, error) {
		calls++
		if ctx != t.Context() || len(messages) != 1 || messages[0] != input || len(opts) != 1 {
			t.Fatal("construction inputs changed")
		}
		if calls == 1 {
			return nil, &provider.UpstreamError{Status: 429, Body: "retry"}
		}
		return output, nil
	}}
	response, err := Generate(t.Context(), provider.NetworkConfig{MaxRetries: 1}, func() (einomodel.ToolCallingChatModel, []*schema.Message, []einomodel.Option, error) {
		builds++
		return model, []*schema.Message{input}, []einomodel.Option{einomodel.WithTemperature(0.5)}, nil
	})
	if err != nil || response.Message != output || builds != 2 || calls != 2 {
		t.Fatalf("response=%v err=%v builds=%d calls=%d", response, err, builds, calls)
	}
}

func TestGenerateBuilderAndUpstreamClientErrors(t *testing.T) {
	rejected := statuserr.New(400, "builder rejected")
	builds := 0
	_, err := Generate(t.Context(), provider.NetworkConfig{MaxRetries: 1}, func() (einomodel.ToolCallingChatModel, []*schema.Message, []einomodel.Option, error) {
		builds++
		return nil, nil, nil, rejected
	})
	if !errors.Is(err, rejected) || builds != 1 || statuserr.StatusCode(err, 0) != 400 {
		t.Fatalf("builder failure changed: %v, attempts %d", err, builds)
	}
	upstream := &modelStub{generate: func(context.Context, []*schema.Message, ...einomodel.Option) (*schema.Message, error) {
		return nil, &provider.UpstreamError{Status: 401, Body: "invalid key"}
	}}
	_, err = Generate(t.Context(), provider.NetworkConfig{MaxRetries: 1}, func() (einomodel.ToolCallingChatModel, []*schema.Message, []einomodel.Option, error) {
		return upstream, nil, nil, nil
	})
	if statuserr.StatusCode(err, 0) != 401 {
		t.Fatalf("upstream status changed: %v", err)
	}
}

func TestStreamPassesThroughPartialOutputAndFailure(t *testing.T) {
	reader, writer := schema.Pipe[*schema.Message](2)
	defer reader.Close()
	output := schema.AssistantMessage("partial", nil)
	interrupted := errors.New("upstream interrupted")
	writer.Send(output, nil)
	writer.Send(nil, interrupted)
	writer.Close()
	opens := 0
	model := &modelStub{stream: func(context.Context, []*schema.Message, ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
		opens++
		return reader, nil
	}}
	stream, err := Stream(t.Context(), func() (einomodel.ToolCallingChatModel, []*schema.Message, []einomodel.Option, error) {
		return model, nil, nil, nil
	})
	if err != nil || stream != reader || opens != 1 {
		t.Fatal("stream was wrapped or reopened")
	}
	if got, err := stream.Recv(); err != nil || got != output {
		t.Fatal("partial output changed")
	}
	if _, err := stream.Recv(); !errors.Is(err, interrupted) {
		t.Fatalf("midstream failure hidden: %v", err)
	}
}

func TestStreamOpenErrorsDoNotRetryAndPreserveCancellationMapping(t *testing.T) {
	for _, upstreamErr := range []error{&provider.UpstreamError{Status: 429, Body: "busy"}, context.Canceled} {
		builds, opens := 0, 0
		model := &modelStub{stream: func(context.Context, []*schema.Message, ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
			opens++
			return nil, upstreamErr
		}}
		_, err := Stream(t.Context(), func() (einomodel.ToolCallingChatModel, []*schema.Message, []einomodel.Option, error) {
			builds++
			return model, nil, nil, nil
		})
		if builds != 1 || opens != 1 {
			t.Fatal("stream open retried")
		}
		expected := statuserr.Wrap(openaibase.NormalizeError(upstreamErr), 502)
		if err.Error() != expected.Error() || statuserr.StatusCode(err, 0) != statuserr.StatusCode(expected, 0) || errors.Is(err, context.Canceled) != errors.Is(expected, context.Canceled) {
			t.Fatalf("stream opening error mapping changed: %v versus %v", err, expected)
		}
	}
}
