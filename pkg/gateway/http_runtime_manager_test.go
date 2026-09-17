package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	a2acard "github.com/agent-guide/agent-gateway/pkg/a2a/card"
	agentpkg "github.com/agent-guide/agent-gateway/pkg/agent"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	configschema "github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	configstoresqlite "github.com/agent-guide/agent-gateway/pkg/configstore/sqlite"
	"github.com/agent-guide/agent-gateway/pkg/credential"
)

func TestHTTPRuntimeManagerReusesCardAndUsesLiveCredential(t *testing.T) {
	var mu sync.Mutex
	cardRequests := 0
	var authorizations []string
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/card":
			mu.Lock()
			cardRequests++
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, "{\"name\":\"Remote\",\"description\":\"\",\"version\":\"1\",\"capabilities\":{\"streaming\":false},"+
				"\"defaultInputModes\":[\"text/plain\"],\"defaultOutputModes\":[\"text/plain\"],\"skills\":[],"+
				"\"supportedInterfaces\":[{\"url\":%q,\"protocolBinding\":\"JSONRPC\",\"protocolVersion\":\"1.0\",\"tenant\":\"tenant-a\"}],"+
				"\"securitySchemes\":{\"bearer\":{\"httpAuthSecurityScheme\":{\"scheme\":\"Bearer\"}}},"+
				"\"securityRequirements\":[{\"schemes\":{\"bearer\":[]}}]}", server.URL+"/a2a")
		case "/a2a":
			mu.Lock()
			authorizations = append(authorizations, r.Header.Get("Authorization"))
			mu.Unlock()
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				return
			}
			id, _ := request["id"].(string)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, "{\"jsonrpc\":\"2.0\",\"id\":%q,\"result\":{\"message\":{\"messageId\":\"m1\",\"role\":\"ROLE_AGENT\",\"parts\":[{\"text\":\"ok\"}]}}}", id)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	credentials := credential.NewManager(nil)
	cred := &credential.Credential{
		ID: "remote-key", Type: credential.TypeAPIKey, Scope: credential.HTTPAgentCredentialScope("remote"),
		Attributes: map[string]string{"api_key": "first"},
	}
	if err := credentials.RegisterCredential(context.Background(), cred); err != nil {
		t.Fatal(err)
	}
	manager := NewHTTPRuntimeManager(nil, credentials, server.Client(), nil)
	agent := testHTTPAgent("remote", server.URL+"/card", "remote-key")
	manager.RefreshRuntimeConfigs(context.Background(), []agentpkg.Agent{agent})
	first, err := manager.ResolveExecution("remote")
	if err != nil {
		t.Fatal(err)
	}
	if first.Interface.Tenant != "tenant-a" || first.Streaming {
		t.Fatalf("execution = %#v", first)
	}
	send := func() {
		t.Helper()
		_, err := first.Client.SendMessage(context.Background(), &a2a.SendMessageRequest{
			Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hello")),
		})
		if err != nil {
			t.Fatalf("SendMessage() error = %v", err)
		}
	}
	send()
	rotated := cred.Clone()
	rotated.Attributes["api_key"] = "second"
	if err := credentials.UpdateCredential(context.Background(), rotated); err != nil {
		t.Fatal(err)
	}
	manager.RefreshRuntimeConfigs(context.Background(), []agentpkg.Agent{agent})
	second, err := manager.ResolveExecution("remote")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("secret-only rotation replaced execution handle")
	}
	send()
	mu.Lock()
	defer mu.Unlock()
	if cardRequests != 1 {
		t.Fatalf("Card requests = %d, want 1", cardRequests)
	}
	if len(authorizations) != 2 || authorizations[0] != "Bearer first" || authorizations[1] != "Bearer second" {
		t.Fatalf("Authorization values = %#v", authorizations)
	}
}

func TestHTTPRuntimeManagerRejectsWrongCredentialOwnerWithoutRefetch(t *testing.T) {
	cardRequests := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		cardRequests++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, "{\"name\":\"Remote\",\"description\":\"\",\"version\":\"1\",\"capabilities\":{},"+
			"\"defaultInputModes\":[\"text/plain\"],\"defaultOutputModes\":[\"text/plain\"],\"skills\":[],"+
			"\"supportedInterfaces\":[{\"url\":%q,\"protocolBinding\":\"JSONRPC\",\"protocolVersion\":\"1.0\"}],"+
			"\"securitySchemes\":{\"bearer\":{\"httpAuthSecurityScheme\":{\"scheme\":\"Bearer\"}}},"+
			"\"securityRequirements\":[{\"schemes\":{\"bearer\":[]}}]}", server.URL+"/a2a")
	}))
	defer server.Close()
	credentials := credential.NewManager(nil)
	if err := credentials.RegisterCredential(context.Background(), &credential.Credential{
		ID: "wrong", Type: credential.TypeAPIKey, Scope: credential.HTTPAgentCredentialScope("other"),
		Attributes: map[string]string{"api_key": "secret"},
	}); err != nil {
		t.Fatal(err)
	}
	manager := NewHTTPRuntimeManager(nil, credentials, server.Client(), nil)
	agent := testHTTPAgent("remote", server.URL+"/card", "wrong")
	manager.RefreshRuntimeConfigs(context.Background(), []agentpkg.Agent{agent})
	if _, err := manager.ResolveExecution("remote"); err == nil {
		t.Fatal("wrong-owner credential made agent executable")
	}
	manager.RefreshRuntimeConfigs(context.Background(), []agentpkg.Agent{agent})
	if cardRequests != 1 {
		t.Fatalf("Card requests = %d, want cached Card reuse", cardRequests)
	}
}

func TestHTTPRuntimeManagerHealthIsConditionalAndCoalesced(t *testing.T) {
	requests := 0
	conditional := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("If-None-Match") == "\"v1\"" {
			conditional++
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", "\"v1\"")
		writeTestAgentCard(w, server.URL+"/a2a", false)
	}))
	defer server.Close()
	manager := NewHTTPRuntimeManager(nil, nil, server.Client(), nil)
	agent := testHTTPAgent("remote", server.URL+"/card", "")
	manager.RefreshRuntimeConfigs(context.Background(), []agentpkg.Agent{agent})
	first := manager.ProbeHealth(context.Background(), "remote")
	second := manager.ProbeHealth(context.Background(), "remote")
	if !first.Healthy || !second.Healthy || first.Drift || second.Drift {
		t.Fatalf("health probes = %#v, %#v", first, second)
	}
	if requests != 2 || conditional != 1 {
		t.Fatalf("requests = %d, conditional = %d", requests, conditional)
	}
}

func TestHTTPRuntimeManagerDisabledAgentDoesNotFetchCard(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	manager := NewHTTPRuntimeManager(nil, nil, server.Client(), nil)
	agent := testHTTPAgent("remote", server.URL+"/card", "")
	agent.Disabled = true
	manager.RefreshRuntimeConfigs(context.Background(), []agentpkg.Agent{agent})
	if requests.Load() != 0 {
		t.Fatalf("Card requests = %d, want 0", requests.Load())
	}
	if probe := manager.ProbeHealth(context.Background(), agent.ID); probe.Healthy || probe.Message != "HTTP runtime is disabled" {
		t.Fatalf("ProbeHealth() = %#v", probe)
	}
}

func TestHTTPRuntimeManagerSlowCardQueueDoesNotDegradeInheritedAgent(t *testing.T) {
	var healthyRequests atomic.Int64
	var inFlight atomic.Int64
	var maxInFlight atomic.Int64
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthy-card" {
			healthyRequests.Add(1)
			writeTestAgentCard(w, server.URL+"/a2a", false)
			return
		}
		current := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			maximum := maxInFlight.Load()
			if current <= maximum || maxInFlight.CompareAndSwap(maximum, current) {
				break
			}
		}
		<-r.Context().Done()
	}))
	defer server.Close()

	manager := NewHTTPRuntimeManager(nil, nil, server.Client(), nil)
	healthy := testHTTPAgent("healthy", server.URL+"/healthy-card", "")
	manager.RefreshRuntimeConfigs(context.Background(), []agentpkg.Agent{healthy})
	original, err := manager.ResolveExecution(healthy.ID)
	if err != nil {
		t.Fatal(err)
	}

	agents := make([]agentpkg.Agent, 0, 9)
	for i := range 8 {
		agents = append(agents, testHTTPAgent(fmt.Sprintf("slow-%d", i), fmt.Sprintf("%s/slow-card-%d", server.URL, i), ""))
	}
	// Put the inherited entry after enough slow entries to fill two waves of
	// the four-slot Card fetch pool. It must never enter that queue.
	agents = append(agents, healthy)
	prepareCtx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	commit := manager.PrepareRuntimeConfigs(prepareCtx, agents)
	cleanup := commit()
	cleanup(context.Background())

	inherited, err := manager.ResolveExecution(healthy.ID)
	if err != nil {
		t.Fatalf("inherited healthy execution degraded: %v", err)
	}
	if inherited != original {
		t.Fatal("inherited healthy execution was replaced")
	}
	if healthyRequests.Load() != 1 {
		t.Fatalf("healthy Card requests = %d, want 1", healthyRequests.Load())
	}
	if maxInFlight.Load() > httpPrepareConcurrency {
		t.Fatalf("concurrent Card requests = %d, limit %d", maxInFlight.Load(), httpPrepareConcurrency)
	}
	for i := range 8 {
		entry := manager.entries[fmt.Sprintf("slow-%d", i)]
		if !entry.cardFetchFailed || entry.cardInputFingerprint == "" || entry.definitionInputFingerprint == "" {
			t.Fatalf("slow-%d entry lost retry state: %#v", i, entry)
		}
	}
}

func TestHTTPRuntimeManagerHealthReportsConfigError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	manager := NewHTTPRuntimeManager(nil, nil, server.Client(), nil)
	manager.RefreshRuntimeConfigs(context.Background(), []agentpkg.Agent{testHTTPAgent("remote", server.URL+"/card", "")})
	probe := manager.ProbeHealth(context.Background(), "remote")
	if probe.Healthy || !strings.Contains(probe.Message, "Agent Card HTTP status 503") {
		t.Fatalf("ProbeHealth() = %#v", probe)
	}
}

func TestHTTPRuntimeManagerHealthAllowsUnrelatedConcurrentProbes(t *testing.T) {
	entered := make(chan string, 2)
	release := make(chan struct{})
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") != "" {
			entered <- r.URL.Path
			<-release
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", "\"v1\"")
		writeTestAgentCard(w, server.URL+"/a2a", false)
	}))
	defer server.Close()
	manager := NewHTTPRuntimeManager(nil, nil, server.Client(), nil)
	manager.RefreshRuntimeConfigs(context.Background(), []agentpkg.Agent{
		testHTTPAgent("a", server.URL+"/card-a", ""),
		testHTTPAgent("b", server.URL+"/card-b", ""),
	})
	results := make(chan HTTPHealthProbe, 2)
	go func() { results <- manager.ProbeHealth(context.Background(), "a") }()
	go func() { results <- manager.ProbeHealth(context.Background(), "b") }()
	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case path := <-entered:
			seen[path] = true
		case <-time.After(time.Second):
			t.Fatal("unrelated health probes were serialized")
		}
	}
	close(release)
	for range 2 {
		if probe := <-results; !probe.Healthy {
			t.Fatalf("ProbeHealth() = %#v", probe)
		}
	}
}

func TestHTTPRuntimeManagerPrunesRetiredHealthCache(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") != "" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", "\"v1\"")
		writeTestAgentCard(w, server.URL+"/a2a", false)
	}))
	defer server.Close()
	manager := NewHTTPRuntimeManager(nil, nil, server.Client(), nil)
	manager.RefreshRuntimeConfigs(context.Background(), []agentpkg.Agent{testHTTPAgent("remote", server.URL+"/card", "")})
	if probe := manager.ProbeHealth(context.Background(), "remote"); !probe.Healthy {
		t.Fatalf("ProbeHealth() = %#v", probe)
	}
	if len(manager.health) != 1 {
		t.Fatalf("health cache size = %d, want 1", len(manager.health))
	}
	manager.RefreshRuntimeConfigs(context.Background(), nil)
	if len(manager.health) != 0 {
		t.Fatalf("health cache size after retirement = %d, want 0", len(manager.health))
	}
}

func TestHTTPRuntimeManagerSelectionReportsSecurityReason(t *testing.T) {
	manager := NewHTTPRuntimeManager(nil, nil, nil, nil)
	_, _, err := manager.selectExecution("remote", "https://agent.example/card", "", &a2acard.Snapshot{
		Interfaces: []a2a.AgentInterface{{
			URL: "https://agent.example/a2a", ProtocolBinding: a2a.TransportProtocolJSONRPC, ProtocolVersion: a2a.Version,
		}},
		Security: []a2acard.SecurityAlternative{{
			Kind: a2acard.SecurityUnsupported, SchemeName: "oauth", Reason: "scheme is not HTTP Bearer",
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "oauth: scheme is not HTTP Bearer") {
		t.Fatalf("selectExecution() error = %v", err)
	}
}

func TestHTTPRuntimeManagerNormalizesDefaultOriginPorts(t *testing.T) {
	manager := NewHTTPRuntimeManager(nil, nil, nil, nil)
	snapshot := &a2acard.Snapshot{
		Interfaces: []a2a.AgentInterface{{URL: "https://agent.example:443/a2a"}},
		Security:   []a2acard.SecurityAlternative{{Kind: a2acard.SecurityAnonymous}},
	}
	selected, _, err := manager.selectExecution("remote", "https://agent.example/card", "", snapshot)
	if err != nil || selected.URL != "https://agent.example:443/a2a" {
		t.Fatalf("default-port selection = %#v, %v", selected, err)
	}
	snapshot.Interfaces[0].URL = "https://agent.example:444/a2a"
	if _, _, err := manager.selectExecution("remote", "https://agent.example/card", "", snapshot); err == nil {
		t.Fatal("different effective port was accepted as same-origin")
	}
}

func TestHTTPAgentCredentialOwnerComparisonIsCaseSensitiveAndFailClosed(t *testing.T) {
	cred := &credential.ManagedCredential{Credential: credential.Credential{
		ID: "remote-key", Type: credential.TypeAPIKey, Scope: credential.HTTPAgentCredentialScope("Agent-A"),
		Attributes: map[string]string{"api_key": "secret"},
	}}
	if !credentialUsableForAgent(cred, "Agent-A") {
		t.Fatal("credential rejected for its exact owner")
	}
	if credentialUsableForAgent(cred, "agent-a") {
		t.Fatal("credential borrowed by case-distinct Agent")
	}
	if credentialUsableForAgent(cred, "") {
		t.Fatal("credential accepted for an empty Agent id")
	}
}

func TestHTTPRuntimeFingerprintMarshalFailureIsDeterministic(t *testing.T) {
	card := &a2a.AgentCard{
		SecuritySchemes: a2a.NamedSecuritySchemes{"invalid": nil},
	}
	if _, err := checkedFingerprint(card); err == nil {
		t.Fatal("checkedFingerprint() accepted an unencodable Agent Card")
	}
	first := fingerprint(card)
	if second := fingerprint(card); first != second {
		t.Fatalf("fingerprint() fallback is unstable: %q != %q", first, second)
	}
}

func TestHTTPRuntimeManagerFailedCardRecoversInBackground(t *testing.T) {
	var ready atomic.Bool
	var requests atomic.Int64
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		if !ready.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		writeTestAgentCard(w, server.URL+"/a2a", false)
	}))
	defer server.Close()

	store, err := configstore.OpenBackend(t.Context(), "sqlite", configstoresqlite.Config{SQLitePath: t.TempDir() + "/config.db"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := configschema.RegisterDefaultStores(store); err != nil {
		t.Fatal(err)
	}
	gateway := NewAgentGateway()
	if err := gateway.Bootstrap(t.Context(), BootstrapOptions{ConfigStoreBackend: store}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gateway.Close)
	if err := gateway.AgentManager().Create(t.Context(), testHTTPAgent("remote", server.URL+"/card", "")); err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.HTTPRuntimeManager().ResolveExecution("remote"); err == nil {
		t.Fatal("failed Card unexpectedly produced a ready execution")
	}
	disabled := testHTTPAgent("disabled", server.URL+"/disabled-card", "")
	disabled.Disabled = true
	if err := gateway.AgentManager().Create(t.Context(), disabled); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("Card requests after unrelated CRUD = %d, want failed Card backoff to suppress refetch", requests.Load())
	}
	ready.Store(true)
	deadline := time.Now().Add(4 * time.Second)
	for {
		if _, err := gateway.HTTPRuntimeManager().ResolveExecution("remote"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("HTTP runtime did not recover through background Recommit")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if requests.Load() < 2 {
		t.Fatalf("Card requests = %d, want initial attempt plus retry", requests.Load())
	}
}

func TestHTTPRuntimeManagerQueuesRecommitArrivingInFlight(t *testing.T) {
	store, err := configstore.OpenBackend(t.Context(), "sqlite", configstoresqlite.Config{SQLitePath: t.TempDir() + "/config.db"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := configschema.RegisterDefaultStores(store); err != nil {
		t.Fatal(err)
	}
	gateway := NewAgentGateway()
	if err := gateway.Bootstrap(t.Context(), BootstrapOptions{ConfigStoreBackend: store}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gateway.Close)

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var calls atomic.Int64
	gateway.AgentManager().AddDefinitionListener(func(ctx context.Context, _ []agentpkg.Agent) agentpkg.DefinitionCommit {
		if calls.Add(1) == 1 {
			close(firstStarted)
			select {
			case <-releaseFirst:
			case <-ctx.Done():
			}
		}
		return nil
	})
	manager := gateway.HTTPRuntimeManager()
	manager.requestRecommit()
	<-firstStarted
	manager.requestRecommit()
	close(releaseFirst)
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("definition listener calls = %d, want queued second Recommit", calls.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func testHTTPAgent(id, cardURL, authRef string) agentpkg.Agent {
	return agentpkg.Agent{
		ID: id, Name: id,
		Runtime: agentpkg.Runtime{Type: agentpkg.RuntimeTypeHTTP, HTTP: &agentpkg.HTTPRuntime{
			CardURL: cardURL, Protocol: "a2a", AuthRef: authRef,
		}},
	}
}
