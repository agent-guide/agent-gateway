package usage

import (
	"testing"
	"time"
)

func TestResolveBucket(t *testing.T) {
	cases := []struct {
		bucket   string
		wantName string
		wantMS   int64
		wantErr  bool
	}{
		{"minute", "minute", int64(time.Minute / time.Millisecond), false},
		{"minutes", "minute", int64(time.Minute / time.Millisecond), false},
		{"min", "minute", int64(time.Minute / time.Millisecond), false},
		{"m", "minute", int64(time.Minute / time.Millisecond), false},
		{"hour", "hour", int64(time.Hour / time.Millisecond), false},
		{"hours", "hour", int64(time.Hour / time.Millisecond), false},
		{"", "hour", int64(time.Hour / time.Millisecond), false},
		{"DAY", "day", int64(24 * time.Hour / time.Millisecond), false},
		{"days", "day", int64(24 * time.Hour / time.Millisecond), false},
		{"3h", "3h", int64(3 * time.Hour / time.Millisecond), false},
		{"5m", "5m", int64(5 * time.Minute / time.Millisecond), false},
		{"30s", "30s", int64(30 * time.Second / time.Millisecond), false},
		{"90m", "90m", int64(90 * time.Minute / time.Millisecond), false},
		{"2d", "2d", int64(2 * 24 * time.Hour / time.Millisecond), false},
		{"1ms", "1ms", 1, false},
		{"1ns", "", 0, true},
		{"0h", "", 0, true},
		{"-5m", "", 0, true},
		{"week", "", 0, true},
		{"abc", "", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.bucket, func(t *testing.T) {
			name, ms, err := ResolveBucket(tc.bucket)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ResolveBucket(%q) error = nil, want error", tc.bucket)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveBucket(%q) error = %v", tc.bucket, err)
			}
			if name != tc.wantName || ms != tc.wantMS {
				t.Fatalf("ResolveBucket(%q) = (%q, %d), want (%q, %d)", tc.bucket, name, ms, tc.wantName, tc.wantMS)
			}
		})
	}
}
