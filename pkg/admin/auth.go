package admin

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/agent-guide/caddy-agent-gateway/internal/httpjson"
	"golang.org/x/crypto/bcrypt"
)

// adminSession holds a live admin session.
type adminSession struct {
	username  string
	createdAt time.Time
}

// sessionStore is an in-memory store for admin session tokens.
type sessionStore struct {
	mu       sync.RWMutex
	sessions map[string]adminSession // opaque token -> session
}

func newSessionStore() *sessionStore {
	return &sessionStore{sessions: make(map[string]adminSession)}
}

func (s *sessionStore) create(username string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	s.mu.Lock()
	s.sessions[token] = adminSession{username: username, createdAt: time.Now().UTC()}
	s.mu.Unlock()
	return token, nil
}

func (s *sessionStore) lookup(token string) (adminSession, bool) {
	s.mu.RLock()
	sess, ok := s.sessions[token]
	s.mu.RUnlock()
	return sess, ok
}

func (s *sessionStore) revoke(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

// bearerToken extracts the Bearer token from the Authorization header.
func bearerToken(r *http.Request) string {
	v := r.Header.Get("Authorization")
	if strings.HasPrefix(v, "Bearer ") {
		return strings.TrimPrefix(v, "Bearer ")
	}
	return ""
}

// requireAuth wraps a handler with Bearer-token session authentication.
// Protected admin routes require configured admin credentials and a valid
// Bearer-token session.
func (h *Handler) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.adminUsername == "" {
			_ = httpjson.Error(w, http.StatusUnauthorized, "admin authentication not configured")
			return
		}
		token := bearerToken(r)
		if token == "" {
			_ = httpjson.Error(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if _, ok := h.sessions.lookup(token); !ok {
			_ = httpjson.Error(w, http.StatusUnauthorized, "invalid or expired session")
			return
		}
		next(w, r)
	}
}

// handleLogin authenticates admin credentials and issues a session token.
// POST /admin/auth/login
func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := httpjson.Decode(r, &req); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Username == "" || req.Password == "" {
		_ = httpjson.Error(w, http.StatusBadRequest, "username and password are required")
		return
	}
	if h.adminUsername == "" {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "admin credentials not configured")
		return
	}

	// Always run bcrypt to prevent timing-based username enumeration.
	hash := h.adminPasswordHash
	if req.Username != h.adminUsername {
		hash = "$2a$10$invalidhashpadding000000000000000000000000000000000000" // dummy
	}
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password))
	if req.Username != h.adminUsername || err != nil {
		_ = httpjson.Error(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	token, err := h.sessions.create(req.Username)
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, "failed to create session")
		return
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]string{
		"token":    token,
		"username": req.Username,
	})
}

// handleLogout invalidates the current session token.
// POST /admin/auth/logout
func (h *Handler) handleLogout(w http.ResponseWriter, r *http.Request) {
	if token := bearerToken(r); token != "" {
		h.sessions.revoke(token)
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]string{"status": "logged_out"})
}

// handleMe returns the current authenticated user's info.
// GET /admin/auth/me
func (h *Handler) handleMe(w http.ResponseWriter, r *http.Request) {
	token := bearerToken(r)
	sess, ok := h.sessions.lookup(token)
	if !ok {
		_ = httpjson.Error(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]any{
		"username":   sess.username,
		"created_at": sess.createdAt,
	})
}

func (h *Handler) sessionUsername(r *http.Request) string {
	if h == nil || r == nil {
		return ""
	}
	token := bearerToken(r)
	if token == "" {
		return ""
	}
	sess, ok := h.sessions.lookup(token)
	if !ok {
		return ""
	}
	return sess.username
}
