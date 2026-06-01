package provider

import "testing"

func TestCompactModeFromOptions(t *testing.T) {
	cases := []struct {
		name string
		opts map[string]any
		want CompactMode
		err  bool
	}{
		{name: "missing", opts: nil, want: CompactModeNone},
		{name: "empty", opts: map[string]any{"compact": ""}, want: CompactModeNone},
		{name: "none", opts: map[string]any{"compact": "none"}, want: CompactModeNone},
		{name: "cc", opts: map[string]any{"compact": "cc"}, want: CompactModeCC},
		{name: "codex", opts: map[string]any{"compact": "codex"}, want: CompactModeCodex},
		{name: "trim case", opts: map[string]any{"compact": " Codex "}, want: CompactModeCodex},
		{name: "string garbage", opts: map[string]any{"compact": "yes-ish"}, want: CompactModeNone, err: true},
		{name: "wrong type", opts: map[string]any{"compact": true}, want: CompactModeNone, err: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CompactModeFromOptions(tc.opts)
			if (err != nil) != tc.err {
				t.Fatalf("CompactModeFromOptions error = %v, want err=%v", err, tc.err)
			}
			if got != tc.want {
				t.Fatalf("CompactModeFromOptions = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestStripCCUnsupportedChatFields(t *testing.T) {
	fields := map[string]any{
		"metadata":         map[string]any{"user_id": "abc"},
		"user":             "user-1",
		"reasoning_effort": "high",
	}
	StripCCUnsupportedChatFields(fields)

	if _, ok := fields["metadata"]; ok {
		t.Fatalf("metadata should be removed")
	}
	if _, ok := fields["user"]; ok {
		t.Fatalf("user should be removed")
	}
	if fields["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort = %#v, want high", fields["reasoning_effort"])
	}

	// Nil map must be a safe no-op.
	StripCCUnsupportedChatFields(nil)
}
