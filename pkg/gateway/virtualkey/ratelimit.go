package virtualkey

import (
	"fmt"
	"time"
)

type RateLimitDimension string

const (
	RateLimitDimensionLLM RateLimitDimension = "llm"
	RateLimitDimensionMCP RateLimitDimension = "mcp"
	// RateLimitDimensionAgent is the unified kind=agent ingress bucket.
	RateLimitDimensionAgent RateLimitDimension = "agent"
)

type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

type Admission struct {
	BackendID         string
	BackendType       string
	Degraded          bool
	Allowed           bool
	RetryAfterSeconds int
	RequestsPerMinute int
	Burst             int
}

type limiterKey struct {
	virtualKeyID string
	dimension    RateLimitDimension
}

func rateLimitForDimension(limits *VirtualKeyRateLimits, dimension RateLimitDimension) (*RateLimit, error) {
	switch dimension {
	case RateLimitDimensionLLM, RateLimitDimensionMCP, RateLimitDimensionAgent:
	default:
		return nil, fmt.Errorf("unsupported rate limit dimension")
	}
	if limits == nil {
		return nil, nil
	}
	switch dimension {
	case RateLimitDimensionLLM:
		return limits.LLM, nil
	case RateLimitDimensionMCP:
		return limits.MCP, nil
	case RateLimitDimensionAgent:
		return limits.Agent, nil
	default:
		return nil, fmt.Errorf("unsupported rate limit dimension %q", dimension)
	}
}
