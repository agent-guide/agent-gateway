package route

// Normalize fills runtime defaults on a route value before it is used by the gateway.
func (r *AgentRoute) Normalize() {
	if r == nil {
		return
	}
	r.Policy.Defaults()
}
