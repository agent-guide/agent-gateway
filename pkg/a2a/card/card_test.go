package card

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

func TestFetchClassifiesCardAndFiltersInterfaces(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/json" || r.Header.Get("Authorization") != "" || r.Header.Get("A2A-Version") != "1.0" {
			t.Errorf("unexpected request headers: %#v", r.Header)
		}
		w.Header().Set("Content-Type", "Application/JSON; Charset=UTF-8")
		w.Header().Set("ETag", "\"v1\"")
		fmt.Fprintf(w, "{\"name\":\"remote\",\"description\":\"\",\"version\":\"1\",\"capabilities\":{\"streaming\":true},"+
			"\"defaultInputModes\":[\"text/plain\"],\"defaultOutputModes\":[\"text/plain\"],\"skills\":[],"+
			"\"supportedInterfaces\":[{\"url\":\"https://ignored.example/a2a\",\"protocolBinding\":\"REST\",\"protocolVersion\":\"1.0\"},"+
			"{\"url\":%q,\"protocolBinding\":\"JSONRPC\",\"protocolVersion\":\"1.0\",\"tenant\":\"tenant-a\"},"+
			"{\"url\":%q,\"protocolBinding\":\"JSON-RPC\",\"protocolVersion\":\"1.0\"}],"+
			"\"securitySchemes\":{\"bearer\":{\"httpAuthSecurityScheme\":{\"scheme\":\"Bearer\"}}},"+
			"\"securityRequirements\":[{\"schemes\":{\"missing\":[]}},{\"schemes\":{\"bearer\":[]}}]}",
			server.URL+"/a2a", server.URL+"/wrong")
	}))
	defer server.Close()

	got, notModified, err := Fetch(context.Background(), server.Client(), server.URL+"/card", Validators{})
	if err != nil || notModified {
		t.Fatalf("Fetch() = %#v, %v, %v", got, notModified, err)
	}
	if len(got.Interfaces) != 1 || got.Interfaces[0].URL != server.URL+"/a2a" || got.Interfaces[0].Tenant != "tenant-a" {
		t.Fatalf("interfaces = %#v", got.Interfaces)
	}
	if len(got.Security) != 2 || got.Security[0].Kind != SecurityUnsupported || got.Security[1].Kind != SecurityBearer {
		t.Fatalf("security = %#v", got.Security)
	}
	if got.Security[0].Reason != "unknown security scheme name" {
		t.Fatalf("dangling scheme reason = %q", got.Security[0].Reason)
	}
	if got.ETag != "\"v1\"" {
		t.Fatalf("etag = %q", got.ETag)
	}
}

func TestFetchRejectsOversizedCard(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, strings.Repeat("x", int(MaxBytes+1)))
	}))
	defer server.Close()
	if _, _, err := Fetch(context.Background(), server.Client(), server.URL, Validators{}); err == nil || !strings.Contains(err.Error(), "Agent Card exceeds") {
		t.Fatalf("Fetch() error = %v", err)
	}
}

func TestFetchAcceptsNotModifiedContentLengthMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") != `"v1"` {
			t.Errorf("If-None-Match = %q", r.Header.Get("If-None-Match"))
		}
		w.Header().Set("Content-Length", "1234")
		w.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()

	snapshot, notModified, err := Fetch(context.Background(), server.Client(), server.URL, Validators{ETag: `"v1"`})
	if err != nil || !notModified || snapshot != nil {
		t.Fatalf("Fetch() = %#v, %v, %v", snapshot, notModified, err)
	}
}

func TestFetchRejectsRedirectAndTrailingJSON(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "{}{}")
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.RedirectHandler(target.URL, http.StatusFound))
	defer redirect.Close()
	if _, _, err := Fetch(context.Background(), redirect.Client(), redirect.URL, Validators{}); err == nil {
		t.Fatal("redirect accepted")
	}
	if _, _, err := Fetch(context.Background(), target.Client(), target.URL, Validators{}); err == nil {
		t.Fatal("trailing JSON accepted")
	}
}

func TestDecodeCardReportsMalformedTrailingInput(t *testing.T) {
	var card a2a.AgentCard
	err := decodeCard([]byte(`{} {`), &card)
	if err == nil || !strings.Contains(err.Error(), "decode trailing Agent Card input") {
		t.Fatalf("decodeCard() error = %v", err)
	}
}

func TestRewritePublicCardReplacesInterfacesSecurityAndSignatures(t *testing.T) {
	remote := &a2a.AgentCard{
		Name: "remote", Description: "remote description", Version: "1", Capabilities: a2a.AgentCapabilities{
			Streaming: true, PushNotifications: true, ExtendedAgentCard: true,
		},
		SupportedInterfaces:  []*a2a.AgentInterface{{URL: "https://remote.example/rpc", ProtocolBinding: a2a.TransportProtocolJSONRPC, ProtocolVersion: a2a.Version}},
		SecuritySchemes:      a2a.NamedSecuritySchemes{"remote": a2a.HTTPAuthSecurityScheme{Scheme: "Bearer"}},
		SecurityRequirements: a2a.SecurityRequirementsOptions{{"remote": {}}},
		Signatures:           []a2a.AgentCardSignature{{}},
		Skills:               []a2a.AgentSkill{{ID: "verify", Name: "Verify", Description: "verify", SecurityRequirements: a2a.SecurityRequirementsOptions{{"remote": {}}}}},
	}
	template, err := PublicTemplate(remote, "gateway-agent", "gateway description")
	if err != nil {
		t.Fatal(err)
	}
	public, err := RewritePublicCard(template, "https://gateway.example/agents/verify", "tenant-a", true)
	if err != nil {
		t.Fatal(err)
	}
	if public.Name != "gateway-agent" || public.Description != "gateway description" {
		t.Fatalf("identity = %q / %q", public.Name, public.Description)
	}
	if len(public.SupportedInterfaces) != 1 || public.SupportedInterfaces[0].URL != "https://gateway.example/agents/verify" || public.SupportedInterfaces[0].Tenant != "tenant-a" {
		t.Fatalf("interfaces = %#v", public.SupportedInterfaces)
	}
	if public.Capabilities.PushNotifications || public.Capabilities.ExtendedAgentCard || !public.Capabilities.Streaming {
		t.Fatalf("capabilities = %#v", public.Capabilities)
	}
	if len(public.Signatures) != 0 || len(public.SecuritySchemes) != 1 || len(public.SecurityRequirements) != 1 || len(public.Skills[0].SecurityRequirements) != 1 {
		t.Fatalf("rewritten security/signatures = schemes:%#v requirements:%#v skill:%#v signatures:%#v", public.SecuritySchemes, public.SecurityRequirements, public.Skills[0].SecurityRequirements, public.Signatures)
	}
	if len(remote.Signatures) != 1 || len(remote.SupportedInterfaces) != 1 {
		t.Fatal("rewrite mutated remote Card")
	}
}
