package usage

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ResolveBucket maps a caller-supplied bucket value to its canonical name and
// width in milliseconds. It accepts named buckets with plural/short forms,
// Go-style durations of at least one millisecond, and a trailing day unit such
// as "2d".
func ResolveBucket(bucket string) (string, int64, error) {
	normalized := strings.ToLower(strings.TrimSpace(bucket))
	switch normalized {
	case "minute", "minutes", "min", "m":
		return "minute", int64(time.Minute / time.Millisecond), nil
	case "hour", "hours", "hr", "h", "":
		return "hour", int64(time.Hour / time.Millisecond), nil
	case "day", "days", "d":
		return "day", int64(24 * time.Hour / time.Millisecond), nil
	}
	if d, ok := parseDurationBucket(normalized); ok && d >= time.Millisecond {
		return normalized, int64(d / time.Millisecond), nil
	}
	return "", 0, fmt.Errorf("bucket %q is not supported (use minute, hour, day, or a duration like 3h, 5m, 1d)", bucket)
}

func parseDurationBucket(s string) (time.Duration, bool) {
	if s == "" {
		return 0, false
	}
	if num, found := strings.CutSuffix(s, "d"); found {
		days, err := strconv.ParseFloat(num, 64)
		if err != nil || days <= 0 {
			return 0, false
		}
		d := time.Duration(days * float64(24*time.Hour))
		return d, d > 0
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, false
	}
	return d, true
}
