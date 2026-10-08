// Package extensionconsumer is an independently compiled distribution fixture.
package extensionconsumer

import (
	"github.com/agent-guide/agent-gateway/pkg/distribution"
	"github.com/agent-guide/agent-gateway/pkg/extension"
)

func Runtime() distribution.Assembly {
	a := distribution.CommunityRuntime()
	a.Edition = distribution.Edition{Name: "fixture", Version: "1.0.0"}
	a.Modules = append(a.Modules, extension.Descriptor{ID: "example.fixture.entries", Version: "1.0.0", SchemaVersion: 1, Roles: []string{"runtime", "validation"}})
	a.Modules = append(a.Modules, extension.Descriptor{ID: "example.fixture.mutable", Version: "1.0.0", SchemaVersion: 1, Dependencies: []string{"example.fixture.entries"}, Roles: []string{"runtime", "validation"}})
	a.Factories = map[string]*extension.Factory{"example.fixture.entries": fixtureFactory(true), "example.fixture.mutable": fixtureFactory(true)}
	return a
}
func Validation() distribution.Assembly {
	a := distribution.CommunityValidation()
	a.Edition = distribution.Edition{Name: "fixture", Version: "1.0.0"}
	a.Modules = append(a.Modules, extension.Descriptor{ID: "example.fixture.entries", Version: "1.0.0", SchemaVersion: 1, Roles: []string{"validation"}})
	a.Modules = append(a.Modules, extension.Descriptor{ID: "example.fixture.mutable", Version: "1.0.0", SchemaVersion: 1, Dependencies: []string{"example.fixture.entries"}, Roles: []string{"validation"}})
	a.Factories = map[string]*extension.Factory{"example.fixture.entries": fixtureFactory(false), "example.fixture.mutable": fixtureFactory(false)}
	return a
}
