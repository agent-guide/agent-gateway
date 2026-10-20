package release_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/distribution"
	"github.com/agent-guide/agent-gateway/pkg/gatewaybundle"
)

func TestFirstRunBundleIsLocallyValid(t *testing.T) {
	root := repositoryRoot(t)
	path := filepath.Join(root, "examples", "first-run", "gateway.bundle.yaml")
	bundle, err := gatewaybundle.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := distribution.CommunityValidation().Registry()
	if err != nil {
		t.Fatal(err)
	}
	if err := bundle.ValidateWithRegistry(t.Context(), registry); err != nil {
		t.Fatal(err)
	}
}

func TestPackagedFirstRunBundleMatchesSource(t *testing.T) {
	packaged := os.Getenv("AGW_PACKAGED_FIRST_RUN_DIR")
	if packaged == "" {
		t.Skip("set AGW_PACKAGED_FIRST_RUN_DIR when verifying the release asset")
	}
	root := repositoryRoot(t)
	source, err := os.ReadFile(filepath.Join(root, "examples", "first-run", "gateway.bundle.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	copy, err := os.ReadFile(filepath.Join(packaged, "gateway.bundle.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(source, copy) {
		t.Fatal("packaged first-run Bundle differs from its checked-in source")
	}
}
