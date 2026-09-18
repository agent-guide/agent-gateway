// Package card fetches and classifies A2A v1.0 Agent Cards without applying
// gateway credential policy.
package card

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/agent-guide/agent-gateway/pkg/a2a/jsonrpc"
)

const MaxBytes int64 = 1 << 20

type SecurityKind string

const (
	SecurityAnonymous   SecurityKind = "anonymous"
	SecurityBearer      SecurityKind = "single_http_bearer"
	SecurityUnsupported SecurityKind = "unsupported"
)

type SecurityAlternative struct {
	Kind       SecurityKind
	SchemeName string
	Reason     string
}

type Snapshot struct {
	Card         *a2a.AgentCard
	Interfaces   []a2a.AgentInterface
	Security     []SecurityAlternative
	ETag         string
	LastModified string
}

type Validators struct {
	ETag         string
	LastModified string
}

// Fetch gets one public Card without redirects, compression, or authentication.
func Fetch(ctx context.Context, base *http.Client, cardURL string, validators Validators) (*Snapshot, bool, error) {
	if base == nil {
		base = http.DefaultClient
	}
	httpClient := *base
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cardURL, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set(jsonrpc.HeaderVersion, jsonrpc.A2AVersion)
	if validators.ETag != "" {
		req.Header.Set("If-None-Match", validators.ETag)
	}
	if validators.LastModified != "" {
		req.Header.Set("If-Modified-Since", validators.LastModified)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		// Content-Length on 304 describes the selected 200 response; it does
		// not prove that the 304 itself contains a body.
		bodyBytes, err := io.Copy(io.Discard, io.LimitReader(resp.Body, 1))
		if err != nil {
			return nil, false, fmt.Errorf("read 304 Agent Card response: %w", err)
		}
		if bodyBytes > 0 {
			return nil, false, fmt.Errorf("304 Agent Card response must be empty")
		}
		return nil, true, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("Agent Card HTTP status %d", resp.StatusCode)
	}
	if err := validateMediaType(resp.Header.Get("Content-Type"), "application/json"); err != nil {
		return nil, false, err
	}
	if enc := strings.TrimSpace(resp.Header.Get("Content-Encoding")); enc != "" && !strings.EqualFold(enc, "identity") {
		return nil, false, fmt.Errorf("unsupported Agent Card content encoding %q", enc)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBytes+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(body)) > MaxBytes {
		return nil, false, fmt.Errorf("Agent Card exceeds %d bytes", MaxBytes)
	}
	var parsed a2a.AgentCard
	if err := decodeCard(body, &parsed); err != nil {
		return nil, false, err
	}
	return &Snapshot{
		Card: &parsed, Interfaces: validInterfaces(parsed.SupportedInterfaces), Security: classifySecurity(&parsed),
		ETag: resp.Header.Get("ETag"), LastModified: resp.Header.Get("Last-Modified"),
	}, false, nil
}

func decodeCard(body []byte, out *a2a.AgentCard) error {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("Agent Card must be one JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("decode Agent Card: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err != nil {
			return fmt.Errorf("decode trailing Agent Card input: %w", err)
		}
		return fmt.Errorf("Agent Card contains trailing JSON value")
	}
	return nil
}

func validInterfaces(in []*a2a.AgentInterface) []a2a.AgentInterface {
	out := make([]a2a.AgentInterface, 0, len(in))
	for _, candidate := range in {
		if candidate == nil || candidate.ProtocolBinding != a2a.TransportProtocolJSONRPC || candidate.ProtocolVersion != a2a.Version {
			continue
		}
		u, err := url.Parse(strings.TrimSpace(candidate.URL))
		if err != nil || !u.IsAbs() || u.Host == "" || u.User != nil || u.Fragment != "" {
			continue
		}
		if u.Scheme == "http" {
			host := u.Hostname()
			ip := net.ParseIP(host)
			if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
				continue
			}
		} else if u.Scheme != "https" {
			continue
		}
		copy := *candidate
		copy.URL = u.String()
		out = append(out, copy)
	}
	return out
}

func classifySecurity(agentCard *a2a.AgentCard) []SecurityAlternative {
	if len(agentCard.SecurityRequirements) == 0 {
		return []SecurityAlternative{{Kind: SecurityAnonymous}}
	}
	out := make([]SecurityAlternative, 0, len(agentCard.SecurityRequirements))
	for _, requirement := range agentCard.SecurityRequirements {
		if len(requirement) == 0 {
			out = append(out, SecurityAlternative{Kind: SecurityAnonymous})
			continue
		}
		if len(requirement) != 1 {
			out = append(out, SecurityAlternative{Kind: SecurityUnsupported, Reason: "multiple schemes required"})
			continue
		}
		for name := range requirement {
			scheme, ok := agentCard.SecuritySchemes[name]
			if !ok {
				out = append(out, SecurityAlternative{Kind: SecurityUnsupported, SchemeName: string(name), Reason: "unknown security scheme name"})
				continue
			}
			httpScheme, bearer := scheme.(a2a.HTTPAuthSecurityScheme)
			if !bearer || !strings.EqualFold(strings.TrimSpace(httpScheme.Scheme), "Bearer") {
				out = append(out, SecurityAlternative{Kind: SecurityUnsupported, SchemeName: string(name), Reason: "scheme is not HTTP Bearer"})
			} else {
				out = append(out, SecurityAlternative{Kind: SecurityBearer, SchemeName: string(name)})
			}
		}
	}
	return out
}

func validateMediaType(value, expected string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("missing Content-Type")
	}
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
