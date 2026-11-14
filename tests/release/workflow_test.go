package release_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type workflowNeeds []string

func (needs *workflowNeeds) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		*needs = []string{node.Value}
		return nil
	}
	var values []string
	if err := node.Decode(&values); err != nil {
		return err
	}
	*needs = values
	return nil
}

func TestReleaseWorkflowPreviewPolicy(t *testing.T) {
	var workflow struct {
		On struct {
			Push struct {
				Tags []string `yaml:"tags"`
			} `yaml:"push"`
		} `yaml:"on"`
		Jobs map[string]struct {
			Needs   workflowNeeds     `yaml:"needs"`
			If      string            `yaml:"if"`
			Outputs map[string]string `yaml:"outputs"`
			Steps   []struct {
				ID   string            `yaml:"id"`
				Run  string            `yaml:"run"`
				Uses string            `yaml:"uses"`
				With map[string]string `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	root := repositoryRoot(t)
	raw := readFile(t, filepath.Join(root, ".github", "workflows", "release.yml"))
	if err := yaml.Unmarshal([]byte(raw), &workflow); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(workflow.On.Push.Tags, "v[0-9]*") {
		t.Fatal("release trigger must accept stable and preview version tags")
	}
	preflight := workflow.Jobs["preflight"]
	if preflight.Outputs["prerelease"] != "${{ steps.version.outputs.prerelease }}" {
		t.Fatal("preflight must expose the version classification")
	}
	var classify string
	for _, step := range preflight.Steps {
		if step.ID == "version" {
			var found bool
			classify, _, found = strings.Cut(step.Run, "test -s ")
			if !found || !strings.Contains(step.Run, `test -s "docs/releases/$GITHUB_REF_NAME.md"`) {
				t.Fatal("version validation must require matching release notes before building")
			}
		}
	}
	if classify == "" {
		t.Fatal("missing release version validation")
	}
	for _, tc := range []struct {
		tag        string
		prerelease string
	}{
		{"v0.8.0", "false"},
		{"v0.8.1", "false"},
		{"v0.8.0-preview.1", "true"},
		{"v0.8.0-preview.12", "true"},
		{"0.8.0-preview.1", ""},
		{"v0.8.0-preview.0", ""},
		{"v0.8.0-preview.01", ""},
		{"v00.8.0", ""},
		{"v0.8.0-alpha.1", ""},
		{"v0.8.0-preview.1-extra", ""},
		{"v0.8.0/../../invalid", ""},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "outputs")
			cmd := exec.Command("bash", "-c", classify)
			cmd.Env = append(os.Environ(), "GITHUB_REF_NAME="+tc.tag, "GITHUB_OUTPUT="+output)
			logs, err := cmd.CombinedOutput()
			if tc.prerelease == "" {
				if err == nil {
					t.Fatalf("invalid tag accepted: %s", logs)
				}
				return
			}
			if err != nil {
				t.Fatalf("valid tag rejected: %v\n%s", err, logs)
			}
			want := "version=" + strings.TrimPrefix(tc.tag, "v") + "\nprerelease=" + tc.prerelease + "\n"
			if got := readFile(t, output); got != want {
				t.Fatalf("classification = %q, want %q", got, want)
			}
		})
	}
	publish := workflow.Jobs["publish-release"]
	if !slices.Contains(publish.Needs, "preflight") {
		t.Fatal("publication must depend on version validation")
	}
	foundPublish := false
	for _, step := range publish.Steps {
		if strings.HasPrefix(step.Uses, "softprops/action-gh-release@") {
			foundPublish = true
			for key, want := range map[string]string{
				"body_path":   "docs/releases/${{ github.ref_name }}.md",
				"prerelease":  "${{ needs.preflight.outputs.prerelease == 'true' }}",
				"make_latest": "${{ needs.preflight.outputs.prerelease == 'true' && 'false' || 'true' }}",
			} {
				if step.With[key] != want {
					t.Errorf("release %s = %q, want %q", key, step.With[key], want)
				}
			}
		}
	}
	if !foundPublish {
		t.Fatal("missing GitHub release publication")
	}
	promote := workflow.Jobs["promote-latest"]
	if promote.If != "needs.preflight.outputs.prerelease == 'false'" ||
		!slices.Contains(promote.Needs, "preflight") || !slices.Contains(promote.Needs, "post-publish") {
		t.Fatal("latest promotion must require a stable release and successful public artifact verification")
	}
}

func TestAdoptionRejectsInvalidExample(t *testing.T) {
	root := repositoryRoot(t)
	binDir := t.TempDir()
	fakeCLI := filepath.Join(binDir, "agwctl")
	if err := os.WriteFile(fakeCLI, []byte("#!/bin/sh\necho 'invalid-example-sentinel' >&2\nexit 23\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "go"), []byte("#!/bin/sh\necho 'unexpected-later-gate' >&2\nexit 99\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", filepath.Join(root, "scripts", "verify-adoption"))
	cmd.Env = append(os.Environ(), "AGWCTL_BIN="+fakeCLI, "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "invalid-example-sentinel") || strings.Contains(string(out), "unexpected-later-gate") {
		t.Fatalf("invalid example did not fail the adoption gate: %v\n%s", err, out)
	}
}
