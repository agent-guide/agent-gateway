package provider

import (
	"context"

	einomodel "github.com/cloudwego/eino/components/model"
	einoschema "github.com/cloudwego/eino/schema"
)

type ChatRequestState struct {
	APIKey        string
	BaseURL       string
	ModelName     string
	Messages      []*einoschema.Message
	Options       []einomodel.Option
	CommonOptions *einomodel.Options
}

func ResolveChatRequest(ctx context.Context, config ProviderConfig, req *GenerateRequest) (*ChatRequestState, error) {
	apiKey, baseURL := ResolveCredential(ctx, config)
	modelName := req.Model
	if modelName == "" {
		modelName = config.DefaultModel
	}

	opts, err := ToEinoOptions(req)
	if err != nil {
		return nil, err
	}

	return &ChatRequestState{
		APIKey:        apiKey,
		BaseURL:       baseURL,
		ModelName:     modelName,
		Messages:      req.Messages,
		Options:       opts,
		CommonOptions: einomodel.GetCommonOptions(nil, opts...),
	}, nil
}

func ToEinoOptions(req *GenerateRequest) ([]einomodel.Option, error) {
	return append([]einomodel.Option(nil), req.Options...), nil
}

func FromEinoMessage(msg *einoschema.Message) *GenerateResponse {
	if msg == nil {
		return &GenerateResponse{}
	}

	return &GenerateResponse{
		Message: msg,
	}
}

func FinishReason(msg *einoschema.Message) string {
	if msg == nil || msg.ResponseMeta == nil {
		return ""
	}
	return msg.ResponseMeta.FinishReason
}

func UsageFromMessage(msg *einoschema.Message) Usage {
	if msg == nil || msg.ResponseMeta == nil || msg.ResponseMeta.Usage == nil {
		return Usage{}
	}

	usage := msg.ResponseMeta.Usage
	return Usage{
		InputTokens:  usage.PromptTokens,
		OutputTokens: usage.CompletionTokens,
	}
}
