package server

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type adminSession struct {
	username  string
	createdAt time.Time
}

type sessionStore struct {
	mu       sync.RWMutex
	sessions map[string]adminSession
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

func bearerToken(r *http.Request) string {
	v := r.Header.Get("Authorization")
	if strings.HasPrefix(v, "Bearer ") {
		return strings.TrimPrefix(v, "Bearer ")
	}
	return ""
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.adminUsername == "" {
			_ = writeError(w, http.StatusUnauthorized, "admin authentication not configured")
			return
		}
		token := bearerToken(r)
		if token == "" {
			_ = writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if _, ok := s.sessions.lookup(token); !ok {
			_ = writeError(w, http.StatusUnauthorized, "invalid or expired session")
			return
		}
		next(w, r)
	}
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		_ = writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Username == "" || req.Password == "" {
		_ = writeError(w, http.StatusBadRequest, "username and password are required")
		return
	}
	if s.adminUsername == "" {
		_ = writeError(w, http.StatusServiceUnavailable, "admin credentials not configured")
		return
	}

	hash := s.adminPasswordHash
	if req.Username != s.adminUsername {
		hash = "$2a$10$invalidhashpadding000000000000000000000000000000000000"
	}
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password))
	if req.Username != s.adminUsername || err != nil {
		_ = writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	token, err := s.sessions.create(req.Username)
	if err != nil {
		_ = writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}
	_ = writeJSON(w, http.StatusOK, map[string]string{
		"token":    token,
		"username": req.Username,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if token := bearerToken(r); token != "" {
		s.sessions.revoke(token)
	}
	_ = writeJSON(w, http.StatusOK, map[string]string{"status": "logged_out"})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	token := bearerToken(r)
	sess, ok := s.sessions.lookup(token)
	if !ok {
		_ = writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	_ = writeJSON(w, http.StatusOK, map[string]any{
		"username":   sess.username,
		"created_at": sess.createdAt,
	})
}
