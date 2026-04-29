package provider

import (
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
