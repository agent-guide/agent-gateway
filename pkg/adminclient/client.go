package adminclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/agent-guide/agent-gateway/internal/httpjson"
)

const (
	defaultBaseURL = "http://localhost:8019"
)

type Config struct {
	BaseURL    string
	Username   string
	Password   string
	Token      string
	HTTPClient *http.Client
}

type Client struct {
	baseURL  string
	username string
	password string
	client   *http.Client

	mu    sync.Mutex
	token string
}

type Error struct {
	StatusCode int
	Message    string
	Body       []byte
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return fmt.Sprintf("admin API error %d: %s", e.StatusCode, e.Message)
	}
	if len(e.Body) != 0 {
		return fmt.Sprintf("admin API error %d: %s", e.StatusCode, strings.TrimSpace(string(e.Body)))
	}
	return fmt.Sprintf("admin API error %d", e.StatusCode)
}

func New(cfg Config) *Client {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		baseURL:  baseURL,
		username: strings.TrimSpace(cfg.Username),
		password: cfg.Password,
		token:    strings.TrimSpace(cfg.Token),
		client:   httpClient,
	}
}

func (c *Client) BaseURL() string {
	if c == nil {
		return ""
	}
	return c.baseURL
}

func (c *Client) SetToken(token string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token = strings.TrimSpace(token)
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type LoginResponse struct {
	Token    string `json:"token"`
	Username string `json:"username"`
}

type MeResponse struct {
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
}

type StatusResponse struct {
	Status string `json:"status"`
	ID     string `json:"id,omitempty"`
	Key    string `json:"key,omitempty"`
}

func (c *Client) Login(ctx context.Context) (*LoginResponse, error) {
	if c == nil {
		return nil, fmt.Errorf("admin client is nil")
	}
	if c.username == "" {
		return nil, fmt.Errorf("admin client username is required")
	}
	if c.password == "" {
		return nil, fmt.Errorf("admin client password is required")
	}

	resp := &LoginResponse{}
	if err := c.do(ctx, http.MethodPost, "/admin/auth/login", LoginRequest{
		Username: c.username,
		Password: c.password,
	}, resp, false, http.StatusOK); err != nil {
		return nil, err
	}
	if resp.Token == "" {
		return nil, fmt.Errorf("admin login succeeded but returned an empty token")
	}
	c.SetToken(resp.Token)
	return resp, nil
}

func (c *Client) Logout(ctx context.Context) error {
	if c == nil {
		return fmt.Errorf("admin client is nil")
	}
	var resp StatusResponse
	if err := c.do(ctx, http.MethodPost, "/admin/auth/logout", nil, &resp, true, http.StatusOK); err != nil {
		return err
	}
	c.SetToken("")
	return nil
}

func (c *Client) Me(ctx context.Context) (*MeResponse, error) {
	var resp MeResponse
	if err := c.do(ctx, http.MethodGet, "/admin/auth/me", nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) do(ctx context.Context, method, path string, reqBody any, out any, auth bool, okStatuses ...int) error {
	if c == nil {
		return fmt.Errorf("admin client is nil")
	}
	fullURL := c.baseURL + path

	var bodyReader io.Reader
	if reqBody != nil {
		payload, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
	if err != nil {
		return err
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		token, err := c.ensureToken(ctx)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("request %s %s: %w", method, fullURL, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	if !statusAllowed(resp.StatusCode, okStatuses) {
		return &Error{
			StatusCode: resp.StatusCode,
			Message:    httpjson.ErrorMessage(raw),
			Body:       raw,
		}
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode response body: %w", err)
	}
	return nil
}

func (c *Client) ensureToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	token := c.token
	c.mu.Unlock()
	if token != "" {
		return token, nil
	}
	resp, err := c.Login(ctx)
	if err != nil {
		return "", err
	}
	return resp.Token, nil
}

func statusAllowed(statusCode int, okStatuses []int) bool {
	for _, ok := range okStatuses {
		if statusCode == ok {
			return true
		}
	}
	return false
}
