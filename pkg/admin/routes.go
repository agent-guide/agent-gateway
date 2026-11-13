package admin

import (
	"net/http"

	"github.com/agent-guide/agent-gateway/internal/httpjson"
)

// Route defines an admin API route.
type Route struct {
	Method  string
	Path    string
	Handler http.HandlerFunc
}

// Routes returns all admin API routes.
func (h *Handler) Routes() []Route {
	return []Route{
		{Method: http.MethodGet, Path: "/admin/manifest", Handler: h.handleManifest},
		{Method: http.MethodGet, Path: "/admin/extensions", Handler: h.handleListExtensions},
		{Method: http.MethodGet, Path: "/admin/extensions/{module_id}", Handler: h.handleGetExtension},
		{Method: http.MethodPut, Path: "/admin/extensions/{module_id}", Handler: h.handlePutExtension},
		{Method: http.MethodDelete, Path: "/admin/extensions/{module_id}", Handler: h.handleDeleteExtension},
		// Health — public
		{Method: http.MethodGet, Path: "/admin/health", Handler: h.handleHealth},

		// LLM provider types
		{Method: http.MethodGet, Path: "/admin/llm/provider_types", Handler: h.handleListProviderTypes},

		// LLM API handler types
		{Method: http.MethodGet, Path: "/admin/llm/api_handler_types", Handler: h.handleListLLMApiHandlerTypes},

		// LLM providers and models
		{Method: http.MethodGet, Path: "/admin/llm/providers", Handler: h.handleListProviders},
		{Method: http.MethodPost, Path: "/admin/llm/providers", Handler: h.handleCreateProvider},
		{Method: http.MethodGet, Path: "/admin/llm/providers/{id}", Handler: h.handleGetProvider},
		{Method: http.MethodPut, Path: "/admin/llm/providers/{id}", Handler: h.handleUpdateProvider},
		{Method: http.MethodPost, Path: "/admin/llm/providers/{id}/enable", Handler: h.handleEnableProvider},
		{Method: http.MethodPost, Path: "/admin/llm/providers/{id}/disable", Handler: h.handleDisableProvider},
		{Method: http.MethodDelete, Path: "/admin/llm/providers/{id}", Handler: h.handleDeleteProvider},
		{Method: http.MethodGet, Path: "/admin/llm/providers/{id}/mutation", Handler: h.handleProviderMutationStatus},
		{Method: http.MethodPost, Path: "/admin/llm/providers/{id}/recover", Handler: h.handleRecoverProvider},
		{Method: http.MethodGet, Path: "/admin/llm/models/providers/{provider_id}/discovered", Handler: h.handleListDiscoveredModels},
		{Method: http.MethodPost, Path: "/admin/llm/models/providers/{provider_id}/refresh", Handler: h.handleRefreshProviderModels},
		{Method: http.MethodGet, Path: "/admin/llm/models/logical", Handler: h.handleLogicalModels},
		{Method: http.MethodPost, Path: "/admin/llm/models/logical", Handler: h.handleLogicalModels},
		{Method: http.MethodGet, Path: "/admin/llm/models/logical/{id}", Handler: h.handleLogicalModels},
		{Method: http.MethodPut, Path: "/admin/llm/models/logical/{id}", Handler: h.handleLogicalModels},
		{Method: http.MethodDelete, Path: "/admin/llm/models/logical/{id}", Handler: h.handleLogicalModels},

		{Method: http.MethodGet, Path: "/admin/llm/models/provider", Handler: h.handleListProviderModels},
		{Method: http.MethodPost, Path: "/admin/llm/models/provider", Handler: h.handleCreateProviderModel},
		{Method: http.MethodGet, Path: "/admin/llm/models/provider/{provider_id}/{upstream_model}", Handler: h.handleGetProviderModel},
		{Method: http.MethodPut, Path: "/admin/llm/models/provider/{provider_id}/{upstream_model}", Handler: h.handleUpdateProviderModel},
		{Method: http.MethodDelete, Path: "/admin/llm/models/provider/{provider_id}/{upstream_model}", Handler: h.handleDeleteProviderModel},
		{Method: http.MethodGet, Path: "/admin/llm/routes", Handler: h.handleListLLMRoutes},
		{Method: http.MethodPost, Path: "/admin/llm/routes", Handler: h.handleCreateLLMRoute},
		{Method: http.MethodGet, Path: "/admin/llm/routes/{id}", Handler: h.handleGetLLMRoute},
		{Method: http.MethodPut, Path: "/admin/llm/routes/{id}", Handler: h.handleUpdateLLMRoute},
		{Method: http.MethodPost, Path: "/admin/llm/routes/{id}/enable", Handler: h.handleEnableLLMRoute},
		{Method: http.MethodPost, Path: "/admin/llm/routes/{id}/disable", Handler: h.handleDisableLLMRoute},
		{Method: http.MethodDelete, Path: "/admin/llm/routes/{id}", Handler: h.handleDeleteLLMRoute},
		{Method: http.MethodGet, Path: "/admin/virtual_keys", Handler: h.handleListVirtualKeys},
		{Method: http.MethodPost, Path: "/admin/virtual_keys", Handler: h.handleCreateVirtualKey},
		{Method: http.MethodGet, Path: "/admin/virtual_keys/{id}", Handler: h.handleGetVirtualKey},
		{Method: http.MethodPut, Path: "/admin/virtual_keys/{id}", Handler: h.handleUpdateVirtualKey},
		{Method: http.MethodPost, Path: "/admin/virtual_keys/{id}/enable", Handler: h.handleEnableVirtualKey},
		{Method: http.MethodPost, Path: "/admin/virtual_keys/{id}/disable", Handler: h.handleDisableVirtualKey},
		{Method: http.MethodDelete, Path: "/admin/virtual_keys/{id}", Handler: h.handleDeleteVirtualKey},
		// Credentials (api_key and externally provisioned oauth_token)
		{Method: http.MethodGet, Path: "/admin/credentials", Handler: h.handleListCredentials},
		{Method: http.MethodPost, Path: "/admin/credentials", Handler: h.handleCreateCredential},
		{Method: http.MethodGet, Path: "/admin/credentials/{credential_id}", Handler: h.handleGetCredential},
		{Method: http.MethodPut, Path: "/admin/credentials/{credential_id}", Handler: h.handleUpdateCredential},
		{Method: http.MethodDelete, Path: "/admin/credentials/{credential_id}", Handler: h.handleDeleteCredential},

		// MCP
		{Method: http.MethodGet, Path: "/admin/connectors/{id}/state", Handler: h.handleConnectorState},
		{Method: http.MethodGet, Path: "/admin/connectors/{id}/mutation", Handler: h.handleConnectorMutation},
		{Method: http.MethodPost, Path: "/admin/connectors/{id}/recover", Handler: h.handleConnectorRecover},
		{Method: http.MethodGet, Path: "/admin/connectors", Handler: h.handlerListConnectors},
		{Method: http.MethodPost, Path: "/admin/connectors", Handler: h.handlerAddConnector},
		{Method: http.MethodGet, Path: "/admin/connectors/{id}", Handler: h.handlerGetConnector},
		{Method: http.MethodPut, Path: "/admin/connectors/{id}", Handler: h.handlerUpdateConnector},
		{Method: http.MethodDelete, Path: "/admin/connectors/{id}", Handler: h.handlerRemoveConnector},
		{Method: http.MethodGet, Path: "/admin/connectors/{id}/capabilities", Handler: h.handleGetConnectorCapabilities},
		{Method: http.MethodGet, Path: "/admin/mcp/routes", Handler: h.handleListMCPRoutes},
		{Method: http.MethodPost, Path: "/admin/mcp/routes", Handler: h.handleCreateMCPRoute},
		{Method: http.MethodGet, Path: "/admin/mcp/routes/{id}", Handler: h.handleGetMCPRoute},
		{Method: http.MethodPut, Path: "/admin/mcp/routes/{id}", Handler: h.handleUpdateMCPRoute},
		{Method: http.MethodDelete, Path: "/admin/mcp/routes/{id}", Handler: h.handleDeleteMCPRoute},
		{Method: http.MethodGet, Path: "/admin/mcp/runtime", Handler: h.handleGetMCPDispatcherRuntime},
		{Method: http.MethodGet, Path: "/admin/mcp/runtime/inflight", Handler: h.handleListMCPDispatcherInFlight},
		{Method: http.MethodGet, Path: "/admin/mcp/runtime/progress", Handler: h.handleListMCPDispatcherProgress},
		{Method: http.MethodGet, Path: "/admin/mcp/runtime/history", Handler: h.handleListMCPDispatcherHistory},
		{Method: http.MethodGet, Path: "/admin/connectors/{id}/sessions", Handler: h.handleListConnectorSessions},
		{Method: http.MethodGet, Path: "/admin/connectors/{id}/tools", Handler: h.handleListMCPTools},
		{Method: http.MethodPost, Path: "/admin/connectors/{id}/tools/call", Handler: h.handleCallMCPTool},
		{Method: http.MethodGet, Path: "/admin/connectors/{id}/resources", Handler: h.handleListMCPResources},
		{Method: http.MethodGet, Path: "/admin/connectors/{id}/resource-templates", Handler: h.handleListMCPResourceTemplates},
		{Method: http.MethodPost, Path: "/admin/connectors/{id}/resources/read", Handler: h.handleReadMCPResource},
		{Method: http.MethodGet, Path: "/admin/connectors/{id}/prompts", Handler: h.handleListMCPPrompts},
		{Method: http.MethodPost, Path: "/admin/connectors/{id}/prompts/get", Handler: h.handleGetMCPPrompt},

		// Runtime-specific ACP operator details remain under /admin/acp/runtime.
		{Method: http.MethodGet, Path: "/admin/acp/runtime", Handler: h.handleGetACPRuntime},
		{Method: http.MethodGet, Path: "/admin/acp/runtime/inflight", Handler: h.handleListACPInFlight},
		{Method: http.MethodDelete, Path: "/admin/acp/runtime/agents/{agent_id}/threads/{thread_id}", Handler: h.handleCloseACPThread},

		// Runtime-specific builtin operator details remain under /admin/builtin/runtime.
		{Method: http.MethodGet, Path: "/admin/builtin/runtime", Handler: h.handleGetBuiltinRuntime},
		{Method: http.MethodGet, Path: "/admin/builtin/runtime/inflight", Handler: h.handleListBuiltinInFlight},

		// Memory
		{Method: http.MethodGet, Path: "/admin/memory/config", Handler: h.handleGetMemoryConfig},
		{Method: http.MethodPut, Path: "/admin/memory/config", Handler: h.handleSetMemoryConfig},
		{Method: http.MethodGet, Path: "/admin/memory/search", Handler: h.handleSearchMemory},

		// Agents
		{Method: http.MethodGet, Path: "/admin/agents", Handler: h.handleListAgents},
		{Method: http.MethodPost, Path: "/admin/agents", Handler: h.handleCreateAgent},
		{Method: http.MethodGet, Path: "/admin/agents/{id}", Handler: h.handleGetAgent},
		{Method: http.MethodPut, Path: "/admin/agents/{id}", Handler: h.handleUpdateAgent},
		{Method: http.MethodDelete, Path: "/admin/agents/{id}", Handler: h.handleDeleteAgent},
		{Method: http.MethodGet, Path: "/admin/agents/{id}/mutation", Handler: h.handleAgentMutationStatus},
		{Method: http.MethodPost, Path: "/admin/agents/{id}/recover", Handler: h.handleRecoverAgent},
		{Method: http.MethodGet, Path: "/admin/agents/routes", Handler: h.handleListAgentRoutes},
		{Method: http.MethodPost, Path: "/admin/agents/routes", Handler: h.handleCreateAgentRoute},
		{Method: http.MethodGet, Path: "/admin/agents/routes/{id}", Handler: h.handleGetAgentRoute},
		{Method: http.MethodPut, Path: "/admin/agents/routes/{id}", Handler: h.handleUpdateAgentRoute},
		{Method: http.MethodDelete, Path: "/admin/agents/routes/{id}", Handler: h.handleDeleteAgentRoute},
		{Method: http.MethodGet, Path: "/admin/agents/{id}/workspace", Handler: h.handleGetAgentWorkspace},
		{Method: http.MethodGet, Path: "/admin/agents/{id}/activity", Handler: h.handleGetAgentActivity},
		{Method: http.MethodGet, Path: "/admin/agents/{id}/usage", Handler: h.handleGetAgentUsage},
		{Method: http.MethodGet, Path: "/admin/agents/{id}/interactions", Handler: h.handleGetAgentInteractions},
		{Method: http.MethodGet, Path: "/admin/agents/{id}/resources", Handler: h.handleGetAgentResources},
		{Method: http.MethodPut, Path: "/admin/agents/{id}/resources", Handler: h.handleUpdateAgentResources},
		{Method: http.MethodGet, Path: "/admin/agents/{id}/health", Handler: h.handleGetAgentHealth},
		{Method: http.MethodGet, Path: "/admin/agents/{id}/capabilities", Handler: h.handleGetAgentCapabilities},
		{Method: http.MethodGet, Path: "/admin/agents/{id}/runs", Handler: h.handleListAgentRuns},
		{Method: http.MethodDelete, Path: "/admin/agents/{id}/runs/{run_id}", Handler: h.handleCancelAgentRun},
		{Method: http.MethodGet, Path: "/admin/agents/{id}/permissions", Handler: h.handleListAgentPermissions},
		{Method: http.MethodPost, Path: "/admin/agents/{id}/permissions/{request_id}", Handler: h.handleResolveAgentPermission},
		{Method: http.MethodGet, Path: "/admin/agents/{id}/sessions", Handler: h.handleListAgentSessions},
		{Method: http.MethodGet, Path: "/admin/agents/{id}/sessions/{session_id}/transcript", Handler: h.handleGetAgentTranscript},

		// Metrics
		{Method: http.MethodGet, Path: "/admin/metrics", Handler: h.handleMetrics},
		{Method: http.MethodGet, Path: "/admin/metrics/prometheus", Handler: h.handlePrometheusMetrics},
		{Method: http.MethodGet, Path: "/admin/metrics/llm/events", Handler: h.handleListLLMMetricsEvents},
		{Method: http.MethodGet, Path: "/admin/metrics/llm/timeseries", Handler: h.handleLLMMetricsTimeseries},
		{Method: http.MethodGet, Path: "/admin/metrics/llm/breakdown", Handler: h.handleLLMMetricsBreakdown},
		{Method: http.MethodGet, Path: "/admin/metrics/mcp/events", Handler: h.handleListMCPMetricsEvents},
		{Method: http.MethodGet, Path: "/admin/metrics/mcp/timeseries", Handler: h.handleMCPMetricsTimeseries},
		{Method: http.MethodGet, Path: "/admin/metrics/mcp/breakdown", Handler: h.handleMCPMetricsBreakdown},
		{Method: http.MethodGet, Path: "/admin/metrics/mcp/tools/summary", Handler: h.handleMCPMetricsToolsSummary},
		{Method: http.MethodGet, Path: "/admin/metrics/acp/events", Handler: h.handleListACPMetricsEvents},
		{Method: http.MethodGet, Path: "/admin/metrics/acp/timeseries", Handler: h.handleACPMetricsTimeseries},
		{Method: http.MethodGet, Path: "/admin/metrics/acp/breakdown", Handler: h.handleACPMetricsBreakdown},
		{Method: http.MethodGet, Path: "/admin/metrics/acp/summary", Handler: h.handleACPMetricsSummary},
		{Method: http.MethodGet, Path: "/admin/metrics/interactions", Handler: h.handleListMetricInteractions},
		{Method: http.MethodGet, Path: "/admin/metrics/interactions/timeseries", Handler: h.handleMetricInteractionsTimeseries},
		{Method: http.MethodGet, Path: "/admin/metrics/interactions/summary", Handler: h.handleMetricInteractionsSummary},
	}
}

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	if h.extensions != nil {
		if err := h.extensions.Health(r.Context()); err != nil {
			extensionError(w, err)
			return
		}
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]string{"status": "ok"})
}
