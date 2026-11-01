package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/agent-guide/agent-gateway/internal/observability/usage"
	agentpkg "github.com/agent-guide/agent-gateway/pkg/agent"
)

// Only authenticated dispatcher dimensions establish a data-plane owner. The
// client cannot turn a thread/session/request identifier into authority. Admin
// calls have no route dimension and retain operator inspection authority.
func agentClientScope(ctx context.Context, a agentpkg.Agent) string {
	dims, _ := usage.DimensionsFromContext(ctx)
	if dims.RouteID == "" {
		return ""
	}
	raw, _ := json.Marshal([]any{dims.RouteID, dims.VirtualKeyID, a.ID, a.Runtime})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
