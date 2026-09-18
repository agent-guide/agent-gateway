// Package proxy implements the HTTP-terminated, non-translating A2A Path A
// pipe. It governs HTTP headers and validates framing while preserving accepted
// JSON-RPC bodies and SSE records byte-for-byte.
package proxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/a2a/jsonrpc"
)

const (
	MaxBodyBytes   int64 = 4 << 20
	MaxEventBytes  int64 = 1 << 20
	MaxStreamBytes int64 = 64 << 20
	IdleTimeout          = 60 * time.Second
)

type InvalidResponseError struct{ Err error }

func (e *InvalidResponseError) Error() string { return "invalid A2A agent response: " + e.Err.Error() }
func (e *InvalidResponseError) Unwrap() error { return e.Err }

type Options struct {
	InterfaceURL string
	HTTPClient   *http.Client
	TotalTimeout time.Duration
	IdleTimeout  time.Duration
}

type Proxy struct {
	interfaceURL *url.URL
	client       *http.Client
	totalTimeout time.Duration
	idleTimeout  time.Duration
}

func New(opts Options) (*Proxy, error) {
	target, err := url.Parse(strings.TrimSpace(opts.InterfaceURL))
	if err != nil || !target.IsAbs() || target.Host == "" || target.User != nil || target.Fragment != "" {
		return nil, fmt.Errorf("A2A interface URL must be absolute")
	}
	client := http.DefaultClient
	if opts.HTTPClient != nil {
		copy := *opts.HTTPClient
		client = &copy
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	total := opts.TotalTimeout
	if total <= 0 {
		total = 120 * time.Second
	}
	idle := opts.IdleTimeout
	if idle <= 0 {
		idle = IdleTimeout
	}
	return &Proxy{interfaceURL: target, client: client, totalTimeout: total, idleTimeout: idle}, nil
}

type Response struct {
	Header    http.Header
	Body      io.ReadCloser
	Buffered  []byte
	Streaming bool
	cancel    context.CancelFunc
}

func (r *Response) Close() error {
	if r == nil {
		return nil
	}
	if r.cancel != nil {
		defer r.cancel()
	}
	if r.Body != nil {
		return r.Body.Close()
	}
	return nil
}

// Do forwards an already inspected request. The caller owns response closure.
func (p *Proxy) Do(ctx context.Context, inbound *http.Request, body []byte, meta jsonrpc.InspectedRequest) (*Response, error) {
	if p == nil || inbound == nil {
		return nil, fmt.Errorf("A2A proxy request is unavailable")
	}
	if int64(len(body)) > MaxBodyBytes {
		return nil, fmt.Errorf("A2A request exceeds %d bytes", MaxBodyBytes)
	}
	requestCtx, cancel := context.WithTimeout(ctx, p.totalTimeout)
	target := *p.interfaceURL
	clientQuery := jsonrpc.RemoveVersionQuery(inbound.URL.RawQuery)
	if target.RawQuery != "" && clientQuery != "" {
		target.RawQuery += "&" + clientQuery
	} else if clientQuery != "" {
		target.RawQuery = clientQuery
	}
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, target.String(), bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header = governedRequestHeaders(inbound.Header)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set(jsonrpc.HeaderVersion, jsonrpc.A2AVersion)
	streaming := meta.Method == jsonrpc.MethodSendStreamingMessage || meta.Method == jsonrpc.MethodSubscribeToTask
	if streaming {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	req.ContentLength = int64(len(body))
	resp, err := p.client.Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	invalid := func(err error) (*Response, error) {
		resp.Body.Close()
		cancel()
		return nil, &InvalidResponseError{Err: err}
	}
	if resp.StatusCode != http.StatusOK {
		return invalid(fmt.Errorf("HTTP status %d", resp.StatusCode))
	}
	if enc := strings.TrimSpace(resp.Header.Get("Content-Encoding")); enc != "" && !strings.EqualFold(enc, "identity") {
		return invalid(fmt.Errorf("unsupported content encoding %q", enc))
	}
	expected := "application/json"
	if streaming {
		expected = "text/event-stream"
	}
	if err := validateMediaType(resp.Header.Get("Content-Type"), expected); err != nil {
		return invalid(err)
	}
	result := &Response{Header: governedResponseHeaders(resp.Header), Streaming: streaming, cancel: cancel}
	if streaming {
		result.Body = newIdleTimeoutBody(resp.Body, p.idleTimeout)
		return result, nil
	}
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes+1))
	resp.Body.Close()
	if err != nil {
		cancel()
		return nil, err
	}
	if int64(len(responseBody)) > MaxBodyBytes {
		cancel()
		return nil, &InvalidResponseError{Err: fmt.Errorf("response exceeds %d bytes", MaxBodyBytes)}
	}
	if err := jsonrpc.ValidateResponse(responseBody, meta.ID); err != nil {
		cancel()
		return nil, &InvalidResponseError{Err: err}
	}
	result.Buffered = responseBody
	return result, nil
}

// CopyStream forwards validated SSE records and flushes after each record when
// dst implements http.Flusher.
func CopyStream(dst io.Writer, src io.Reader, requestID []byte) error {
	return jsonrpc.CopyValidatedSSE(flushingWriter{Writer: dst}, src, MaxEventBytes, MaxStreamBytes, func(data []byte) error {
		if err := jsonrpc.ValidateResponse(data, requestID); err != nil {
			return &InvalidResponseError{Err: err}
		}
		return nil
	})
}

type flushingWriter struct{ io.Writer }

func (w flushingWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if err == nil {
		if flusher, ok := w.Writer.(http.Flusher); ok {
			flusher.Flush()
		}
	}
	return n, err
}

func governedRequestHeaders(in http.Header) http.Header {
	out := in.Clone()
	stripHopByHop(out)
	for _, name := range []string{
		"Host", "Authorization", "Proxy-Authorization", "Cookie", "Forwarded",
		"X-Agent-Depth", "traceparent", "tracestate", "X-Trace-ID", "X-Span-ID",
	} {
		out.Del(name)
	}
	for name := range out {
		if strings.HasPrefix(strings.ToLower(name), "x-forwarded-") {
			out.Del(name)
		}
	}
	return out
}

func governedResponseHeaders(in http.Header) http.Header {
	out := in.Clone()
	stripHopByHop(out)
	for _, name := range []string{"Set-Cookie", "WWW-Authenticate", "Proxy-Authenticate"} {
		out.Del(name)
	}
	return out
}

func stripHopByHop(header http.Header) {
	for _, connection := range header.Values("Connection") {
		for _, name := range strings.Split(connection, ",") {
			header.Del(strings.TrimSpace(name))
		}
	}
	for _, name := range []string{"Connection", "Proxy-Connection", "Keep-Alive", "TE", "Trailer", "Transfer-Encoding", "Upgrade"} {
		header.Del(name)
	}
}

func validateMediaType(value, expected string) error {
	mediaType, params, err := mime.ParseMediaType(value)
	if err != nil || !strings.EqualFold(mediaType, expected) {
		return fmt.Errorf("invalid Content-Type %q", value)
	}
	if len(params) == 0 {
		return nil
	}
	charset, ok := params["charset"]
	if len(params) != 1 || !ok || !strings.EqualFold(charset, "utf-8") {
		return fmt.Errorf("unsupported Content-Type parameters")
	}
	return nil
}

type readResult struct {
	data []byte
	err  error
}

func newIdleTimeoutBody(source io.ReadCloser, timeout time.Duration) io.ReadCloser {
	reader, writer := io.Pipe()
	go func() {
		defer source.Close()
		results := make(chan readResult, 1)
		done := make(chan struct{})
		defer close(done)
		go func() {
			buffer := make([]byte, 32<<10)
			for {
				n, err := source.Read(buffer)
				chunk := append([]byte(nil), buffer[:n]...)
				select {
				case results <- readResult{data: chunk, err: err}:
				case <-done:
					return
				}
				if err != nil {
					return
				}
			}
		}()
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		for {
			select {
			case result := <-results:
				if len(result.data) > 0 {
					if _, err := writer.Write(result.data); err != nil {
						_ = source.Close()
						_ = writer.CloseWithError(err)
						return
					}
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					timer.Reset(timeout)
				}
				if result.err != nil {
					_ = writer.CloseWithError(result.err)
					return
				}
			case <-timer.C:
				_ = source.Close()
				_ = writer.CloseWithError(context.DeadlineExceeded)
				return
			}
		}
	}()
	return reader
}

func IsInvalidResponse(err error) bool {
	var target *InvalidResponseError
	return errors.As(err, &target)
}

// CopyHeaders replaces dst with the governed upstream response headers.
func CopyHeaders(dst, source http.Header) {
	for name := range dst {
		dst.Del(name)
	}
	for name, values := range source {
		for _, value := range values {
			dst.Add(name, value)
		}
	}
}

func ContentLength(body []byte) string { return strconv.Itoa(len(body)) }
