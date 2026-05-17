package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/agent-guide/agent-gateway/internal/statuserr"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// CreateResponsesViaChat adapts a Responses API request onto the provider Chat API.
// Providers opt into this compatibility path explicitly by calling this helper.
func CreateResponsesViaChat(ctx context.Context, prov Provider, req *ResponsesRequest) (*ResponsesResponse, error) {
	chatReq, err := ResponsesToChatRequest(req)
	if err != nil {
		return nil, err
	}
	resp, err := prov.Chat(ctx, chatReq)
	if err != nil {
		return nil, err
	}
	return ResponsesFromChatResponse(resp, chatReq.Model), nil
}

// StreamResponsesViaChat adapts a streaming Responses API request onto the provider
// Chat stream API. Providers opt into this compatibility path explicitly by
// calling this helper.
func StreamResponsesViaChat(ctx context.Context, prov Provider, req *ResponsesRequest) (*schema.StreamReader[*ResponsesStreamEvent], error) {
	chatReq, err := ResponsesToChatRequest(req)
	if err != nil {
		return nil, err
	}
	stream, err := prov.StreamChat(ctx, chatReq)
	if err != nil {
		return nil, err
	}

	sr, sw := schema.Pipe[*ResponsesStreamEvent](16)
	go func() {
		defer stream.Close()
		defer sw.Close()

		resp := ResponsesFromChatResponse(&ChatResponse{}, chatReq.Model)
		sw.Send(ResponsesCreatedEvent(resp), nil)

		for {
			chunk, err := stream.Recv()
			if err != nil {
				if err == io.EOF {
					sw.Send(ResponsesCompletedEvent(resp), nil)
					return
				}
				sw.Send(ResponsesCompletedEvent(resp), err)
				return
			}

			if chunk == nil {
				continue
			}
			if text := messageText(chunk); text != "" {
				resp.Output[0].Content[0].Text += text
				sw.Send(ResponsesDeltaEvent(resp.Output[0].ID, text), nil)
			}
			resp.Usage = responsesUsageFromMessage(chunk)
		}
	}()
	return sr, nil
}

// ResponsesToChatRequest converts a minimal Responses API request into ChatRequest.
func ResponsesToChatRequest(req *ResponsesRequest) (*ChatRequest, error) {
	if req == nil {
		return nil, statuserr.New(http.StatusBadRequest, "invalid request")
	}
	if strings.TrimSpace(req.PreviousResponseID) != "" {
		return nil, statuserr.New(http.StatusBadRequest, "previous_response_id is not supported")
	}
	if req.Store != nil && *req.Store {
		return nil, statuserr.New(http.StatusBadRequest, "store=true is not supported")
	}

	messages, err := responsesInputToMessages(req.Input)
	if err != nil {
		return nil, err
	}

	chatReq := &ChatRequest{
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

// ResponsesFromChatResponse converts a Chat response into a minimal Responses envelope.
func ResponsesFromChatResponse(resp *ChatResponse, model string) *ResponsesResponse {
	var msg *schema.Message
	if resp != nil {
		msg = resp.Message
	}
	now := time.Now()
	return &ResponsesResponse{
		ID:        fmt.Sprintf("resp_%d", now.UnixNano()),
		Object:    "response",
		CreatedAt: now.Unix(),
		Model:     model,
		Output: []ResponsesResponseOutput{{
			ID:     fmt.Sprintf("msg_%d", now.UnixNano()),
			Type:   "message",
			Role:   "assistant",
			Status: "completed",
			Content: []ResponsesResponseContentPart{{
				Type:        "output_text",
				Text:        messageText(msg),
				Annotations: []any{},
			}},
		}},
		Usage: responsesUsageFromMessage(msg),
	}
}

func ResponsesCreatedEvent(resp *ResponsesResponse) *ResponsesStreamEvent {
	return &ResponsesStreamEvent{
		Type:     "response.created",
		Response: resp,
	}
}

func ResponsesDeltaEvent(itemID string, delta string) *ResponsesStreamEvent {
	return &ResponsesStreamEvent{
		Type:         "response.output_text.delta",
		ItemID:       itemID,
		OutputIndex:  0,
		ContentIndex: 0,
		Delta:        delta,
	}
}

func ResponsesCompletedEvent(resp *ResponsesResponse) *ResponsesStreamEvent {
	return &ResponsesStreamEvent{
		Type:     "response.completed",
		Response: resp,
	}
}

func responsesInputToMessages(input any) ([]*schema.Message, error) {
	switch v := input.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return nil, statuserr.New(http.StatusBadRequest, "input is required")
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
			return nil, statuserr.New(http.StatusBadRequest, "input is required")
		}
		return msgs, nil
	default:
		return nil, statuserr.New(http.StatusBadRequest, "unsupported input type for responses api")
	}
}

func responseInputItemToMessage(item any) (*schema.Message, error) {
	obj, ok := item.(map[string]any)
	if !ok {
		return nil, statuserr.New(http.StatusBadRequest, "unsupported input item type for responses api")
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
				return nil, statuserr.New(http.StatusBadRequest, "unsupported input content part for responses api")
			}
			partType, _ := part["type"].(string)
			if partType != "" && partType != "input_text" && partType != "text" {
				return nil, statuserr.New(http.StatusBadRequest, fmt.Sprintf("unsupported input content type %q for responses api", partType))
			}
			text, _ := part["text"].(string)
			if strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		}
		return &schema.Message{Role: schema.RoleType(role), Content: strings.Join(parts, "\n")}, nil
	default:
		return nil, statuserr.New(http.StatusBadRequest, "unsupported input content for responses api")
	}
}

func responsesUsageFromMessage(msg *schema.Message) *ResponsesResponseUsage {
	usage := UsageFromMessage(msg)
	return &ResponsesResponseUsage{
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
		TotalTokens:  usage.InputTokens + usage.OutputTokens,
	}
}

func messageText(msg *schema.Message) string {
	if msg == nil {
		return ""
	}
	return msg.Content
}
