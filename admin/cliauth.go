package admin

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/agent-guide/caddy-agent-gateway/internal/utils"
	"github.com/agent-guide/caddy-agent-gateway/llm/cliauth/manager"
	"go.uber.org/zap"
)

// cliAuthStatus tracks the state of an async CLI auth flow.
type cliAuthStatus struct {
	Status       string     `json:"status"` // "running", "succeeded", "failed"
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	Error        string     `json:"error,omitempty"`
	CredentialID string     `json:"credential_id,omitempty"`
}

func (h *Handler) handleListCLIAuthCredentials(w http.ResponseWriter, r *http.Request) {
	if h.cliauthManager == nil {
		_ = utils.WriteError(w, http.StatusServiceUnavailable, "auth manager not configured")
		return
	}

	provider := r.URL.Query().Get("provider")
	items := h.cliauthManager.ListCredential(provider)
	_ = utils.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) handleGetCLIAuthCredential(w http.ResponseWriter, r *http.Request) {
	if h.cliauthManager == nil {
		_ = utils.WriteError(w, http.StatusServiceUnavailable, "auth manager not configured")
		return
	}

	id := strings.TrimSpace(r.PathValue("credential_id"))
	if id == "" {
		_ = utils.WriteError(w, http.StatusBadRequest, "credential_id is required")
		return
	}

	item := h.cliauthManager.GetCredential(id)
	if item == nil {
		_ = utils.WriteError(w, http.StatusNotFound, "credential not found")
		return
	}
	_ = utils.WriteJSON(w, http.StatusOK, item)
}

func (h *Handler) handleDeleteCLIAuthCredential(w http.ResponseWriter, r *http.Request) {
	if h.cliauthManager == nil {
		_ = utils.WriteError(w, http.StatusServiceUnavailable, "auth manager not configured")
		return
	}

	id := strings.TrimSpace(r.PathValue("credential_id"))
	if id == "" {
		_ = utils.WriteError(w, http.StatusBadRequest, "credential_id is required")
		return
	}
	if err := h.cliauthManager.DeregisterCredential(r.Context(), id); err != nil {
		_ = utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = utils.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted", "credential_id": id})
}

func (h *Handler) handleListCLIAuthAuthenticators(w http.ResponseWriter, r *http.Request) {
	if h.cliauthManager == nil {
		_ = utils.WriteError(w, http.StatusServiceUnavailable, "auth manager not configured")
		return
	}

	_ = utils.WriteJSON(w, http.StatusOK, map[string]any{"items": h.cliauthManager.ListAuthenticatorStates()})
}

func (h *Handler) handleEnableCLIAuthAuthenticator(w http.ResponseWriter, r *http.Request) {
	if h.cliauthManager == nil {
		_ = utils.WriteError(w, http.StatusServiceUnavailable, "auth manager not configured")
		return
	}

	name := strings.ToLower(strings.TrimSpace(r.PathValue("authenticator_name")))
	if name == "" {
		_ = utils.WriteError(w, http.StatusBadRequest, "authenticator_name is required")
		return
	}

	_, existed := h.cliauthManager.AuthenticatorState(name)
	state, err := h.cliauthManager.EnableAuthenticator(name)
	if err != nil {
		_ = utils.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	status := http.StatusCreated
	if existed {
		status = http.StatusOK
	}
	_ = utils.WriteJSON(w, status, map[string]any{"status": "enabled", "authenticator": state})
}

func (h *Handler) handleDisableCLIAuthAuthenticator(w http.ResponseWriter, r *http.Request) {
	if h.cliauthManager == nil {
		_ = utils.WriteError(w, http.StatusServiceUnavailable, "auth manager not configured")
		return
	}

	name := strings.ToLower(strings.TrimSpace(r.PathValue("authenticator_name")))
	if name == "" {
		_ = utils.WriteError(w, http.StatusBadRequest, "authenticator_name is required")
		return
	}
	if err := h.cliauthManager.DisableAuthenticator(name); err != nil {
		if errors.Is(err, manager.ErrAuthenticatorReadOnly) {
			_ = utils.WriteError(w, http.StatusConflict, err.Error())
			return
		}
		_ = utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = utils.WriteJSON(w, http.StatusOK, map[string]string{"status": "disabled", "authenticator_name": name})
}

// handleStartCLIAuthAuthenticatorLogin triggers a provider-specific CLI auth flow asynchronously.
// POST /admin/cliauth/authenticators/{authenticator_name}/login
//
// The handler returns 202 Accepted immediately. In the background it invokes
// the registered Authenticator's Login method. On success the returned
// credential is registered with the auth manager.
func (h *Handler) handleStartCLIAuthAuthenticatorLogin(w http.ResponseWriter, r *http.Request) {
	if h.cliauthManager == nil {
		_ = utils.WriteError(w, http.StatusServiceUnavailable, "auth manager not configured")
		return
	}

	requestedName := strings.ToLower(strings.TrimSpace(r.PathValue("authenticator_name")))
	if requestedName == "" {
		_ = utils.WriteError(w, http.StatusBadRequest, "authenticator_name is required")
		return
	}

	auth, ok := h.cliauthManager.GetAuthenticator(requestedName)
	if !ok {
		_ = utils.WriteError(w, http.StatusNotFound, requestedName+" authenticator not enabled")
		return
	}

	status := &cliAuthStatus{
		Status:    "running",
		StartedAt: time.Now().UTC(),
	}
	h.storeCLIAuthStatus(requestedName, status)

	// Run the login flow in the background so the HTTP call returns immediately.
	go func() {
		ctx := context.Background()
		cred, err := auth.Login(ctx)
		finished := cliAuthStatusSnapshot(status)
		now := time.Now().UTC()
		finished.FinishedAt = &now
		if err != nil {
			finished.Status = "failed"
			finished.Error = err.Error()
			h.storeCLIAuthStatus(requestedName, &finished)
			h.logger.Error("cli login failed", zap.String("cliname", requestedName), zap.Error(err))
			return
		}
		if regErr := h.cliauthManager.RegisterCredential(ctx, cred); regErr != nil {
			finished.Status = "failed"
			finished.Error = regErr.Error()
			h.storeCLIAuthStatus(requestedName, &finished)
			h.logger.Error("cli login: register credential failed",
				zap.String("cliname", requestedName), zap.Error(regErr))
			return
		}
		finished.Status = "succeeded"
		finished.CredentialID = cred.ID
		h.storeCLIAuthStatus(requestedName, &finished)
		h.logger.Info("cli login succeeded",
			zap.String("cliname", requestedName),
			zap.String("credential_id", cred.ID))
	}()

	_ = utils.WriteJSON(w, http.StatusAccepted, map[string]string{
		"status":             "login_started",
		"authenticator_name": requestedName,
		"message":            "CLI login initiated. Complete the provider authentication flow on the server to register the credential.",
	})
}

// handleGetCLIAuthAuthenticatorLoginStatus returns the current status of an async CLI auth flow.
// GET /admin/cliauth/authenticators/{authenticator_name}/login/status
func (h *Handler) handleGetCLIAuthAuthenticatorLoginStatus(w http.ResponseWriter, r *http.Request) {
	cliname := strings.ToLower(strings.TrimSpace(r.PathValue("authenticator_name")))
	if cliname == "" {
		_ = utils.WriteError(w, http.StatusBadRequest, "authenticator_name is required")
		return
	}

	val, ok := h.cliAuthSessions.Load(cliname)
	if !ok {
		_ = utils.WriteError(w, http.StatusNotFound, "no login session found for "+cliname)
		return
	}
	status, ok := val.(cliAuthStatus)
	if !ok {
		_ = utils.WriteError(w, http.StatusInternalServerError, "invalid login session state")
		return
	}
	_ = utils.WriteJSON(w, http.StatusOK, status)
}

func (h *Handler) storeCLIAuthStatus(cliname string, status *cliAuthStatus) {
	if h == nil || status == nil {
		return
	}
	h.cliAuthSessions.Store(cliname, cliAuthStatusSnapshot(status))
}

func cliAuthStatusSnapshot(status *cliAuthStatus) cliAuthStatus {
	if status == nil {
		return cliAuthStatus{}
	}
	snapshot := *status
	if status.FinishedAt != nil {
		finished := *status.FinishedAt
		snapshot.FinishedAt = &finished
	}
	return snapshot
}
