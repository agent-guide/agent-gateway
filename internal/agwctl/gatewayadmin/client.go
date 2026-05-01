package gatewayadmin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL  string
	username string
	password string
	client   *http.Client
}

func NewClient(baseURL, username, password string) *Client {
	if baseURL == "" {
		baseURL = "http://localhost:8019"
	}
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		username: username,
		password: password,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) List(path string) ([]map[string]any, any, error) {
	if c.username == "" {
		return nil, nil, fmt.Errorf("--user is required for gateway commands")
	}
	token, err := c.login()
	if err != nil {
		return nil, nil, fmt.Errorf("gateway login: %w", err)
	}

	req, err := http.NewRequest(http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("gateway unreachable: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, nil, fmt.Errorf("gateway error %d: %s", resp.StatusCode, string(raw))
	}

	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, nil, fmt.Errorf("parse gateway response: %w", err)
	}

	var items []map[string]any
	if rawItems, ok := envelope["items"].([]any); ok {
		for _, v := range rawItems {
			if m, ok := v.(map[string]any); ok {
				items = append(items, m)
			}
		}
	}
	return items, envelope, nil
}

func (c *Client) login() (string, error) {
	body, _ := json.Marshal(map[string]string{"username": c.username, "password": c.password})
	resp, err := c.client.Post(c.baseURL+"/admin/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("reach gateway: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, string(raw))
	}
	var result struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("parse token from response: %w; body=%q", err, strings.TrimSpace(string(raw)))
	}
	if result.Token == "" {
		return "", fmt.Errorf("parse token from response: missing token; body=%q", strings.TrimSpace(string(raw)))
	}
	return result.Token, nil
}
