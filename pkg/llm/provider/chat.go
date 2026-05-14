package provider

import (
	"context"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// ChatRequest is the unified internal chat request format passed to providers.
type ChatRequest struct {
	Model    string
	Messages []*schema.Message
	Options  []einomodel.Option
}

// ChatResponse is the unified internal chat response format returned by providers.
type ChatResponse struct {
	Message *schema.Message
}

type ChatRequestState struct {
	ModelName     string
	Messages      []*schema.Message
	Options       []einomodel.Option
	CommonOptions *einomodel.Options
}

func ResolveChatRequest(_ context.Context, config ProviderConfig, req *ChatRequest) (*ChatRequestState, error) {
	modelName := req.Model
	if modelName == "" {
		modelName = config.DefaultModel
	}

	opts := append([]einomodel.Option(nil), req.Options...)

	return &ChatRequestState{
		ModelName:     modelName,
		Messages:      req.Messages,
		Options:       opts,
		CommonOptions: einomodel.GetCommonOptions(nil, opts...),
	}, nil
}

func ChatResponseFromEinoMessage(msg *schema.Message) *ChatResponse {
	if msg == nil {
		return &ChatResponse{}
	}

	return &ChatResponse{
		Message: msg,
	}
}

func FinishReason(msg *schema.Message) string {
	if msg == nil || msg.ResponseMeta == nil {
		return ""
	}
	return msg.ResponseMeta.FinishReason
}

func UsageFromMessage(msg *schema.Message) Usage {
	if msg == nil || msg.ResponseMeta == nil || msg.ResponseMeta.Usage == nil {
		return Usage{}
	}

	usage := msg.ResponseMeta.Usage
	return Usage{
		InputTokens:  usage.PromptTokens,
		OutputTokens: usage.CompletionTokens,
	}
}
