// Package httpapi compiles declarative HTTP tools and executes them without
// depending on Agent definitions or MCP sessions.
package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/agent-guide/agent-gateway/internal/strictjson"
	basemcp "github.com/agent-guide/agent-gateway/pkg/mcp"
)

type Config struct {
	BaseURL          string `json:"base_url"`
	Auth             *Auth  `json:"auth,omitempty"`
	TimeoutMS        int    `json:"timeout_ms,omitempty"`
	MaxResponseBytes int64  `json:"max_response_bytes,omitempty"`
	Tools            []Tool `json:"tools"`
}
type Auth struct {
	Type          string `json:"type"`
	CredentialRef string `json:"credential_ref,omitempty"`
	HeaderName    string `json:"header_name,omitempty"`
}
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
	Request     Request        `json:"request"`
	Response    *Response      `json:"response,omitempty"`
}
type Request struct {
	Method     string             `json:"method"`
	Path       string             `json:"path"`
	PathParams map[string]Binding `json:"path_params,omitempty"`
	Query      map[string]Binding `json:"query,omitempty"`
	Body       map[string]Binding `json:"body,omitempty"`
	Headers    map[string]string  `json:"headers,omitempty"`
}
type Response struct {
	Select string `json:"select,omitempty"`
}
type Binding struct {
	Argument string `json:"argument,omitempty"`
	Value    any    `json:"value,omitempty"`
	literal  bool
}

func (b *Binding) UnmarshalJSON(data []byte) error {
	v, e := strictjson.Parse(data, 1<<20, 64)
	if e != nil {
		return e
	}
	m, ok := v.(map[string]any)
	if !ok || len(m) != 1 {
		return fmt.Errorf("binding must select exactly argument or value")
	}
	if v, ok := m["argument"]; ok {
		b.Argument, ok = v.(string)
		if !ok || b.Argument == "" {
			return fmt.Errorf("invalid argument binding")
		}
		return nil
	}
	v, ok = m["value"]
	if !ok {
		return fmt.Errorf("invalid binding")
	}
	b.Value = v
	b.literal = true
	return nil
}
func (b Binding) MarshalJSON() ([]byte, error) {
	if b.Argument != "" {
		return json.Marshal(map[string]any{"argument": b.Argument})
	}
	return json.Marshal(map[string]any{"value": b.Value})
}
func (c *Config) UnmarshalJSON(data []byte) error {
	// Null is business data only inside a static binding or enum.
	v, e := strictjson.Parse(data, 4<<20, 128)
	if e != nil {
		return e
	}
	if e = nonNull(v, ""); e != nil {
		return e
	}
	type plain Config
	p := plain{TimeoutMS: 10000, MaxResponseBytes: 1 << 20}
	if e := strictjson.Decode(data, &p, 4<<20, 128); e != nil {
		return e
	}
	if p.TimeoutMS < 1 || p.MaxResponseBytes < 1 {
		return fmt.Errorf("invalid HTTP limits")
	}
	*c = Config(p)
	return nil
}
func (a *Auth) UnmarshalJSON(data []byte) error {
	v, e := strictjson.Parse(data, 4096, 8)
	if e != nil {
		return e
	}
	m, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("auth must be object")
	}
	typ, _ := m["type"].(string)
	allowed := map[string]bool{"type": true}
	if typ == "bearer" || typ == "api_key" {
		allowed["credential_ref"] = true
	}
	if typ == "api_key" {
		allowed["header_name"] = true
	}
	for k, v := range m {
		if !allowed[k] || v == nil {
			return fmt.Errorf("invalid auth variant")
		}
	}
	type plain Auth
	var p plain
	if e := strictjson.Decode(data, &p, 4096, 8); e != nil {
		return e
	}
	*a = Auth(p)
	return nil
}
func nonNull(v any, path string) error {
	// Static body/query values and enum instances are JSON data rather than
	// nullable configuration fields. Their narrower contracts are checked later.
	data := strings.Contains(path, "/input_schema/") && strings.HasSuffix(path, "/enum") || strings.Contains(path, "/request/body/") && strings.HasSuffix(path, "/value") || strings.Contains(path, "/request/query/") && strings.HasSuffix(path, "/value")
	if data {
		return nil
	}
	if v == nil {
		return fmt.Errorf("explicit null is unsupported")
	}
	switch x := v.(type) {
	case map[string]any:
		for k, v := range x {
			if err := nonNull(v, path+"/"+k); err != nil {
				return err
			}
		}
	case []any:
		for _, v := range x {
			if err := nonNull(v, path+"/[]"); err != nil {
				return err
			}
		}
	}
	return nil
}

var toolName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
var placeholder = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var headerToken = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

func reserved(s string) bool {
	s = strings.ToLower(s)
	for _, p := range []string{"proxy-", "sec-", "forwarded", "x-forwarded-"} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	switch s {
	case "authorization", "host", "cookie", "set-cookie", "content-type", "content-length", "content-encoding", "transfer-encoding", "connection", "keep-alive", "te", "trailer", "upgrade", "accept", "accept-encoding":
		return true
	}
	return false
}
func validHeader(s string) bool {
	return len(s) > 0 && len(s) <= 128 && headerToken.MatchString(s) && !reserved(s)
}
func validValue(s string) bool {
	for _, r := range s {
		if r < 32 || r == 127 {
			return false
		}
	}
	return utf8.ValidString(s)
}
func safePath(p string, template bool) bool {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.ContainsAny(p, "\\?#") || !utf8.ValidString(p) {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "." || seg == ".." || strings.Contains(seg, "%") || !validValue(seg) {
			return false
		}
		if strings.ContainsAny(seg, "{}") && (!template || !strings.HasPrefix(seg, "{") || !strings.HasSuffix(seg, "}") || !placeholder.MatchString(seg[1:len(seg)-1])) {
			return false
		}
	}
	return true
}
func (c Config) Validate() error { _, e := Compile(c); return e }

type Catalog struct {
	config Config
	base   *url.URL
	tools  map[string]*compiledTool
	public []basemcp.Tool
}
type compiledTool struct {
	tool   Tool
	schema *Schema
}

func Compile(c Config) (*Catalog, error) {
	encoded, err := json.Marshal(c)
	if err != nil || len(encoded) > 4<<20 {
		return nil, fmt.Errorf("HTTP configuration exceeds limit")
	}
	var normalized Config
	if err = json.Unmarshal(encoded, &normalized); err != nil {
		return nil, err
	}
	c = normalized
	if c.TimeoutMS == 0 {
		c.TimeoutMS = 10000
	}
	if c.MaxResponseBytes == 0 {
		c.MaxResponseBytes = 1 << 20
	}
	if c.TimeoutMS < 1 || c.TimeoutMS > 60000 || c.MaxResponseBytes < 1 || c.MaxResponseBytes > 8<<20 {
		return nil, fmt.Errorf("invalid HTTP limits")
	}
	base, e := url.Parse(c.BaseURL)
	if e != nil || base.User != nil || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" || base.RawFragment != "" || (base.Scheme != "https" && base.Scheme != "http") || base.Hostname() == "" || base.Opaque != "" {
		return nil, fmt.Errorf("invalid base_url")
	}
	if base.Path != "" && !safePath(base.EscapedPath(), false) {
		return nil, fmt.Errorf("invalid base path")
	}
	if _, e := destination(base); e != nil {
		return nil, e
	}
	if c.Auth != nil {
		a := c.Auth
		switch a.Type {
		case "none":
			if a.CredentialRef != "" || a.HeaderName != "" {
				return nil, fmt.Errorf("none auth accepts type only")
			}
		case "bearer", "api_key":
			if strings.TrimSpace(a.CredentialRef) == "" || strings.ContainsAny(a.CredentialRef, "/:\\${}") || strings.TrimSpace(a.CredentialRef) != a.CredentialRef {
				return nil, fmt.Errorf("invalid credential reference")
			}
			if a.Type == "bearer" && a.HeaderName != "" || a.Type == "api_key" && !validHeader(a.HeaderName) {
				return nil, fmt.Errorf("invalid auth header")
			}
		default:
			return nil, fmt.Errorf("unsupported HTTP auth")
		}
	}
	if len(c.Tools) < 1 || len(c.Tools) > 128 {
		return nil, fmt.Errorf("HTTP connector requires 1-128 tools")
	}
	cat := &Catalog{config: c, base: base, tools: map[string]*compiledTool{}}
	for _, t := range c.Tools {
		if !toolName.MatchString(t.Name) || strings.TrimSpace(t.Description) == "" || len(t.Description) > 4096 || !utf8.ValidString(t.Description) || cat.tools[t.Name] != nil {
			return nil, fmt.Errorf("invalid or duplicate tool")
		}
		s, e := CompileSchema(t.InputSchema)
		if e != nil {
			return nil, e
		}
		r := t.Request
		if r.Method != "GET" && r.Method != "POST" || !safePath(r.Path, true) || r.Method == "GET" && r.Body != nil {
			return nil, fmt.Errorf("invalid request mapping")
		}
		placeholders := map[string]bool{}
		for _, seg := range strings.Split(r.Path, "/") {
			if strings.HasPrefix(seg, "{") {
				placeholders[seg[1:len(seg)-1]] = true
			}
		}
		if len(placeholders) != len(r.PathParams) {
			return nil, fmt.Errorf("path bindings mismatch")
		}
		for k, b := range r.PathParams {
			p := s.properties[b.Argument]
			if !placeholders[k] || b.Argument == "" || p == nil || len(p.types) != 1 || !p.types["string"] {
				return nil, fmt.Errorf("invalid path binding")
			}
			required := false
			if a, ok := s.raw["required"].([]any); ok {
				for _, v := range a {
					required = required || v == b.Argument
				}
			}
			min, ok := number(p.raw["minLength"])
			if !required || !ok || min.Sign() <= 0 {
				return nil, fmt.Errorf("path argument must be required nonempty string")
			}
		}
		for _, entry := range []struct {
			m     map[string]Binding
			query bool
		}{{r.Query, true}, {r.Body, false}} {
			for k, b := range entry.m {
				if k == "" {
					return nil, fmt.Errorf("empty mapping key")
				}
				if b.Argument != "" {
					p := s.properties[b.Argument]
					if p == nil {
						return nil, fmt.Errorf("unknown binding argument")
					}
					if entry.query {
						for typ := range p.types {
							if typ != "string" && typ != "boolean" && typ != "integer" && typ != "number" {
								return nil, fmt.Errorf("query requires scalar")
							}
						}
					}
				} else if entry.query {
					if _, e := scalar(b.Value); e != nil {
						return nil, e
					}
				}
			}
		}
		seen := map[string]bool{}
		for k, v := range r.Headers {
			lower := strings.ToLower(k)
			if !validHeader(k) || !validValue(v) || seen[lower] || c.Auth != nil && strings.EqualFold(k, c.Auth.HeaderName) {
				return nil, fmt.Errorf("invalid or reserved header")
			}
			seen[lower] = true
		}
		if t.Response != nil {
			if _, e := pointerParts(t.Response.Select); e != nil {
				return nil, e
			}
		}
		cat.tools[t.Name] = &compiledTool{tool: t, schema: s}
		cat.public = append(cat.public, basemcp.Tool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
	}
	b, _ := json.Marshal(basemcp.ToolListResult{Tools: cat.public})
	if len(b) > 1<<20 {
		return nil, fmt.Errorf("tool catalog exceeds 1 MiB")
	}
	return cat, nil
}
func scalar(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case bool:
		if x {
			return "true", nil
		}
		return "false", nil
	case json.Number:
		if _, ok := number(x); ok {
			return string(x), nil
		}
	}
	return "", fmt.Errorf("query requires non-null scalar")
}
func pointerParts(p string) ([]string, error) {
	if p == "" {
		return nil, nil
	}
	if !strings.HasPrefix(p, "/") {
		return nil, fmt.Errorf("invalid JSON pointer")
	}
	parts := strings.Split(p[1:], "/")
	for i, s := range parts {
		for j := 0; j < len(s); j++ {
			if s[j] == '~' {
				j++
				if j >= len(s) || (s[j] != '0' && s[j] != '1') {
					return nil, fmt.Errorf("invalid pointer escaping")
				}
			}
		}
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(s, "~1", "/"), "~0", "~")
	}
	return parts, nil
}
func (c *Catalog) Tools() []basemcp.Tool {
	b, _ := json.Marshal(c.public)
	var out []basemcp.Tool
	_ = strictjson.Decode(b, &out, 1<<20, 64)
	return out
}

// Header constants are owned by the executor, never copied from ingress.
var _ = http.MethodGet
