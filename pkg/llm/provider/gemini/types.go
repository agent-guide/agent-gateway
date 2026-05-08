package gemini

// ModelsResponse is the response from GET /v1beta/models.
type ModelsResponse struct {
	Models []ModelData `json:"models"`
}

// ModelData describes a single Gemini model.
type ModelData struct {
	Name             string `json:"name"` // "models/gemini-1.5-pro"
	DisplayName      string `json:"displayName"`
	Description      string `json:"description"`
	InputTokenLimit  int    `json:"inputTokenLimit"`
	OutputTokenLimit int    `json:"outputTokenLimit"`
}
