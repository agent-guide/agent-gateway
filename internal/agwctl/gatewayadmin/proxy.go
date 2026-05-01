package gatewayadmin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Proxy struct {
	gatewayAddr string
	adminUser   string
	adminPass   string

	mu     sync.Mutex
	token  string
	client *http.Client
}

func NewProxy(gatewayAddr, adminUser, adminPass string) *Proxy {
	if gatewayAddr == "" {
		gatewayAddr = "http://localhost:8080"
	}
	return &Proxy{
		gatewayAddr: strings.TrimRight(gatewayAddr, "/"),
		adminUser:   adminUser,
		adminPass:   adminPass,
		client:      &http.Client{Timeout: 30 * time.Second},
	}
}

func (p *Proxy) ServeProxy(w http.ResponseWriter, r *http.Request) {
	if p.adminUser == "" {
		writeError(w, http.StatusServiceUnavailable, "gateway proxy not configured: missing admin credentials")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("read request body: %s", err))
		return
	}

	token, err := p.getToken()
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("gateway auth failed: %s", err))
		return
	}

	status, err := p.forward(w, r, token, body)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	if status == http.StatusUnauthorized {
		p.invalidateToken()
		token, err = p.login()
		if err != nil {
			writeError(w, http.StatusBadGateway, fmt.Sprintf("gateway re-auth failed: %s", err))
			return
		}
		status, err = p.forward(w, r, token, body)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		if status == http.StatusUnauthorized {
			writeError(w, http.StatusBadGateway, "gateway returned unauthorized after re-auth")
			return
		}
	}
}

func (p *Proxy) getToken() (string, error) {
	p.mu.Lock()
	t := p.token
	p.mu.Unlock()
	if t != "" {
		return t, nil
	}
	return p.login()
}

func (p *Proxy) login() (string, error) {
	body, _ := json.Marshal(map[string]string{
		"username": p.adminUser,
		"password": p.adminPass,
	})
	resp, err := p.client.Post(p.gatewayAddr+"/admin/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("reach gateway: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gateway login %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var result struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("parse gateway token: %w", err)
	}
	if result.Token == "" {
		return "", fmt.Errorf("parse gateway token: missing token")
	}
	p.mu.Lock()
	p.token = result.Token
	p.mu.Unlock()
	return result.Token, nil
}

func (p *Proxy) invalidateToken() {
	p.mu.Lock()
	p.token = ""
	p.mu.Unlock()
}

func (p *Proxy) forward(w http.ResponseWriter, r *http.Request, token string, body []byte) (int, error) {
	targetURL, err := url.Parse(p.gatewayAddr + r.URL.RequestURI())
	if err != nil {
		return 0, fmt.Errorf("build target url: %w", err)
	}

	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL.String(), bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("build proxy request: %w", err)
	}

	for key, vals := range r.Header {
		k := http.CanonicalHeaderKey(key)
		if k == "Authorization" || k == "Connection" || k == "Te" || k == "Trailers" || k == "Transfer-Encoding" || k == "Upgrade" {
			continue
		}
		for _, v := range vals {
			outReq.Header.Add(k, v)
		}
	}
	outReq.Header.Set("Authorization", "Bearer "+token)

	resp, err := p.client.Do(outReq)
	if err != nil {
		return 0, fmt.Errorf("gateway unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return http.StatusUnauthorized, nil
	}

	for key, vals := range resp.Header {
		k := http.CanonicalHeaderKey(key)
		if k == "Access-Control-Allow-Origin" || k == "Access-Control-Allow-Methods" ||
			k == "Access-Control-Allow-Headers" || k == "Access-Control-Max-Age" {
			continue
		}
		for _, v := range vals {
			w.Header().Add(key, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
	return resp.StatusCode, nil
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
