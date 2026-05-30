package provider

import "testing"

func TestBoolOption(t *testing.T) {
	cases := []struct {
		name string
		opts map[string]any
		want bool
	}{
		{name: "missing", opts: nil, want: false},
		{name: "native true", opts: map[string]any{"cc_compat": true}, want: true},
		{name: "native false", opts: map[string]any{"cc_compat": false}, want: false},
		{name: "string true", opts: map[string]any{"cc_compat": "true"}, want: true},
		{name: "string 1", opts: map[string]any{"cc_compat": " 1 "}, want: true},
		{name: "string garbage", opts: map[string]any{"cc_compat": "yes-ish"}, want: false},
		{name: "wrong type", opts: map[string]any{"cc_compat": 1}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := ProviderConfig{Options: tc.opts}
			if got := cfg.BoolOption("cc_compat"); got != tc.want {
				t.Fatalf("BoolOption = %v, want %v", got, tc.want)
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
