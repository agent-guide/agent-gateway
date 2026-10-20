package release_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/distribution"
)

type releaseMetadata struct {
	Version      string `json:"version"`
	SourceCommit string `json:"source_commit"`
	GoVersion    string `json:"go_version"`
	Target       struct {
		OS   string `json:"os"`
		Arch string `json:"arch"`
	} `json:"target"`
}

func TestReleaseArtifactIdentity(t *testing.T) {
	dir := os.Getenv("AGW_RELEASE_ARTIFACT_DIR")
	if dir == "" {
		t.Skip("set AGW_RELEASE_ARTIFACT_DIR through scripts/verify-release-archive")
	}
	wantVersion := requireEnv(t, "AGW_EXPECTED_VERSION")
	wantOS := requireEnv(t, "AGW_EXPECTED_GOOS")
	wantArch := requireEnv(t, "AGW_EXPECTED_GOARCH")

	runtimeManifest, err := distribution.CommunityRuntime().Manifest()
	if err != nil {
		t.Fatal(err)
	}
	validationManifest, err := distribution.CommunityValidation().Manifest()
	if err != nil {
		t.Fatal(err)
	}
	assertManifestVersion(t, runtimeManifest, wantVersion)
	assertManifestVersion(t, validationManifest, wantVersion)

	for binary, want := range map[string]distribution.Assembly{
		"agw":    runtimeManifest,
		"agwd":   runtimeManifest,
		"agwctl": validationManifest,
	} {
		t.Run(binary, func(t *testing.T) {
			path := filepath.Join(dir, binary)
			output, err := exec.Command(path, "manifest").Output()
			if err != nil {
				t.Fatalf("run %s manifest: %v", binary, err)
			}
			var got distribution.Assembly
			if err := json.Unmarshal(output, &got); err != nil {
				t.Fatalf("decode %s manifest: %v", binary, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s manifest does not match its Community projection", binary)
			}
		})
	}

	raw, err := os.ReadFile(filepath.Join(dir, "RELEASE-METADATA.json"))
	if err != nil {
		t.Fatal(err)
	}
	var metadata releaseMetadata
	if err := json.Unmarshal(raw, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Version != wantVersion || metadata.Target.OS != wantOS || metadata.Target.Arch != wantArch {
		t.Fatalf("release metadata = %#v", metadata)
	}
	if metadata.SourceCommit == "" || metadata.GoVersion == "" {
		t.Fatalf("release metadata is incomplete: %#v", metadata)
	}
}

func assertManifestVersion(t *testing.T, manifest distribution.Assembly, want string) {
	t.Helper()
	if manifest.Edition.Version != want {
		t.Fatalf("edition version = %q, want %q", manifest.Edition.Version, want)
	}
	for _, module := range manifest.Modules {
		if module.Version != want {
			t.Fatalf("module %q version = %q, want %q", module.ID, module.Version, want)
		}
	}
}

func requireEnv(t *testing.T, name string) string {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		t.Fatalf("%s is required", name)
	}
	return value
}
