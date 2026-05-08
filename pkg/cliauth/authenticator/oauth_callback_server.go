package authenticator

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

type oauthCallbackResult struct {
	code  string
	state string
	err   string
}

type callbackHTTPServer struct {
	port     int
	srv      *http.Server
	resultCh chan oauthCallbackResult
	mu       sync.Mutex
	running  bool
}

func newCallbackHTTPServer(port int) *callbackHTTPServer {
	return &callbackHTTPServer{
		port:     port,
		resultCh: make(chan oauthCallbackResult, 1),
	}
}

func (s *callbackHTTPServer) start(handler http.Handler) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("callback server already running")
	}

	addr := fmt.Sprintf(":%d", s.port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("port %d already in use: %w", s.port, err)
	}

	s.srv = &http.Server{
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	s.running = true

	go func() {
		_ = s.srv.Serve(ln)
	}()
	return nil
}

func (s *callbackHTTPServer) stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running || s.srv == nil {
		return nil
	}
	err := s.srv.Shutdown(ctx)
	s.running = false
	s.srv = nil
	return err
}

func (s *callbackHTTPServer) waitForCallback(timeout time.Duration) (code, state string, err error) {
	select {
	case result := <-s.resultCh:
		if result.err != "" {
			return "", "", fmt.Errorf("OAuth callback error: %s", result.err)
		}
		return result.code, result.state, nil
	case <-time.After(timeout):
		return "", "", fmt.Errorf("timed out waiting for OAuth callback after %s", timeout)
	}
}

func writeOAuthSuccessHTML(w http.ResponseWriter, html string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(html))
}
