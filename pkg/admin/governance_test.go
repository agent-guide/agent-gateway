package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminRejectsUnsupportedAgentBudget(t *testing.T) {
	h := NewHandler(newTestAgentGateway(&testConfigStore{agentStore: newAgentConfigStore()}, nil, nil, nil, nil), nil)
	body := `{"id":"a","name":"Agent","disabled":true,"runtime":{"type":"http","http":{"card_url":"https://example.org/card","protocol":"a2a"}},"policy":{"budget":{}}}`
	request := httptest.NewRequest(http.MethodPost, "/admin/agents", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.handleCreateAgent(rec, request)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "policy.budget is unsupported") {
		t.Fatalf("create = %d %s", rec.Code, rec.Body.String())
	}
}
