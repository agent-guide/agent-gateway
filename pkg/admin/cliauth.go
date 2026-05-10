package admin

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/agent-guide/agent-gateway/internal/httpjson"
	"github.com/agent-guide/agent-gateway/pkg/cliauth"
	"go.uber.org/zap"
)

// cliAuthStatus tracks the state of an async CLI auth flow.
type cliAuthStatus struct {
	LoginID           string     `json:"login_id"`
	AuthenticatorName string     `json:"authenticator_name"`
	Status            string     `json:"status"` // "running", "succeeded", "failed"
	StartedAt         time.Time  `json:"started_at"`
	FinishedAt        *time.Time `json:"finished_at,omitempty"`
	Phase             string     `json:"phase,omitempty"`
	Message           string     `json:"message,omitempty"`
	VerificationURL   string     `json:"verification_url,omitempty"`
	UserCode          string     `json:"user_code,omitempty"`
	Error             string     `json:"error,omitempty"`
	CredentialID      string     `json:"credential_id,omitempty"`
}

type cliAuthRefresherStatus struct {
	Enabled bool `json:"enabled"`
}

type cliAuthAuthenticatorView struct {
	Name         string                      `json:"name"`
	ProviderType string                      `json:"provider_type,omitempty"`
	Enabled      bool                        `json:"enabled"`
	Config       cliauth.AuthenticatorConfig `json:"config"`
}

type updateCLIAuthAuthenticatorRequest struct {
	Enabled *bool                        `json:"enabled,omitempty"`
	Config  *cliauth.AuthenticatorConfig `json:"config"`
}

func (h *Handler) handleListCLIAuthAuthenticators(w http.ResponseWriter, r *http.Request) {
	if h.cliauthManager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "auth manager not configured")
		return
	}

	states := h.cliauthManager.ListAuthenticatorStates()
	items := make([]cliAuthAuthenticatorView, 0, len(states))
	for _, state := range states {
		view, err := h.cliAuthAuthenticatorView(state.Name)
		if err != nil {
			_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
			return
		}
		items = append(items, view)
	}

	_ = httpjson.Write(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) handleGetCLIAuthAuthenticator(w http.ResponseWriter, r *http.Request) {
	if h.cliauthManager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "auth manager not configured")
		return
	}

	view, err := h.cliAuthAuthenticatorView(r.PathValue("authenticator_name"))
	if err != nil {
		_ = httpjson.Error(w, http.StatusNotFound, err.Error())
		return
	}

	_ = httpjson.Write(w, http.StatusOK, view)
}

func (h *Handler) handleUpdateCLIAuthAuthenticator(w http.ResponseWriter, r *http.Request) {
	if h.cliauthManager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "auth manager not configured")
		return
	}

	name := strings.ToLower(strings.TrimSpace(r.PathValue("authenticator_name")))
	if name == "" {
		_ = httpjson.Error(w, http.StatusBadRequest, "authenticator_name is required")
		return
	}

	req, err := decodeCLIAuthAuthenticatorUpdateRequest(r)
	if err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	view, statusText, created, err := h.updateCLIAuthAuthenticator(name, req)
	if err != nil {
		code := http.StatusNotFound
		if strings.Contains(err.Error(), "config is required") || strings.Contains(err.Error(), "at least one of enabled or config is required") || strings.Contains(err.Error(), "config cannot be provided when enabled is false") {
			code = http.StatusBadRequest
		}
		_ = httpjson.Error(w, code, err.Error())
		return
	}
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	_ = httpjson.Write(w, status, map[string]any{"status": statusText, "authenticator": view})
}

func (h *Handler) enableCLIAuthAuthenticator(name string, cfg cliauth.AuthenticatorConfig) (cliauth.AuthenticatorState, bool, error) {
	previous, ok := h.cliauthManager.GetAuthenticatorState(name)
	state, err := h.cliauthManager.EnableAuthenticator(name, cfg)
	if err != nil {
		return cliauth.AuthenticatorState{}, false, err
	}
	return state, !ok || !previous.Enabled, nil
}

func (h *Handler) updateCLIAuthAuthenticator(name string, req updateCLIAuthAuthenticatorRequest) (cliAuthAuthenticatorView, string, bool, error) {
	if req.Enabled == nil && req.Config == nil {
		return cliAuthAuthenticatorView{}, "", false, fmt.Errorf("at least one of enabled or config is required")
	}
	if req.Enabled != nil && !*req.Enabled {
		previous, ok := h.cliauthManager.GetAuthenticatorState(name)
		if req.Config != nil {
			return cliAuthAuthenticatorView{}, "", false, fmt.Errorf("config cannot be provided when enabled is false")
		}
		if err := h.cliauthManager.DisableAuthenticator(name); err != nil {
			return cliAuthAuthenticatorView{}, "", false, err
		}
		view, err := h.cliAuthAuthenticatorView(name)
		if err == nil {
			return view, "disabled", false, nil
		}
		if ok {
			return cliAuthAuthenticatorView{
				Name:         previous.Name,
				ProviderType: previous.ProviderType,
				Enabled:      false,
				Config:       previous.Config,
			}, "disabled", false, nil
		}
		return cliAuthAuthenticatorView{
			Name:    name,
			Enabled: false,
		}, "disabled", false, nil
	}

	if req.Config == nil {
		return cliAuthAuthenticatorView{}, "", false, fmt.Errorf("config is required")
	}
	state, created, err := h.enableCLIAuthAuthenticator(name, *req.Config)
	if err != nil {
		return cliAuthAuthenticatorView{}, "", false, err
	}
	view := cliAuthAuthenticatorView{
		Name:         state.Name,
		ProviderType: state.ProviderType,
		Enabled:      state.Enabled,
		Config:       state.Config,
	}
	status := "updated"
	if created {
		status = "enabled"
	}
	return view, status, created, nil
}

func decodeCLIAuthAuthenticatorUpdateRequest(r *http.Request) (updateCLIAuthAuthenticatorRequest, error) {
	if r == nil || r.Body == nil {
		return updateCLIAuthAuthenticatorRequest{}, fmt.Errorf("request body is required")
	}

	var req updateCLIAuthAuthenticatorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return updateCLIAuthAuthenticatorRequest{}, fmt.Errorf("invalid request body")
	}
	return req, nil
}

func (h *Handler) handleGetCLIAuthRefresherStatus(w http.ResponseWriter, r *http.Request) {
	if h.cliauthRefresher == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "auth refresher not configured")
		return
	}

	_ = httpjson.Write(w, http.StatusOK, cliAuthRefresherStatus{
		Enabled: h.cliauthRefresher.IsRunning(),
	})
}

func (h *Handler) handleEnableCLIAuthRefresher(w http.ResponseWriter, r *http.Request) {
	if h.cliauthRefresher == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "auth refresher not configured")
		return
	}

	alreadyRunning := h.cliauthRefresher.IsRunning()
	h.cliauthRefresher.Start(context.Background())

	status := http.StatusCreated
	if alreadyRunning {
		status = http.StatusOK
	}
	_ = httpjson.Write(w, status, map[string]any{
		"status":  "enabled",
		"enabled": true,
	})
}

func (h *Handler) handleDisableCLIAuthRefresher(w http.ResponseWriter, r *http.Request) {
	if h.cliauthRefresher == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "auth refresher not configured")
		return
	}

	h.cliauthRefresher.Stop()
	_ = httpjson.Write(w, http.StatusOK, map[string]any{
		"status":  "disabled",
		"enabled": false,
	})
}

// handleStartCLIAuthAuthenticatorLogin triggers a provider-specific CLI auth flow asynchronously.
// POST /admin/cliauth/authenticators/{authenticator_name}/login
//
// The handler returns 202 Accepted immediately. In the background it invokes
// the registered Authenticator's Login method. On success the returned
// credential is registered with the auth manager.
func (h *Handler) handleStartCLIAuthAuthenticatorLogin(w http.ResponseWriter, r *http.Request) {
	if h.cliauthManager == nil || h.cliauthRefresher == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "auth manager not configured")
		return
	}

	requestedName := strings.ToLower(strings.TrimSpace(r.PathValue("authenticator_name")))
	if requestedName == "" {
		_ = httpjson.Error(w, http.StatusBadRequest, "authenticator_name is required")
		return
	}

	auth, ok := h.cliauthManager.GetAuthenticator(requestedName)
	if !ok {
		_ = httpjson.Error(w, http.StatusNotFound, requestedName+" authenticator not enabled")
		return
	}

	loginID, err := generateCLIAuthLoginID()
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, "failed to create login session")
		return
	}

	status := &cliAuthStatus{
		LoginID:           loginID,
		AuthenticatorName: requestedName,
		Status:            "running",
		StartedAt:         time.Now().UTC(),
	}
	if activeLoginID, conflict := h.startCLIAuthSession(requestedName, status); conflict {
		_ = httpjson.Error(w, http.StatusConflict, "login already running for "+requestedName+" (login_id="+activeLoginID+")")
		return
	}

	// Run the login flow in the background so the HTTP call returns immediately.
	go func() {
		ctx := context.Background()
		reporter := cliAuthStatusReporter{
			loginID: loginID,
			handler: h,
		}
		cred, err := auth.Login(ctx, reporter)
		finished, ok := h.getCLIAuthStatus(loginID)
		if !ok {
			finished = cliAuthStatusSnapshot(status)
		}
		now := time.Now().UTC()
		finished.FinishedAt = &now
		if err != nil {
			finished.Status = "failed"
			finished.Error = err.Error()
			h.finishCLIAuthSession(loginID, &finished)
			h.logger.Error("cli login failed", zap.String("cliname", requestedName), zap.Error(err))
			return
		}
		if regErr := h.cliauthRefresher.RegisterLoginCredential(ctx, cliauth.NewCLIAuthCredential(cred)); regErr != nil {
			finished.Status = "failed"
			finished.Error = regErr.Error()
			h.finishCLIAuthSession(loginID, &finished)
			h.logger.Error("cli login: register credential failed",
				zap.String("cliname", requestedName), zap.Error(regErr))
			return
		}
		finished.Status = "succeeded"
		finished.CredentialID = cred.ID
		h.finishCLIAuthSession(loginID, &finished)
		h.logger.Info("cli login succeeded",
			zap.String("cliname", requestedName),
			zap.String("login_id", loginID),
			zap.String("credential_id", cred.ID))
	}()

	_ = httpjson.Write(w, http.StatusAccepted, map[string]string{
		"login_id":           loginID,
		"status":             "login_started",
		"authenticator_name": requestedName,
		"message":            "CLI login initiated. Poll /admin/cliauth/logins/{login_id} for the verification URL and any required user action.",
	})
}

// handleGetCLIAuthLoginStatus returns the current status of an async CLI auth flow.
// GET /admin/cliauth/logins/{login_id}
func (h *Handler) handleGetCLIAuthLoginStatus(w http.ResponseWriter, r *http.Request) {
	loginID := strings.TrimSpace(r.PathValue("login_id"))
	if loginID == "" {
		_ = httpjson.Error(w, http.StatusBadRequest, "login_id is required")
		return
	}

	status, ok := h.getCLIAuthStatus(loginID)
	if !ok {
		_ = httpjson.Error(w, http.StatusNotFound, "no login session found for "+loginID)
		return
	}
	_ = httpjson.Write(w, http.StatusOK, status)
}

func (h *Handler) storeCLIAuthStatus(loginID string, status *cliAuthStatus) {
	if h == nil || status == nil {
		return
	}
	h.cliAuthMu.Lock()
	defer h.cliAuthMu.Unlock()
	h.cliAuthSessions[loginID] = cliAuthStatusSnapshot(status)
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

type cliAuthStatusReporter struct {
	loginID string
	handler *Handler
}

func (r cliAuthStatusReporter) UpdateLoginStatus(update cliauth.LoginStatusUpdate) {
	if r.handler == nil || strings.TrimSpace(r.loginID) == "" {
		return
	}
	status, ok := r.handler.getCLIAuthStatus(r.loginID)
	if !ok {
		return
	}
	status.Phase = strings.TrimSpace(update.Phase)
	status.Message = strings.TrimSpace(update.Message)
	status.VerificationURL = strings.TrimSpace(update.VerificationURL)
	status.UserCode = strings.TrimSpace(update.UserCode)
	r.handler.storeCLIAuthStatus(r.loginID, &status)
}

func (h *Handler) startCLIAuthSession(cliname string, status *cliAuthStatus) (string, bool) {
	if h == nil || status == nil || strings.TrimSpace(cliname) == "" || strings.TrimSpace(status.LoginID) == "" {
		return "", true
	}
	h.cliAuthMu.Lock()
	defer h.cliAuthMu.Unlock()

	if activeLoginID, ok := h.cliAuthActive[cliname]; ok {
		if existing, ok := h.cliAuthSessions[activeLoginID]; ok && existing.Status == "running" {
			return activeLoginID, true
		}
		delete(h.cliAuthActive, cliname)
	}

	h.cliAuthSessions[status.LoginID] = cliAuthStatusSnapshot(status)
	h.cliAuthActive[cliname] = status.LoginID
	return "", false
}

func (h *Handler) finishCLIAuthSession(loginID string, status *cliAuthStatus) {
	if h == nil || status == nil || strings.TrimSpace(loginID) == "" {
		return
	}
	h.cliAuthMu.Lock()
	defer h.cliAuthMu.Unlock()

	snapshot := cliAuthStatusSnapshot(status)
	h.cliAuthSessions[loginID] = snapshot
	if activeLoginID, ok := h.cliAuthActive[snapshot.AuthenticatorName]; ok && activeLoginID == loginID {
		delete(h.cliAuthActive, snapshot.AuthenticatorName)
	}
}

func (h *Handler) getCLIAuthStatus(loginID string) (cliAuthStatus, bool) {
	if h == nil || strings.TrimSpace(loginID) == "" {
		return cliAuthStatus{}, false
	}
	h.cliAuthMu.RLock()
	defer h.cliAuthMu.RUnlock()
	status, ok := h.cliAuthSessions[loginID]
	return status, ok
}

func generateCLIAuthLoginID() (string, error) {
	var b [18]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "clilogin-" + base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func (h *Handler) cliAuthAuthenticatorView(rawName string) (cliAuthAuthenticatorView, error) {
	if h == nil || h.cliauthManager == nil {
		return cliAuthAuthenticatorView{}, fmt.Errorf("auth manager not configured")
	}

	name := strings.ToLower(strings.TrimSpace(rawName))
	if name == "" {
		return cliAuthAuthenticatorView{}, fmt.Errorf("authenticator_name is required")
	}

	state, ok := h.cliauthManager.GetAuthenticatorState(name)
	if ok {
		return cliAuthAuthenticatorView{
			Name:         state.Name,
			ProviderType: state.ProviderType,
			Enabled:      state.Enabled,
			Config:       state.Config,
		}, nil
	}

	return cliAuthAuthenticatorView{}, fmt.Errorf("unknown authenticator: %s", name)
}
