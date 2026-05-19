package authenticator

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	utls "github.com/refraction-networking/utls"

	"github.com/agent-guide/agent-gateway/pkg/cliauth"
	"github.com/agent-guide/agent-gateway/pkg/httpclient"
)

const claudecodeBrowserLikeTLSProfile = "browser_like_tls"

func buildClaudeCodeHTTPClient(cfg cliauth.AuthenticatorConfig) *http.Client {
	if strings.EqualFold(strings.TrimSpace(cfg.TransportProfile), claudecodeBrowserLikeTLSProfile) {
		return buildClaudeCodeBrowserLikeHTTPClient(cfg.Network)
	}
	return httpclient.BuildHTTPClient(cfg.Network)
}

func buildClaudeCodeBrowserLikeHTTPClient(config httpclient.NetworkConfig) *http.Client {
	config.Defaults()

	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		MaxIdleConns:        config.MaxIdleConnections,
		MaxIdleConnsPerHost: config.MaxIdleConnectionsPerHost,
		IdleConnTimeout:     config.IdleKeepAliveTimeout(),
		ForceAttemptHTTP2:   true,
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialUTLSContext(ctx, network, addr)
		},
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

func dialUTLSContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("claudecode: parse server address %q: %w", addr, err)
	}

	rawConn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}

	cfg := &utls.Config{
		ServerName: host,
		NextProtos: []string{"h2", "http/1.1"},
	}
	tlsConn := utls.UClient(rawConn, cfg, utls.HelloFirefox_Auto)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = rawConn.Close()
		return nil, fmt.Errorf("claudecode: TLS handshake failed: %w", err)
	}
	return tlsConn, nil
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
