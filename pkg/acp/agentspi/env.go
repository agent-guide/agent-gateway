package agentspi

import (
	"os"
	"sort"
	"strings"
)

// MergeEnv returns the current process environment with the given overrides
// applied on top: keys present in overrides replace any inherited value, and
// new keys are appended. The result is sorted for deterministic output. An
// empty overrides map returns nil so the transport falls back to os.Environ()
// unchanged.
func MergeEnv(overrides map[string]string) []string {
	if len(overrides) == 0 {
		return nil
	}
	merged := make(map[string]string)
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			merged[kv[:i]] = kv[i+1:]
		}
	}
	for k, v := range overrides {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		merged[k] = v
	}
	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(merged))
	for _, k := range keys {
		out = append(out, k+"="+merged[k])
	}
	return out
}
