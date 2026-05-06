package credentialmgr

// Filter identifies credentials for manager-side listing and storage operations.
type Filter struct {
	Source       string
	ProviderType string
	ProviderID   string
	Model        string
}
