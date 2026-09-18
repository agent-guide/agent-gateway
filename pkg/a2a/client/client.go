// Package client wraps the official A2A client with the gateway's fixed
// JSON-RPC-only transport policy.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	agwjsonrpc "github.com/agent-guide/agent-gateway/pkg/a2a/jsonrpc"
)

const (
	MaxBodyBytes   int64 = 4 << 20
	MaxEventBytes  int64 = 1 << 20
	MaxStreamBytes int64 = 64 << 20
	IdleTimeout          = 60 * time.Second
)

type ResponseError struct {
	StatusCode int
}

func (e *ResponseError) Error() string { return fmt.Sprintf("A2A HTTP status %d", e.StatusCode) }

type Client struct {
	sdk *a2aclient.Client
}

func New(ctx context.Context, iface a2a.AgentInterface, httpClient *http.Client) (*Client, error) {
	if iface.ProtocolBinding != a2a.TransportProtocolJSONRPC || iface.ProtocolVersion != a2a.Version {
		return nil, fmt.Errorf("unsupported A2A interface")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	guarded := *httpClient
	guarded.Transport = &guardTransport{base: transportOrDefault(httpClient.Transport)}
	guarded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	factory := a2aclient.NewFactory(
		a2aclient.WithDefaultsDisabled(),
		a2aclient.WithConfig(a2aclient.Config{DisableTenantPropagation: true}),
		a2aclient.WithJSONRPCTransport(&guarded),
	)
	sdk, err := factory.CreateFromEndpoints(ctx, []*a2a.AgentInterface{&iface})
	if err != nil {
		return nil, err
	}
	return &Client{sdk: sdk}, nil
}

func (c *Client) SendMessage(ctx context.Context, req *a2a.SendMessageRequest) (a2a.SendMessageResult, error) {
	return c.sdk.SendMessage(ctx, req)
}

func (c *Client) SendStreamingMessage(ctx context.Context, req *a2a.SendMessageRequest) iter.Seq2[a2a.Event, error] {
	return c.sdk.SendStreamingMessage(ctx, req)
}

func (c *Client) CancelTask(ctx context.Context, req *a2a.CancelTaskRequest) (*a2a.Task, error) {
	return c.sdk.CancelTask(ctx, req)
}

func (c *Client) Close() error {
	if c == nil || c.sdk == nil {
		return nil
	}
	return c.sdk.Destroy()
}

type guardTransport struct {
	base http.RoundTripper
}

func (t *guardTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.Body == nil {
		return nil, fmt.Errorf("A2A request body is required")
	}
	clone := req.Clone(req.Context())
	clone.Header.Set(agwjsonrpc.HeaderVersion, agwjsonrpc.A2AVersion)
	clone.Header.Set("Accept-Encoding", "identity")
	if clone.Header.Get("Accept") == "" {
		clone.Header.Set("Accept", "application/json")
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, MaxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > MaxBodyBytes {
		return nil, fmt.Errorf("A2A request exceeds %d bytes", MaxBodyBytes)
	}
	meta, err := agwjsonrpc.ParseRequestMeta(body)
	if err != nil {
		return nil, err
	}
	clone.Body = io.NopCloser(bytes.NewReader(body))
	clone.ContentLength = int64(len(body))
	resp, err := t.base.RoundTrip(clone)
	if err != nil {
		return nil, err
	}
	closeWithError := func(err error) (*http.Response, error) {
		resp.Body.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return closeWithError(&ResponseError{StatusCode: resp.StatusCode})
	}
	if enc := strings.TrimSpace(resp.Header.Get("Content-Encoding")); enc != "" && !strings.EqualFold(enc, "identity") {
		return closeWithError(fmt.Errorf("unsupported content encoding %q", enc))
	}
	streaming := acceptsMediaType(clone.Header.Get("Accept"), "text/event-stream")
	expected := "application/json"
	if streaming {
		expected = "text/event-stream"
	}
	if err := validateMediaType(resp.Header.Get("Content-Type"), expected); err != nil {
		return closeWithError(err)
	}
	resp.Body = newIdleTimeoutBody(resp.Body, IdleTimeout)
	if streaming {
		resp.Body = newValidatingSSEBody(resp.Body, meta.ID)
		return resp, nil
	}
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes+1))
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	if int64(len(responseBody)) > MaxBodyBytes {
		return nil, fmt.Errorf("A2A response exceeds %d bytes", MaxBodyBytes)
	}
	if err := agwjsonrpc.ValidateResponse(responseBody, meta.ID); err != nil {
		return nil, fmt.Errorf("invalid A2A response: %w", err)
	}
	resp.Body = io.NopCloser(bytes.NewReader(responseBody))
	resp.ContentLength = int64(len(responseBody))
	return resp, nil
}

func acceptsMediaType(value, expected string) bool {
	for _, item := range strings.Split(value, ",") {
		mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(item))
		if err != nil || !strings.EqualFold(mediaType, expected) {
			continue
		}
		if rawQuality, ok := params["q"]; ok {
			quality, err := strconv.ParseFloat(strings.TrimSpace(rawQuality), 64)
			if err != nil || quality <= 0 || quality > 1 {
				continue
			}
		}
		return true
	}
	return false
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

func transportOrDefault(rt http.RoundTripper) http.RoundTripper {
	if rt == nil {
		return http.DefaultTransport
	}
	return rt
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

func newValidatingSSEBody(source io.ReadCloser, requestID json.RawMessage) io.ReadCloser {
	reader, writer := io.Pipe()
	go func() {
		defer source.Close()
		writer.CloseWithError(copyValidatedSSE(writer, source, requestID))
	}()
	return reader
}

func copyValidatedSSE(dst io.Writer, src io.Reader, requestID json.RawMessage) error {
	return copyValidatedSSEWithLimits(dst, src, requestID, MaxEventBytes, MaxStreamBytes)
}

func copyValidatedSSEWithLimits(dst io.Writer, src io.Reader, requestID json.RawMessage, maxEventBytes, maxStreamBytes int64) error {
	return agwjsonrpc.CopyValidatedSSE(dst, src, maxEventBytes, maxStreamBytes, func(data []byte) error {
		if err := agwjsonrpc.ValidateResponse(data, requestID); err != nil {
			return fmt.Errorf("invalid A2A SSE event: %w", err)
		}
		return nil
	})
}
