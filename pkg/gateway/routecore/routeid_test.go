package routecore

import (
	"strings"
	"testing"
)

func TestGenerateRouteIDIsDeterministicAndSlashFree(t *testing.T) {
	cases := map[string]string{
		"/acp/opencode": "acp:opencode-main:acp-opencode",
		"/":             "acp:opencode-main:root",
		"":              "acp:opencode-main:root",
		"/mcp/Foo/bar/": "acp:opencode-main:mcp-foo-bar",
	}
	for path, want := range cases {
		id := GenerateRouteID("acp", "opencode-main", path)
		if id != want {
			t.Fatalf("GenerateRouteID(%q) = %q, want %q", path, id, want)
		}
		if strings.ContainsAny(id, "/\\") {
			t.Fatalf("generated id must be slash-free: %q", id)
		}
		if err := ValidateRouteID(id); err != nil {
			t.Fatalf("generated id must pass validation: %q: %v", id, err)
		}
	}
}

func TestGenerateRouteIDSameSlugCollides(t *testing.T) {
	// No disambiguating suffix: distinct paths that slugify to the same value
	// collide by design, surfacing later as a duplicate-id error.
	a := GenerateRouteID("mcp", "svc", "/mcp/foo")
	b := GenerateRouteID("mcp", "svc", "/mcp-foo")
	if a != b {
		t.Fatalf("paths with the same slug must collide: %q vs %q", a, b)
	}
}

func TestValidateRouteIDRejectsSlashes(t *testing.T) {
	for _, id := range []string{"mcp:svc:/mcp", "a/b", "a\\b"} {
		if err := ValidateRouteID(id); err == nil {
			t.Fatalf("expected %q to be rejected", id)
		}
	}
	if err := ValidateRouteID("mcp:svc:mcp-3f9a1c2b"); err != nil {
		t.Fatalf("slash-free id must be accepted: %v", err)
	}
}
