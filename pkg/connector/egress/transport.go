package egress

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"time"
)

// NewClient checks every request, including redirects and SSE POST endpoints.
// It dials the admitted DNS answers directly, preserving the URL host for TLS.
// No environment proxy, cookie jar or idle connection pool bypasses policy.
func NewClient(p Policy) *http.Client {
	return &http.Client{Transport: &policyTransport{policy: p.Clone(), resolver: net.DefaultResolver}}
}

type policyTransport struct {
	policy   Policy
	resolver Resolver
}

func (t *policyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	addresses, err := t.policy.Resolve(req.Context(), t.resolver, req.URL)
	if err != nil {
		return nil, err
	}
	destination, err := destination(req.URL)
	if err != nil {
		return nil, err
	}
	tr := &http.Transport{
		Proxy:               nil,
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: 10 * time.Second,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			var last error
			for _, ip := range addresses {
				conn, err := (&net.Dialer{Timeout: 30 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), strconv.Itoa(destination.port)))
				if err != nil {
					last = err
					continue
				}
				peer, err := netip.ParseAddrPort(conn.RemoteAddr().String())
				if err != nil || peer.Addr().Unmap() != ip || !t.policy.admits(destination, peer.Addr()) {
					conn.Close()
					return nil, &DeniedError{}
				}
				return conn, nil
			}
			return nil, last
		},
	}
	return tr.RoundTrip(req)
}

// CheckURL performs configuration-time admission without resolving DNS.
func (p Policy) CheckURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || !p.CanAdmit(u) {
		return &DeniedError{}
	}
	return nil
}
