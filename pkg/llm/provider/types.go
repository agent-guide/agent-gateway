package provider

// ModelInfo describes a model available from a provider.
type ModelInfo struct {
	ID           string
	Name         string
	DisplayName  string
	Description  string
	Capabilities ModelCapabilities
}

// Usage contains token consumption information.
type Usage struct {
	InputTokens  int
	OutputTokens int
}
