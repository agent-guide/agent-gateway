package anthropic

// ModelsResponse is the response from GET /v1/models.
type ModelsResponse struct {
	Data []ModelData `json:"data"`
}

// ModelData describes a single model.
type ModelData struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}
