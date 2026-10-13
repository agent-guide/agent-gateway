package extension

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/agent-guide/agent-gateway/pkg/attribution"
	"github.com/agent-guide/agent-gateway/pkg/event"
	"go.uber.org/zap"
)

// AdminRoute declares a method/path owned by its registration's module. Paths
// are absolute, with literal segments or single-segment {name} wildcards.
// Subtree, host, multi-segment wildcard and root-envelope routes are unsupported.
// GET also owns HEAD. Overlapping declarations are rejected, even if one would
// be more specific in net/http's mux.
type AdminRoute struct {
	Method string
	Path   string
}

// AdminDependencies is request-scoped. Config is an owned, reference-preserving
// copy, never resolved secrets. Events accepts only this owner's typed mutation
// events and stamps the current trusted attribution. No gateway/store is exposed.
type AdminDependencies struct {
	Config      Envelope
	Logger      *zap.Logger
	Attribution func(context.Context) (attribution.Value, bool)
	Events      event.Publisher
}

// AdminContributor is implemented by a prepared Resource whose factory declares
// AdminRoutes. Handlers use r.Pattern/PathValue to dispatch; they must honor request
// cancellation, be concurrency-safe and use WriteAdminError for bounded errors.
// Health and Close are inherited from Resource. The generation lease covers the
// whole handler call and its context is canceled on bounded generation retirement.
type AdminContributor interface {
	ServeAdminHTTP(http.ResponseWriter, *http.Request, AdminDependencies)
}

// OwnedAdminRoute is the immutable registry projection used at Admin assembly.
type OwnedAdminRoute struct {
	ModuleID string
	AdminRoute
}

var adminLiteral = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
var adminWildcard = regexp.MustCompile(`^\{[A-Za-z_][A-Za-z0-9_]*\}$`)
var adminCode = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func validateAdminRoutes(id string, routes []AdminRoute) error {
	if len(routes) > 64 {
		return failure("invalid_registration", id, "admin_routes")
	}
	for i, r := range routes {
		if !slices.Contains([]string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}, r.Method) {
			return failure("invalid_registration", id, "admin_routes")
		}
		prefix := "/admin/extensions/" + id + "/"
		if len(r.Path) > 1024 || !strings.HasPrefix(r.Path, prefix) || r.Method == "OPTIONS" {
			// OPTIONS remains the shared mount's CORS preflight operation.
			return failure("route_conflict", id, "admin_routes")
		}
		names := map[string]bool{}
		for _, segment := range strings.Split(strings.TrimPrefix(r.Path, prefix), "/") {
			if adminWildcard.MatchString(segment) {
				if names[segment] {
					return failure("route_conflict", id, "admin_routes")
				}
				names[segment] = true
			} else if !adminLiteral.MatchString(segment) || segment == "." || segment == ".." {
				return failure("route_conflict", id, "admin_routes")
			}
		}
		for _, previous := range routes[:i] {
			if adminRoutesOverlap(previous, r) {
				return failure("route_conflict", id, "admin_routes")
			}
		}
	}
	return nil
}
func adminRoutesOverlap(a, b AdminRoute) bool {
	methods := a.Method == b.Method || a.Method == "GET" && b.Method == "HEAD" || a.Method == "HEAD" && b.Method == "GET"
	if !methods {
		return false
	}
	as, bs := strings.Split(a.Path, "/"), strings.Split(b.Path, "/")
	if len(as) != len(bs) {
		return false
	}
	for i := range as {
		if as[i] != bs[i] && !adminWildcard.MatchString(as[i]) && !adminWildcard.MatchString(bs[i]) {
			return false
		}
	}
	return true
}

// AdminRoutes returns copies of the complete, validated declaration set. The
// literal module namespace makes contributions disjoint from all Community
// route families, including the separate AgentRoute mux and envelope roots.
func (r *Registry) AdminRoutes() []OwnedAdminRoute {
	var out []OwnedAdminRoute
	if r != nil {
		for _, id := range r.order {
			if f := r.entries[id].Factory; f != nil {
				for _, route := range f.AdminRoutes {
					out = append(out, OwnedAdminRoute{ModuleID: id, AdminRoute: route})
				}
			}
		}
	}
	return out
}

// AdminRoutes snapshots the linked registry declarations once during HTTP assembly.
func (m *Manager) AdminRoutes() []OwnedAdminRoute { return m.registry.AdminRoutes() }

// WriteAdminError writes bounded JSON without arbitrary callback diagnostics.
func WriteAdminError(w http.ResponseWriter, status int, code string) {
	if status < 400 || status > 599 {
		status = http.StatusInternalServerError
	}
	if !adminCode.MatchString(code) {
		code = "extension_unavailable"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "code": code})
}

// AdminPublisher binds a validated owner once during HTTP assembly and returns
// a request binder. Each invocation captures the trusted request context without
// registry/configuration reads. Publish's context controls cancellation/delivery
// only: a contributor cannot replace the captured request actor or correlation.
func (m *Manager) AdminPublisher(id string) func(context.Context) event.Publisher {
	reg, ok := m.registry.entries[id]
	if !ok || reg.Factory == nil || len(reg.Factory.AdminRoutes) == 0 {
		return nil
	}
	return func(trusted context.Context) event.Publisher {
		return adminPublisher{manager: m, owner: id, trusted: trusted}
	}
}

type adminPublisher struct {
	manager *Manager
	owner   string
	trusted context.Context
}

func (p adminPublisher) Publish(ctx context.Context, e event.Envelope) bool {
	if e.Family != "extension.mutation" || e.Mutation == nil || e.Mutation.ModuleID != p.owner {
		return false
	}
	// FromContext returns a fresh defensive copy for every event, even if a
	// downstream publisher mutates the value it receives.
	e.Attribution, _ = attribution.FromContext(p.trusted)
	p.manager.mu.Lock()
	publisher := p.manager.publisher
	p.manager.mu.Unlock()
	return publisher != nil && publisher.Publish(ctx, e)
}
