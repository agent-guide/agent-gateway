package limiter_test

import (
	"context"
	vk "github.com/agent-guide/agent-gateway/pkg/gateway/virtualkey/limiter"
	"github.com/agent-guide/agent-gateway/pkg/gateway/virtualkey/limiter/contracttest"
	"testing"
)

func TestMemoryBackendContract(t *testing.T) {
	contracttest.Limiter(t, func(context.Context) (vk.LimiterBackend, error) { return vk.NewMemoryBackend(nil), nil })
}

func TestMemoryBackendRefillContract(t *testing.T) {
	contracttest.LimiterRefill(t, func(_ context.Context, clock vk.Clock) (vk.LimiterBackend, error) {
		return vk.NewMemoryBackend(clock), nil
	})
}
