package agentspi

import (
	"os"
	"testing"
)

func TestMergeEnvReturnsNilForEmptyOverrides(t *testing.T) {
	if got := MergeEnv(nil); got != nil {
		t.Fatalf("MergeEnv(nil) = %v, want nil", got)
	}
	if got := MergeEnv(map[string]string{}); got != nil {
		t.Fatalf("MergeEnv(empty) = %v, want nil", got)
	}
}

func TestMergeEnvOverridesAndAdds(t *testing.T) {
	t.Setenv("ACP_TEST_INHERITED", "inherited")
	t.Setenv("ACP_TEST_OVERRIDE", "old")

	out := MergeEnv(map[string]string{
		"ACP_TEST_OVERRIDE": "new",
		"ACP_TEST_NEW":      "added",
		"  ":                "dropped",
	})

	got := map[string]string{}
	for _, kv := range out {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				got[kv[:i]] = kv[i+1:]
				break
			}
		}
	}

	if got["ACP_TEST_INHERITED"] != "inherited" {
		t.Errorf("inherited key lost: %q", got["ACP_TEST_INHERITED"])
	}
	if got["ACP_TEST_OVERRIDE"] != "new" {
		t.Errorf("override not applied: %q", got["ACP_TEST_OVERRIDE"])
	}
	if got["ACP_TEST_NEW"] != "added" {
		t.Errorf("new key not added: %q", got["ACP_TEST_NEW"])
	}
	if _, ok := got[""]; ok {
		t.Error("empty key was not dropped")
	}
	// PATH from the real environment should survive the merge.
	if _, ok := os.LookupEnv("PATH"); ok {
		if got["PATH"] == "" {
			t.Error("PATH not inherited into merged env")
		}
	}
}
