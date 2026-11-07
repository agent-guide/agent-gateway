// Package einochat shares execution plumbing for the selected eino-backed chat
// providers. Vendor model/message/option construction remains with each provider.
package einochat

import (
	"context"

	"github.com/agent-guide/agent-gateway/internal/statuserr"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider/openaibase"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type Build func() (einomodel.ToolCallingChatModel, []*schema.Message, []einomodel.Option, error)

// Generate rebuilds the vendor model on each existing non-stream retry. Builder
// errors keep their original treatment; only upstream Generate errors normalize.
func Generate(ctx context.Context, network provider.NetworkConfig, build Build) (*provider.ChatResponse, error) {
	return provider.RetryProviderCall(network, func() (*provider.ChatResponse, error) {
		model, messages, opts, err := build()
		if err != nil {
			return nil, err
		}
		message, err := model.Generate(ctx, messages, opts...)
		if err != nil {
			return nil, statuserr.Wrap(openaibase.NormalizeError(err), 502)
		}
		return provider.ChatResponseFromEinoMessage(message), nil
	})
}

// Stream opens once and returns the vendor stream directly. It never retries,
// buffers, consumes, converts or hides mid-stream errors/native protocol state.
func Stream(ctx context.Context, build Build) (*schema.StreamReader[*schema.Message], error) {
	model, messages, opts, err := build()
	if err != nil {
		return nil, err
	}
	stream, err := model.Stream(ctx, messages, opts...)
	if err != nil {
		return nil, statuserr.Wrap(openaibase.NormalizeError(err), 502)
	}
	return stream, nil
}
