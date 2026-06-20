package sqlite

import (
	"strings"
	"time"

	"gorm.io/gorm"
)

func (s *SQLiteConfigStoreCreator) UsageDB() *gorm.DB {
	if s == nil {
		return nil
	}
	return s.db
}

func MigrateUsageTables(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS llm_usage_events (
			event_id TEXT PRIMARY KEY, trace_id TEXT, span_id TEXT NOT NULL, parent_span_id TEXT,
			agent_depth INTEGER NOT NULL DEFAULT 0, started_at INTEGER NOT NULL, finished_at INTEGER NOT NULL,
			route_id TEXT, route_kind TEXT NOT NULL DEFAULT 'llm', route_protocol TEXT, virtual_key_id TEXT,
			success INTEGER NOT NULL DEFAULT 0, status_code INTEGER, error_type TEXT, latency_ms INTEGER,
			llm_api TEXT, api_operation TEXT, provider_id TEXT, provider_type TEXT,
			logical_model TEXT, upstream_model TEXT, credential_source TEXT, credential_id TEXT,
			stream INTEGER NOT NULL DEFAULT 0, input_tokens INTEGER, output_tokens INTEGER, total_tokens INTEGER,
			usage_finalized INTEGER NOT NULL DEFAULT 1, request_tool_count INTEGER NOT NULL DEFAULT 0,
			request_tool_names TEXT, tool_call_count INTEGER NOT NULL DEFAULT 0, tool_names TEXT,
			agent_id TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_llm_events_started ON llm_usage_events (started_at)`,
		`CREATE INDEX IF NOT EXISTS idx_llm_events_route ON llm_usage_events (route_id, started_at)`,
		`CREATE INDEX IF NOT EXISTS idx_llm_events_vkey ON llm_usage_events (virtual_key_id, started_at)`,
		`CREATE INDEX IF NOT EXISTS idx_llm_events_trace ON llm_usage_events (trace_id, started_at) WHERE trace_id IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_llm_events_tool_use ON llm_usage_events (tool_call_count, started_at) WHERE tool_call_count > 0`,
		`CREATE TABLE IF NOT EXISTS mcp_usage_events (
			event_id TEXT PRIMARY KEY, trace_id TEXT, span_id TEXT NOT NULL, parent_span_id TEXT,
			agent_depth INTEGER NOT NULL DEFAULT 0, started_at INTEGER NOT NULL, finished_at INTEGER NOT NULL,
			route_id TEXT, route_kind TEXT NOT NULL DEFAULT 'mcp', route_protocol TEXT, virtual_key_id TEXT,
			success INTEGER NOT NULL DEFAULT 0, status_code INTEGER, error_type TEXT, latency_ms INTEGER,
			request_id TEXT, service_id TEXT, method TEXT, tool_name TEXT, presented_tool_name TEXT,
			executed_tool_name TEXT, execution_mode TEXT, policy_action TEXT, resource_uri TEXT, prompt_name TEXT,
			completion_ref_type TEXT, completion_argument TEXT, arg_count INTEGER, result_status TEXT,
			cancelled INTEGER NOT NULL DEFAULT 0, tool_args_json TEXT,
			agent_id TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_mcp_events_started ON mcp_usage_events (started_at)`,
		`CREATE INDEX IF NOT EXISTS idx_mcp_events_route ON mcp_usage_events (route_id, started_at)`,
		`CREATE INDEX IF NOT EXISTS idx_mcp_events_request ON mcp_usage_events (route_id, request_id, started_at)`,
		`CREATE INDEX IF NOT EXISTS idx_mcp_events_trace ON mcp_usage_events (trace_id, started_at) WHERE trace_id IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_mcp_events_tool ON mcp_usage_events (tool_name, started_at) WHERE tool_name IS NOT NULL`,
		`CREATE TABLE IF NOT EXISTS acp_usage_events (
			event_id TEXT PRIMARY KEY, trace_id TEXT, span_id TEXT NOT NULL, parent_span_id TEXT,
			agent_depth INTEGER NOT NULL DEFAULT 0, started_at INTEGER NOT NULL, finished_at INTEGER NOT NULL,
			route_id TEXT, route_kind TEXT NOT NULL DEFAULT 'acp', route_protocol TEXT, virtual_key_id TEXT,
			success INTEGER NOT NULL DEFAULT 0, status_code INTEGER, error_type TEXT, latency_ms INTEGER,
			service_id TEXT, agent_type TEXT, operation TEXT, thread_id TEXT, session_id TEXT,
			permission_request_id TEXT, fresh_session INTEGER, event_counts_json TEXT, usage_json TEXT,
			result_status TEXT, agent_id TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_acp_events_started ON acp_usage_events (started_at)`,
		`CREATE INDEX IF NOT EXISTS idx_acp_events_route ON acp_usage_events (route_id, started_at)`,
		`CREATE INDEX IF NOT EXISTS idx_acp_events_service ON acp_usage_events (service_id, started_at)`,
		`CREATE INDEX IF NOT EXISTS idx_acp_events_trace ON acp_usage_events (trace_id, started_at) WHERE trace_id IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_acp_events_thread ON acp_usage_events (thread_id, started_at) WHERE thread_id IS NOT NULL`,
	}
	for _, stmt := range stmts {
		if err := db.Exec(stmt).Error; err != nil {
			return err
		}
	}
	// agent_id is an additive attribution tag. Existing databases created before
	// agents shipped lack the column; add it idempotently, ignoring the
	// duplicate-column error on databases that already have it.
	for _, table := range []string{"llm_usage_events", "mcp_usage_events", "acp_usage_events"} {
		if err := db.Exec("ALTER TABLE " + table + " ADD COLUMN agent_id TEXT").Error; err != nil {
			if !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
				return err
			}
		}
	}
	agentIndexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_llm_events_agent ON llm_usage_events (agent_id, started_at) WHERE agent_id IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_mcp_events_agent ON mcp_usage_events (agent_id, started_at) WHERE agent_id IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_acp_events_agent ON acp_usage_events (agent_id, started_at) WHERE agent_id IS NOT NULL`,
	}
	for _, stmt := range agentIndexes {
		if err := db.Exec(stmt).Error; err != nil {
			return err
		}
	}
	return nil
}

func CleanupUsageEvents(db *gorm.DB, retention time.Duration) error {
	if db == nil || retention <= 0 {
		return nil
	}
	cutoff := time.Now().UTC().Add(-retention).UnixMilli()
	for _, table := range []string{"llm_usage_events", "mcp_usage_events", "acp_usage_events"} {
		if err := db.Exec("DELETE FROM "+table+" WHERE started_at < ?", cutoff).Error; err != nil {
			return err
		}
	}
	return nil
}
