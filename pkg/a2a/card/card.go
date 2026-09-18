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

const GatewayBearerScheme a2a.SecuritySchemeName = "gatewayBearer"

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

// PublicTemplate returns a deep-cloned, route-neutral gateway-owned Card.
// Remote interfaces, authentication, signatures, and per-skill authentication
// are intentionally removed because the gateway replaces all of them.
func PublicTemplate(remote *a2a.AgentCard, name, description string) (*a2a.AgentCard, error) {
	if remote == nil {
		return nil, fmt.Errorf("Agent Card is required")
	}
	data, err := json.Marshal(remote)
	if err != nil {
		return nil, fmt.Errorf("clone Agent Card: %w", err)
	}
	var public a2a.AgentCard
	if err := json.Unmarshal(data, &public); err != nil {
		return nil, fmt.Errorf("clone Agent Card: %w", err)
	}
	if strings.TrimSpace(name) != "" {
		public.Name = strings.TrimSpace(name)
	}
	if strings.TrimSpace(description) != "" {
		public.Description = strings.TrimSpace(description)
	}
	public.SupportedInterfaces = nil
	public.SecuritySchemes = nil
	public.SecurityRequirements = nil
	public.Signatures = nil
	public.Capabilities.PushNotifications = false
	public.Capabilities.ExtendedAgentCard = false
	for i := range public.Skills {
		public.Skills[i].SecurityRequirements = nil
	}
	return &public, nil
}

// RewritePublicCard materializes one route-specific Card from a public
// template. publicURL and tenant come from the caller; this package does not
// infer deployment topology or VirtualKey policy.
func RewritePublicCard(template *a2a.AgentCard, publicURL, tenant string, requireBearer bool) (*a2a.AgentCard, error) {
	public, err := PublicTemplate(template, "", "")
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(strings.TrimSpace(publicURL))
	if err != nil || !u.IsAbs() || u.Host == "" || u.User != nil || u.Fragment != "" {
		return nil, fmt.Errorf("public Agent interface URL must be absolute")
	}
	public.SupportedInterfaces = []*a2a.AgentInterface{{
		URL: u.String(), ProtocolBinding: a2a.TransportProtocolJSONRPC,
		ProtocolVersion: a2a.Version, Tenant: tenant,
	}}
	if requireBearer {
		public.SecuritySchemes = a2a.NamedSecuritySchemes{
			GatewayBearerScheme: a2a.HTTPAuthSecurityScheme{Scheme: "Bearer", Description: "Agent Gateway VirtualKey"},
		}
		public.SecurityRequirements = a2a.SecurityRequirementsOptions{
			a2a.SecurityRequirements{GatewayBearerScheme: a2a.SecuritySchemeScopes{}},
		}
		for i := range public.Skills {
			public.Skills[i].SecurityRequirements = a2a.SecurityRequirementsOptions{
				a2a.SecurityRequirements{GatewayBearerScheme: a2a.SecuritySchemeScopes{}},
			}
		}
	}
	return public, nil
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
