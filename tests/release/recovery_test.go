package release_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreviewResumeRequiresVerifiedImmutableInputs(t *testing.T) {
	root := repositoryRoot(t)
	commit := strings.Repeat("a", 40)
	for _, scenario := range []string{
		"valid", "wrong-source", "wrong-workflow", "failed-scan", "missing-archive",
		"already-published", "runtime-failure", "stable-tag", "mutable-image",
	} {
		t.Run(scenario, func(t *testing.T) {
			tag := "v0.8.0-preview.1"
			gateway := "ghcr.io/agent-guide/agent-gateway@sha256:" + strings.Repeat("b", 64)
			fixture := "ghcr.io/agent-guide/agent-gateway-a2a-fixture@sha256:" + strings.Repeat("c", 64)
			run := map[string]any{
				"event": "push", "head_branch": tag, "head_sha": commit,
				"status": "completed", "conclusion": "failure", "path": ".github/workflows/release.yml",
			}
			var jobs []map[string]any
			for _, name := range []string{
				"preflight", "images", "source-sbom", "public-images", "image-scan",
				"Image smoke (amd64)", "Image smoke (arm64)",
				"archives (ubuntu-24.04, linux, amd64)", "archives (ubuntu-24.04-arm, linux, arm64)",
				"archives (macos-15-intel, darwin, amd64)", "archives (macos-15, darwin, arm64)",
				"first-run", "publish-release", "post-publish", "promote-latest",
			} {
				job := map[string]any{"name": name, "conclusion": "success"}
				switch name {
				case "first-run":
					step := "Package and verify digest-pinned first-run asset"
					if scenario == "runtime-failure" {
						step = "Execute first-run archive outside the checkout"
					}
					job["conclusion"] = "failure"
					job["steps"] = []map[string]string{{"name": step, "conclusion": "failure"}}
				case "publish-release", "post-publish", "promote-latest":
					job["conclusion"] = "skipped"
				}
				if scenario == "failed-scan" && name == "image-scan" ||
					scenario == "missing-archive" && name == "archives (ubuntu-24.04, linux, amd64)" {
					job["conclusion"] = "failure"
				}
				if scenario == "already-published" && name == "publish-release" {
					job["conclusion"] = "success"
				}
				jobs = append(jobs, job)
			}
			switch scenario {
			case "wrong-source":
				run["head_sha"] = strings.Repeat("d", 40)
			case "wrong-workflow":
				run["path"] = ".github/workflows/ci.yml"
			case "stable-tag":
				tag = "v0.8.0"
				run["head_branch"] = tag
			case "mutable-image":
				gateway = "ghcr.io/agent-guide/agent-gateway:v0.8.0-preview.1"
			}
			dir := t.TempDir()
			for name, value := range map[string]any{"run": run, "jobs": map[string]any{"jobs": jobs}} {
				data, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, name+".json"), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("python3", filepath.Join(root, "scripts", "verify-preview-resume.py"),
				filepath.Join(dir, "run.json"), filepath.Join(dir, "jobs.json"), tag, commit, gateway, fixture)
			logs, err := cmd.CombinedOutput()
			if scenario == "valid" {
				if err != nil || !strings.Contains(string(logs), "source_commit="+commit) {
					t.Fatalf("valid recovery rejected: %v\n%s", err, logs)
				}
			} else if err == nil {
				t.Fatalf("invalid recovery accepted: %s", logs)
			}
		})
	}
}
