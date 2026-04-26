package httpclient

import (
	"net/http"
	"net/url"
	"time"
)

// NetworkConfig controls shared HTTP client behavior.
type NetworkConfig struct {
	// RequestTimeoutSeconds is the per-request HTTP timeout. Default: 120.
	RequestTimeoutSeconds int `json:"request_timeout_seconds,omitempty"`
	// MaxRetries is the number of automatic retries on transient errors. Default: 3.
	MaxRetries int `json:"max_retries,omitempty"`
	// RetryDelaySeconds is the base delay between retries. Default: 1.
	RetryDelaySeconds int `json:"retry_delay_seconds,omitempty"`
	// MaxIdleConnections is the maximum number of idle keep-alive connections across all hosts. Default: 100.
	MaxIdleConnections int `json:"max_idle_connections,omitempty"`
	// MaxIdleConnectionsPerHost is the maximum number of idle keep-alive connections to keep per host. Default: 20.
	MaxIdleConnectionsPerHost int `json:"max_idle_connections_per_host,omitempty"`
	// IdleKeepAliveTimeoutSeconds is how long an idle keep-alive connection remains reusable before closing. Default: 90.
	IdleKeepAliveTimeoutSeconds int `json:"idle_keep_alive_timeout_seconds,omitempty"`
	// ProxyURL is an optional HTTP/HTTPS/SOCKS5 proxy URL.
	ProxyURL string `json:"proxy_url,omitempty"`
	// ExtraHeaders are additional HTTP headers sent with every request.
	ExtraHeaders map[string]string `json:"extra_headers,omitempty"`
}

// Defaults fills in zero values with sensible defaults.
func (c *NetworkConfig) Defaults() {
	if c.RequestTimeoutSeconds == 0 {
		c.RequestTimeoutSeconds = 120
	}
	if c.MaxRetries == 0 {
		c.MaxRetries = 3
	}
	if c.RetryDelaySeconds == 0 {
		c.RetryDelaySeconds = 1
	}
	if c.MaxIdleConnections == 0 {
		c.MaxIdleConnections = 100
	}
	if c.MaxIdleConnectionsPerHost == 0 {
		c.MaxIdleConnectionsPerHost = 20
	}
	if c.IdleKeepAliveTimeoutSeconds == 0 {
		c.IdleKeepAliveTimeoutSeconds = 90
	}
}

// RequestTimeout returns the configured request timeout as a time.Duration.
func (c *NetworkConfig) RequestTimeout() time.Duration {
	if c.RequestTimeoutSeconds == 0 {
		return 120 * time.Second
	}
	return time.Duration(c.RequestTimeoutSeconds) * time.Second
}

// IdleKeepAliveTimeout returns the configured idle keep-alive timeout as a time.Duration.
func (c *NetworkConfig) IdleKeepAliveTimeout() time.Duration {
	if c.IdleKeepAliveTimeoutSeconds == 0 {
		return 90 * time.Second
	}
	return time.Duration(c.IdleKeepAliveTimeoutSeconds) * time.Second
}

type headerRoundTripper struct {
	base    http.RoundTripper
	headers map[string]string
}

func (h *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	for k, v := range h.headers {
		cloned.Header.Set(k, v)
	}
	return h.base.RoundTrip(cloned)
}

// BuildHTTPClient constructs a shared HTTP client from the network config.
func BuildHTTPClient(config NetworkConfig) *http.Client {
	config.Defaults()
	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		MaxIdleConns:        config.MaxIdleConnections,
		MaxIdleConnsPerHost: config.MaxIdleConnectionsPerHost,
		IdleConnTimeout:     config.IdleKeepAliveTimeout(),
	}
	if config.ProxyURL != "" {
		if parsed, err := url.Parse(config.ProxyURL); err == nil {
			transport.Proxy = http.ProxyURL(parsed)
		}
	}

	headers := make(map[string]string, len(config.ExtraHeaders))
	for k, v := range config.ExtraHeaders {
		headers[k] = v
	}

	rt := http.RoundTripper(transport)
	if len(headers) > 0 {
		rt = &headerRoundTripper{base: rt, headers: headers}
	}

	return &http.Client{
		Timeout:   config.RequestTimeout(),
		Transport: rt,
	}
}
