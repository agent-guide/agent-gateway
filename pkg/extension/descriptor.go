package extension

// Descriptor describes a module explicitly linked by a distribution's main.
// It contains no factory pointers, secrets, entitlement or customer state.
type Descriptor struct {
	ID            string   `json:"id"`
	Version       string   `json:"version"`
	SchemaVersion int      `json:"schema_version"`
	Capabilities  []string `json:"capabilities,omitempty"`
	Dependencies  []string `json:"dependencies,omitempty"`
	Roles         []string `json:"roles,omitempty"`
}
