package openai

import (
	"fmt"
	"strings"
	"time"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/agent-guide/caddy-agent-gateway/pkg/llm/provider"
)

// ResponsesRequest reuses the provider-level request model directly because
// Responses API requests are passed through to supporting providers unchanged.
type ResponsesRequest = provider.ResponsesRequest

func responsesToInternal(req *ResponsesRequest) (*provider.ChatRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("invalid request")
	}
	if strings.TrimSpace(req.PreviousResponseID) != "" {
		return nil, fmt.Errorf("previous_response_id is not supported")
	}
	if req.Store != nil && *req.Store {
		return nil, fmt.Errorf("store=true is not supported")
	}

	messages, err := responsesInputToMessages(req.Input)
	if err != nil {
		return nil, err
	}

	chatReq := &provider.ChatRequest{
		Model:    req.Model,
		Messages: messages,
	}

	if instructions := strings.TrimSpace(req.Instructions); instructions != "" {
		chatReq.Messages = append([]*schema.Message{{
			Role:    schema.System,
			Content: instructions,
		}}, chatReq.Messages...)
	}

	var opts []einomodel.Option
	if req.Temperature != 0 {
		opts = append(opts, einomodel.WithTemperature(float32(req.Temperature)))
	}
	if req.TopP != 0 {
		opts = append(opts, einomodel.WithTopP(float32(req.TopP)))
	}
	if req.MaxOutputTokens > 0 {
		opts = append(opts, einomodel.WithMaxTokens(req.MaxOutputTokens))
	}
	chatReq.Options = opts
	return chatReq, nil
}

func responsesInputToMessages(input any) ([]*schema.Message, error) {
	switch v := input.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return nil, fmt.Errorf("input is required")
		}
		return []*schema.Message{{
			Role:    schema.User,
			Content: v,
		}}, nil
	case []any:
		msgs := make([]*schema.Message, 0, len(v))
		for _, item := range v {
			msg, err := responseInputItemToMessage(item)
			if err != nil {
				return nil, err
			}
			if msg != nil {
				msgs = append(msgs, msg)
			}
		}
		if len(msgs) == 0 {
			return nil, fmt.Errorf("input is required")
		}
		return msgs, nil
	default:
		return nil, fmt.Errorf("unsupported input type for responses api")
	}
}

func responseInputItemToMessage(item any) (*schema.Message, error) {
	obj, ok := item.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unsupported input item type for responses api")
	}

	role, _ := obj["role"].(string)
	if strings.TrimSpace(role) == "" {
		role = string(schema.User)
	}

	switch content := obj["content"].(type) {
	case string:
		return &schema.Message{Role: schema.RoleType(role), Content: content}, nil
	case []any:
		var parts []string
		for _, raw := range content {
			part, ok := raw.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("unsupported input content part for responses api")
			}
			partType, _ := part["type"].(string)
			if partType != "" && partType != "input_text" && partType != "text" {
				return nil, fmt.Errorf("unsupported input content type %q for responses api", partType)
			}
			text, _ := part["text"].(string)
			if strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		}
		return &schema.Message{Role: schema.RoleType(role), Content: strings.Join(parts, "\n")}, nil
	default:
		return nil, fmt.Errorf("unsupported input content for responses api")
	}
}

func responsesFromInternal(resp *provider.ChatResponse, model string) *provider.ResponsesResponse {
	var msg *schema.Message
	if resp != nil {
		msg = resp.Message
	}
	content := messageText(msg)
	usage := provider.UsageFromMessage(msg)
	return &provider.ResponsesResponse{
		ID:        fmt.Sprintf("resp_%d", time.Now().UnixNano()),
		Object:    "response",
		CreatedAt: time.Now().Unix(),
		Model:     model,
		Output: []provider.ResponsesResponseOutput{{
			ID:     fmt.Sprintf("msg_%d", time.Now().UnixNano()),
			Type:   "message",
			Role:   "assistant",
			Status: "completed",
			Content: []provider.ResponsesResponseContentPart{{
				Type:        "output_text",
				Text:        content,
				Annotations: []any{},
			}},
		}},
		Usage: &provider.ResponsesResponseUsage{
			InputTokens:  usage.InputTokens,
			OutputTokens: usage.OutputTokens,
			TotalTokens:  usage.InputTokens + usage.OutputTokens,
		},
	}
}

func responsesCreatedEvent(resp *provider.ResponsesResponse) *provider.ResponsesStreamEvent {
	return &provider.ResponsesStreamEvent{
		Type:     "response.created",
		Response: resp,
	}
}

func responsesDeltaEvent(itemID string, delta string) *provider.ResponsesStreamEvent {
	return &provider.ResponsesStreamEvent{
		Type:         "response.output_text.delta",
		ItemID:       itemID,
		OutputIndex:  0,
		ContentIndex: 0,
		Delta:        delta,
	}
}

func responsesCompletedEvent(resp *provider.ResponsesResponse) *provider.ResponsesStreamEvent {
	return &provider.ResponsesStreamEvent{
		Type:     "response.completed",
		Response: resp,
	}
}
