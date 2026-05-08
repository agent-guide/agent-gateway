package embedding

import (
	"context"
	"fmt"
)

// OpenAIEmbedder generates embeddings using the OpenAI API.
type OpenAIEmbedder struct {
	apiKey string
	model  string
}

// NewOpenAIEmbedder creates an OpenAI embedder.
func NewOpenAIEmbedder(apiKey, model string) *OpenAIEmbedder {
	if model == "" {
		model = "text-embedding-3-small"
	}
	return &OpenAIEmbedder{apiKey: apiKey, model: model}
}

func (e *OpenAIEmbedder) Embed(ctx context.Context, text string) ([]float64, error) {
	// TODO: implement using net/http against OpenAI embeddings API
	return nil, fmt.Errorf("openai embedder: Embed not implemented")
}

func (e *OpenAIEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float64, error) {
	// TODO: implement batch embedding
	return nil, fmt.Errorf("openai embedder: EmbedBatch not implemented")
}

func (e *OpenAIEmbedder) Dimensions() int {
	switch e.model {
	case "text-embedding-3-large":
		return 3072
	case "text-embedding-ada-002":
		return 1536
	default: // text-embedding-3-small
		return 1536
	}
}
