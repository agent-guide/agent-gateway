package httpapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/agent-guide/agent-gateway/internal/strictjson"
	connectorruntime "github.com/agent-guide/agent-gateway/pkg/connector/runtime"
	cred "github.com/agent-guide/agent-gateway/pkg/credential/model"
	basemcp "github.com/agent-guide/agent-gateway/pkg/mcp"
)

type Error struct{ Category string }

func (e *Error) Error() string      { return "HTTP tool execution failed: " + e.Category }
func failure(category string) error { return &Error{Category: category} }

type CredentialReader interface {
	GetCredential(string) *cred.ManagedCredential
}
type HTTPExecution = connectorruntime.HTTPExecution

type executionKey struct{}

// Capture installs a request-private channel. The owning logical tool observer
// receives sanitized metadata; tool content and MCP _meta never carry it.
func Capture(ctx context.Context) (context.Context, *HTTPExecution) {
	s := &HTTPExecution{}
	return context.WithValue(ctx, executionKey{}, s), s
}

type Engine struct {
	Observe     func(context.Context, HTTPExecution, time.Time)
	mu          sync.RWMutex
	policy      Policy
	credentials CredentialReader
	resolver    Resolver
	admission   *admission
	ctx         context.Context
	cancel      context.CancelFunc
}

func NewEngine(p Policy, c CredentialReader) (*Engine, error) {
	if e := p.Validate(); e != nil {
		return nil, e
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Engine{policy: p, credentials: c, resolver: net.DefaultResolver, admission: newAdmission(p.Limits), ctx: ctx, cancel: cancel}, nil
}
func (e *Engine) Close() { e.cancel() }
func (e *Engine) Reload(p Policy) error {
	if err := p.Validate(); err != nil {
		return err
	}
	e.mu.Lock()
	e.policy = p
	e.mu.Unlock()
	e.admission.mu.Lock()
	e.admission.limits = p.Limits
	close(e.admission.changed)
	e.admission.changed = make(chan struct{})
	e.admission.mu.Unlock()
	return nil
}
func (e *Engine) CanAdmit(c *Catalog) bool {
	e.mu.RLock()
	p := e.policy
	e.mu.RUnlock()
	return p.CanAdmit(c.base)
}
func (e *Engine) Ready(id string, c *Catalog) bool {
	_, err := e.authorization(id, c.config.Auth)
	return err == nil
}
func (e *Engine) authorization(id string, a *Auth) (string, error) {
	if a == nil || a.Type == "none" {
		return "", nil
	}
	if e.credentials == nil {
		return "", failure("credential_unavailable")
	}
	c := e.credentials.GetCredential(a.CredentialRef)
	if c == nil || c.ID != a.CredentialRef || c.Disabled || c.Type != "api_key" || c.ScopeValue() != cred.ConnectorCredentialScope(id) || c.ProviderID != "" || c.ProviderType != "" {
		return "", failure("credential_unavailable")
	}
	secret := c.APIKey()
	if strings.TrimSpace(secret) == "" || !validValue(secret) {
		return "", failure("credential_unavailable")
	}
	if a.Type == "bearer" {
		secret = "Bearer " + secret
	}
	return secret, nil
}
func readBounded(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return b, err
	}
	if int64(len(b)) > limit {
		return b, failure("response_too_large")
	}
	return b, nil
}

type countingBody struct {
	io.Reader
	n atomic.Int64
}

func (r *countingBody) Read(b []byte) (int, error) {
	n, e := r.Reader.Read(b)
	r.n.Add(int64(n))
	return n, e
}
func (r *countingBody) Close() error { return nil }
func (e *Engine) Call(ctx context.Context, id string, c *Catalog, name string, args map[string]any) (result *basemcp.ToolResult, err error) {
	t := c.tools[name]
	if t == nil {
		return nil, failure("unknown_tool")
	}
	b, jerr := json.Marshal(args)
	if jerr != nil {
		return nil, failure("input_validation")
	}
	v, jerr := strictjson.Parse(b, 1<<20, 64)
	if jerr != nil || t.schema.Validate(v) != nil {
		return nil, failure("input_validation")
	}
	arguments, ok := v.(map[string]any)
	if !ok {
		return nil, failure("input_validation")
	}
	r := t.tool.Request
	path := strings.TrimRight(c.base.Path, "/") + r.Path
	for key, b := range r.PathParams {
		v, ok := arguments[b.Argument].(string)
		if !ok || v == "" || v == "." || v == ".." || strings.ContainsAny(v, "/\\%?#\x00") || !validValue(v) {
			return nil, failure("input_validation")
		}
		path = strings.ReplaceAll(path, "{"+key+"}", url.PathEscape(v))
	}
	targetURL := *c.base
	decoded, jerr := url.PathUnescape(path)
	if jerr != nil {
		return nil, failure("input_validation")
	}
	targetURL.Path = decoded
	targetURL.RawPath = path
	query := url.Values{}
	body := map[string]any{}
	for _, entry := range []struct {
		m map[string]Binding
		q bool
	}{{r.Query, true}, {r.Body, false}} {
		for k, b := range entry.m {
			v := b.Value
			if b.Argument != "" {
				var exists bool
				v, exists = arguments[b.Argument]
				if !exists {
					continue
				}
			}
			if entry.q {
				s, e := scalar(v)
				if e != nil {
					return nil, failure("input_validation")
				}
				query.Set(k, s)
			} else {
				body[k] = v
			}
		}
	}
	targetURL.RawQuery = query.Encode()
	var payload []byte
	if r.Method == "POST" {
		payload, jerr = json.Marshal(body)
		if jerr != nil || len(payload) > 1<<20 {
			return nil, failure("input_validation")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.config.TimeoutMS)*time.Millisecond)
	defer cancel()
	stop := context.AfterFunc(e.ctx, cancel)
	defer stop()
	release, jerr := e.admission.acquire(ctx, id)
	if jerr != nil {
		return nil, safeNetwork(jerr)
	}
	defer release()
	secret, jerr := e.authorization(id, c.config.Auth)
	if jerr != nil {
		return nil, jerr
	}
	e.mu.RLock()
	p := e.policy
	e.mu.RUnlock()
	dest, addresses, jerr := resolve(ctx, e.resolver, p, &targetURL)
	if jerr != nil {
		return nil, jerr
	}
	var summaryMu sync.Mutex
	finished := false
	var started time.Time
	var summary *HTTPExecution
	start := func() bool {
		summaryMu.Lock()
		defer summaryMu.Unlock()
		if finished {
			return false
		}
		if !started.IsZero() {
			return true
		}
		started = time.Now()
		summary = &HTTPExecution{Method: r.Method, HostPort: net.JoinHostPort(dest.host, strconv.Itoa(dest.port)), PathTemplate: strings.TrimRight(c.base.Path, "/") + r.Path}
		return true
	}
	// A per-call transport deliberately avoids shared peers, cookies and mutable
	// headers. Every dial uses this call's complete admitted DNS answer set.
	transport := &http.Transport{Proxy: nil, DisableCompression: true, DisableKeepAlives: true, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: dest.host}, DialContext: func(dctx context.Context, network, addr string) (net.Conn, error) {
		if !start() {
			return nil, context.Canceled
		}
		var last error
		for _, a := range addresses {
			conn, err := (&net.Dialer{}).DialContext(dctx, "tcp", net.JoinHostPort(a.String(), strconv.Itoa(dest.port)))
			if err != nil {
				last = err
				continue
			}
			peer, err := netip.ParseAddrPort(conn.RemoteAddr().String())
			if err != nil || peer.Addr().Unmap() != a || !p.admits(dest, peer.Addr().Unmap()) {
				conn.Close()
				return nil, failure("egress_denied")
			}
			return conn, nil
		}
		return nil, last
	}}
	defer transport.CloseIdleConnections()
	counter := &countingBody{Reader: bytes.NewReader(payload)}
	defer func() {
		summaryMu.Lock()
		finished = true
		defer summaryMu.Unlock()
		if summary != nil {
			summary.DurationMS = time.Since(started).Milliseconds()
			summary.RequestBodyBytes = counter.n.Load()
			if err != nil {
				var safe *Error
				if errors.As(err, &safe) {
					switch safe.Category {
					case "network_error", "timeout", "cancelled":
						summary.NetworkErrorCategory = safe.Category
					}
				}
			}
			if e.Observe != nil {
				func() { defer func() { _ = recover() }(); e.Observe(ctx, *summary, started) }()
			}
			if dst, ok := ctx.Value(executionKey{}).(*HTTPExecution); ok {
				*dst = *summary
			}
		}
	}()
	req, jerr := http.NewRequestWithContext(ctx, r.Method, targetURL.String(), nil)
	if jerr != nil {
		return nil, failure("input_validation")
	}
	if r.Method == "POST" {
		req.Body = counter
		req.ContentLength = int64(len(payload))
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range r.Headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	if secret != "" {
		header := "Authorization"
		if c.config.Auth.Type == "api_key" {
			header = c.config.Auth.HeaderName
		}
		req.Header.Set(header, secret)
	}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, jerr := client.Do(req)
	if jerr != nil {
		return nil, safeNetwork(jerr)
	}
	defer resp.Body.Close()
	status := resp.StatusCode
	summary.StatusCode = &status
	response, jerr := readBounded(resp.Body, c.config.MaxResponseBytes)
	summary.ResponseBodyBytes = int64(len(response))
	if jerr != nil {
		var safe *Error
		if errors.As(jerr, &safe) {
			return nil, jerr
		}
		return nil, safeNetwork(jerr)
	}
	if status < 200 || status >= 300 {
		return nil, failure("http_error")
	}
	if encoding := strings.TrimSpace(resp.Header.Get("Content-Encoding")); encoding != "" && !strings.EqualFold(encoding, "identity") {
		return nil, failure("response_invalid")
	}
	var selected any
	if status == 204 {
		selected = map[string]any{}
	} else {
		media, params, merr := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		if merr != nil || media != "application/json" && !(strings.HasPrefix(media, "application/") && strings.HasSuffix(media, "+json")) {
			return nil, failure("response_invalid")
		}
		for k, v := range params {
			if k != "charset" || !strings.EqualFold(v, "utf-8") {
				return nil, failure("response_invalid")
			}
		}
		selected, jerr = strictjson.Parse(response, int(c.config.MaxResponseBytes), 64)
		if jerr != nil {
			return nil, failure("response_invalid")
		}
	}
	if t.tool.Response != nil {
		parts, _ := pointerParts(t.tool.Response.Select)
		for _, part := range parts {
			switch v := selected.(type) {
			case map[string]any:
				var exists bool
				selected, exists = v[part]
				if !exists {
					return nil, failure("response_mapping")
				}
			case []any:
				idx, e := strconv.Atoi(part)
				if e != nil || idx < 0 || idx >= len(v) || strconv.Itoa(idx) != part {
					return nil, failure("response_mapping")
				}
				selected = v[idx]
			default:
				return nil, failure("response_mapping")
			}
		}
	}
	// MCP structuredContent must be an object, including scalar/array responses.
	if _, ok := selected.(map[string]any); !ok {
		selected = map[string]any{"result": selected}
	}
	text, _ := json.Marshal(selected)
	return &basemcp.ToolResult{StructuredContent: selected, Content: []any{map[string]any{"type": "text", "text": string(text)}}}, nil
}
func safeNetwork(err error) error {
	var safe *Error
	if errors.As(err, &safe) {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return failure("cancelled")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return failure("timeout")
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return failure("timeout")
	}
	return failure("network_error")
}

var _ = fmt.Sprintf
