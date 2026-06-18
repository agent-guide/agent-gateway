package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProtectedRoutesDelegateAuthenticationToMountLayer(t *testing.T) {
	handler := NewHandler(newTestAgentGateway(&testConfigStore{}, nil, nil, nil, nil), nil)

	req := httptest.NewRequest(http.MethodGet, "/admin/providers", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestAuthSessionEndpointsAreNotRegistered(t *testing.T) {
	handler := NewHandler(newTestAgentGateway(&testConfigStore{}, nil, nil, nil, nil), nil)

	req := httptest.NewRequest(http.MethodPost, "/admin/auth/login", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unexpected status: got %d want %d", rec.Code, http.StatusNotFound)
	}
}
