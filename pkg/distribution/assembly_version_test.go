package distribution

import "testing"

func TestCommunityManifestVersion(t *testing.T) {
	const want = "0.7.1"
	for name, assembly := range map[string]Assembly{
		"runtime":    CommunityRuntime(),
		"validation": CommunityValidation(),
	} {
		t.Run(name, func(t *testing.T) {
			manifest, err := assembly.Manifest()
			if err != nil {
				t.Fatal(err)
			}
			if manifest.Edition.Version != want {
				t.Fatalf("edition version = %q, want %q", manifest.Edition.Version, want)
			}
			if len(manifest.Modules) == 0 {
				t.Fatal("manifest has no modules")
			}
			for _, module := range manifest.Modules {
				if module.Version != want {
					t.Errorf("module %q version = %q, want %q", module.ID, module.Version, want)
				}
			}
		})
	}
}
