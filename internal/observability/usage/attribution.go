package usage

import "sync/atomic"

// AgentAttributor maps an originating route/service/session back to a single
// agent id for write-time usage attribution. It is implemented by the agent
// control-plane layer and consumed here so the lower layers never depend on
// pkg/agent (the dependency arrow stays pkg/agent -> runtime). It returns
// ok=false when the mapping is empty or ambiguous, in which case the agent id is
// left empty.
type AgentAttributor interface {
	ResolveAgentID(routeID, serviceID, sessionID string) (string, bool)
}

// AgentAttribution is a settable holder for an AgentAttributor. The metrics
// pipeline (and therefore the observer) is constructed before the agent manager
// exists, so the concrete attributor is injected later via Set without rebuilding
// the observer.
type AgentAttribution struct {
	v atomic.Value // holds attributorBox
}

type attributorBox struct {
	attr AgentAttributor
}

func NewAgentAttribution() *AgentAttribution {
	return &AgentAttribution{}
}

// Set installs (or replaces) the active attributor. A nil attributor is ignored.
func (a *AgentAttribution) Set(attr AgentAttributor) {
	if a == nil || attr == nil {
		return
	}
	a.v.Store(attributorBox{attr: attr})
}

// ResolveAgentID delegates to the installed attributor, returning ok=false when
// none is installed.
func (a *AgentAttribution) ResolveAgentID(routeID, serviceID, sessionID string) (string, bool) {
	if a == nil {
		return "", false
	}
	box, ok := a.v.Load().(attributorBox)
	if !ok || box.attr == nil {
		return "", false
	}
	return box.attr.ResolveAgentID(routeID, serviceID, sessionID)
}

// AttributionFilter selects durable Agent identity. RouteIDs are an optional
// fallback for untagged exclusive MCP history only, never shared LLM traffic
// or events already attributed to another Agent.
type AttributionFilter struct {
	AgentID  string
	RouteIDs []string
}

// IsEmpty reports whether the filter carries no selector at all.
func (f *AttributionFilter) IsEmpty() bool {
	return f == nil || (f.AgentID == "" && len(f.RouteIDs) == 0)
}
