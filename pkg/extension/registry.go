package extension

import (
	"context"
	"encoding/json"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Error carries bounded, secret-free diagnostics. Codes are stable; Error never
// incorporates errors returned by module callbacks.
type Error struct {
	Code     string `json:"code"`
	ModuleID string `json:"module_id,omitempty"`
	Field    string `json:"field,omitempty"`
}

func (e *Error) Error() string             { return e.Code + ": " + e.ModuleID + " " + e.Field }
func failure(code, id, field string) error { return &Error{code, id, field} }

// Manifest is a deterministic metadata projection, with no executable pointers.
type Manifest struct {
	Modules []Descriptor `json:"modules"`
}

// Registration binds a descriptor to optional configuration behavior. A nil
// Factory is valid for metadata-only modules; their envelopes are rejected.
type Registration struct {
	Descriptor Descriptor
	Factory    *Factory
}

// Factory validates reference-preserving JSON and prepares owned resources.
// Callbacks must honor cancellation and must never mutate a live snapshot.
// Validate may apply defaults, but must retain every declared secret reference.
type Factory struct {
	AdminRoutes  []AdminRoute
	SecretFields []string
	Validate     func(context.Context, json.RawMessage) (json.RawMessage, error)
	Dependencies func(context.Context, json.RawMessage) ([]string, error)
	Prepare      func(context.Context, PreparedConfig, Snapshot) (Resource, error)
}

// Resource belongs to a generation. All reads must be concurrency-safe and
// Health/Close must honor their context. Close is called once per preparation.
type Resource interface {
	Health(context.Context) error
	Close(context.Context) error
}

// Registry is immutable after construction and owns all descriptor slices.
type Registry struct {
	entries map[string]Registration
	order   []string
}

var idPattern = regexp.MustCompile(`^[a-z0-9-]+(\.[a-z0-9-]+)+$`)
var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

func ValidID(s string) bool { return len(s) <= 128 && idPattern.MatchString(s) }
func cloneDescriptor(d Descriptor) Descriptor {
	d.Capabilities = slices.Clone(d.Capabilities)
	d.Dependencies = slices.Clone(d.Dependencies)
	d.Roles = slices.Clone(d.Roles)
	return d
}
func sortedUnique(values []string, valid func(string) bool) ([]string, bool) {
	out := slices.Clone(values)
	sort.Strings(out)
	for i, v := range out {
		if !valid(v) || i > 0 && out[i-1] == v {
			return nil, false
		}
	}
	return out, true
}

// NewRegistry validates all registrations and resolves dependencies before any
// callback executes. Empty registries are valid; nil registrations are not.
func NewRegistry(registrations ...Registration) (*Registry, error) {
	if len(registrations) > 256 {
		return nil, failure("invalid_registration", "", "modules")
	}
	r := &Registry{entries: map[string]Registration{}}
	for _, reg := range registrations {
		d := cloneDescriptor(reg.Descriptor)
		bad := func(field string) (*Registry, error) {
			return nil, failure("invalid_registration", safeID(d.ID), field)
		}
		if !ValidID(d.ID) {
			return bad("id")
		}
		if !versionPattern.MatchString(d.Version) || len(d.Version) > 128 {
			return bad("version")
		}
		// Semver numeric prerelease identifiers cannot contain leading zeros.
		versionCore, _, _ := strings.Cut(d.Version, "+")
		if _, pre, ok := strings.Cut(versionCore, "-"); ok {
			for _, p := range strings.Split(pre, ".") {
				if regexp.MustCompile(`^[0-9]+$`).MatchString(p) && len(p) > 1 && p[0] == '0' {
					return bad("version")
				}
			}
		}

		if d.SchemaVersion <= 0 {
			return bad("schema_version")
		}
		if len(d.Capabilities) > 64 || len(d.Dependencies) > 64 {
			return bad("metadata")
		}
		var ok bool
		if d.Capabilities, ok = sortedUnique(d.Capabilities, ValidID); !ok {
			return bad("capabilities")
		}
		if d.Dependencies, ok = sortedUnique(d.Dependencies, ValidID); !ok {
			return bad("dependencies")
		}
		if d.Roles, ok = sortedUnique(d.Roles, func(s string) bool { return slices.Contains([]string{"runtime", "validation", "admin", "spi"}, s) }); !ok || len(d.Roles) == 0 {
			return bad("roles")
		}
		encoded, _ := json.Marshal(d)
		if len(encoded) > 64*1024 {
			return bad("metadata")
		}
		if _, exists := r.entries[d.ID]; exists {
			return nil, failure("duplicate_module", d.ID, "")
		}
		reg.Descriptor = d
		if reg.Factory != nil {
			f := *reg.Factory
			f.AdminRoutes = slices.Clone(f.AdminRoutes)
			if err := validateAdminRoutes(d.ID, f.AdminRoutes); err != nil {
				return nil, err
			}
			if len(f.AdminRoutes) > 0 && (!slices.Contains(d.Roles, "admin") || f.Prepare == nil) {
				return bad("admin_routes")
			}
			f.SecretFields = slices.Clone(f.SecretFields)
			if f.Validate == nil || (f.Prepare == nil && !slices.Equal(d.Roles, []string{"validation"})) || len(f.SecretFields) > 64 {
				return bad("factory")
			}
			if f.SecretFields, ok = sortedUnique(f.SecretFields, validPointer); !ok {
				return bad("secret_fields")
			}
			reg.Factory = &f
		}
		r.entries[d.ID] = reg
	}
	order, err := r.topological(nil)
	if err != nil {
		return nil, err
	}
	r.order = order
	return r, nil
}
func safeID(id string) string {
	if ValidID(id) {
		return id
	}
	return ""
}
func nilResource(r Resource) bool {
	if r == nil {
		return true
	}
	v := reflect.ValueOf(r)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return v.IsNil()
	}
	return false
}

// Manifest returns independent, sorted metadata, never invokes factories.
func (r *Registry) Manifest() Manifest {
	m := Manifest{Modules: []Descriptor{}}
	if r == nil {
		return m
	}
	for _, reg := range r.entries {
		m.Modules = append(m.Modules, cloneDescriptor(reg.Descriptor))
	}
	sort.Slice(m.Modules, func(i, j int) bool { return m.Modules[i].ID < m.Modules[j].ID })
	return m
}
func (r *Registry) topological(extra map[string][]string) ([]string, error) {
	ids := make([]string, 0, len(r.entries))
	for id := range r.entries {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	incoming := map[string]int{}
	dependents := map[string][]string{}
	for _, id := range ids {
		deps := slices.Clone(r.entries[id].Descriptor.Dependencies)
		deps = append(deps, extra[id]...)
		sort.Strings(deps)
		deps = slices.Compact(deps)
		incoming[id] = len(deps)
		for _, dep := range deps {
			if _, ok := r.entries[dep]; !ok {
				return nil, failure("missing_dependency", id, "dependencies")
			}
			dependents[dep] = append(dependents[dep], id)
		}
	}
	ready := []string{}
	for _, id := range ids {
		if incoming[id] == 0 {
			ready = append(ready, id)
		}
	}
	order := make([]string, 0, len(ids))
	for len(ready) > 0 {
		// Re-sort after dependencies complete: newly ready modules participate in
		// the same lexical tie rule as those already waiting.
		sort.Strings(ready)
		id := ready[0]
		ready = ready[1:]
		order = append(order, id)
		for _, dependent := range dependents[id] {
			incoming[dependent]--
			if incoming[dependent] == 0 {
				ready = append(ready, dependent)
			}
		}
	}
	if len(order) != len(ids) {
		for _, id := range ids {
			if incoming[id] > 0 {
				return nil, failure("dependency_cycle", id, "")
			}
		}
	}
	return order, nil
}
