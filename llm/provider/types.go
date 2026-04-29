package provider

// ModelInfo describes a model available from a provider.
type ModelInfo struct {
	ID           string
	Name         string
	Description  string
	Capabilities ProviderCapabilities
}

// Usage contains token consumption information.
type Usage struct {
	InputTokens  int
	OutputTokens int
}
