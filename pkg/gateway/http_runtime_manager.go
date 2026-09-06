package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/agent-guide/agent-gateway/internal/observability/usage"
	a2acard "github.com/agent-guide/agent-gateway/pkg/a2a/card"
	a2aclient "github.com/agent-guide/agent-gateway/pkg/a2a/client"
	agentpkg "github.com/agent-guide/agent-gateway/pkg/agent"
	"github.com/agent-guide/agent-gateway/pkg/credential"
	"go.uber.org/zap"
)

const (
	httpDefaultTurnTimeout = 120 * time.Second
	httpCardTimeout        = 10 * time.Second
	httpConnectTimeout     = 10 * time.Second
	httpHeaderTimeout      = 30 * time.Second
	httpIdleTimeout        = 60 * time.Second
	httpPrepareConcurrency = 4
)

// HTTPExecution is one immutable, Card-selected Path B execution view. Its
// client contains a live credential transport and never retains a secret.
type HTTPExecution struct {
	Client      *a2aclient.Client
	Interface   a2a.AgentInterface
	Card        *a2a.AgentCard
	AuthRef     string
	Timeout     time.Duration
	Fingerprint string
	Streaming   bool
	bindings    *httpSessionBindings
	runs        *httpRunSlots
}

type httpRuntimeEntry struct {
	cardInputFingerprint       string
	credentialFingerprint      string
	definitionInputFingerprint string
	executionFingerprint       string
	card                       *a2acard.Snapshot
	execution                  *HTTPExecution
	authRef                    string
	cardURL                    string
	configError                string
	disabled                   bool
}

// HTTPRuntimeManager owns the single immutable Card-derived generation used by
// HTTP Agent execution. It is both an Agent definition listener and a
// credential lifecycle listener.
type HTTPRuntimeManager struct {
	agents      *agentpkg.Manager
	credentials *credential.Manager
	cardClient  *http.Client
	logger      *zap.Logger

	mu         sync.RWMutex
	entries    map[string]httpRuntimeEntry
	reverse    map[string][]string
	recommitMu sync.Mutex
	recommit   bool
	healthMu   sync.Mutex
	health     map[string]HTTPHealthProbe
}

func NewHTTPRuntimeManager(agents *agentpkg.Manager, credentials *credential.Manager, cardClient *http.Client, logger *zap.Logger) *HTTPRuntimeManager {
	if cardClient == nil {
		cardClient = newHTTPTransportClient(nil)
	}
	return &HTTPRuntimeManager{
		agents: agents, credentials: credentials, cardClient: cardClient, logger: logger,
		entries: map[string]httpRuntimeEntry{}, reverse: map[string][]string{}, health: map[string]HTTPHealthProbe{},
	}
}

func (m *HTTPRuntimeManager) PrepareRuntimeConfigs(ctx context.Context, agents []agentpkg.Agent) agentpkg.DefinitionCommit {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	previous := make(map[string]httpRuntimeEntry, len(m.entries))
	for id, entry := range m.entries {
		previous[id] = entry
	}
	m.mu.RUnlock()

	next := make(map[string]httpRuntimeEntry)
	var nextMu sync.Mutex
	sem := make(chan struct{}, httpPrepareConcurrency)
	var wg sync.WaitGroup
	for _, agent := range agents {
		if agent.Runtime.Type != agentpkg.RuntimeTypeHTTP || agent.Runtime.HTTP == nil {
			continue
		}
		agent := agent
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				nextMu.Lock()
				next[agent.ID] = httpRuntimeEntry{configError: "HTTP runtime preparation timed out", disabled: agent.Disabled}
				nextMu.Unlock()
				return
			}
			entry := m.prepareEntry(ctx, agent, previous[agent.ID])
			nextMu.Lock()
			next[agent.ID] = entry
			nextMu.Unlock()
		}()
	}
	wg.Wait()

	return func() agentpkg.DefinitionCleanup {
		m.mu.Lock()
		old := m.entries
		m.entries = next
		m.reverse = buildHTTPReverseIndex(next)
		m.mu.Unlock()
		var retired []*HTTPExecution
		for id, oldEntry := range old {
			nextEntry, ok := next[id]
			if oldEntry.execution != nil && (!ok || nextEntry.execution != oldEntry.execution) {
				retired = append(retired, oldEntry.execution)
			}
		}
		return func(context.Context) {
			for _, execution := range retired {
				if execution.Client != nil {
					_ = execution.Client.Close()
				}
			}
		}
	}
}

func (m *HTTPRuntimeManager) RefreshRuntimeConfigs(ctx context.Context, agents []agentpkg.Agent) {
	commit := m.PrepareRuntimeConfigs(ctx, agents)
	if commit == nil {
		return
	}
	if cleanup := commit(); cleanup != nil {
		cleanup(context.WithoutCancel(ctx))
	}
}

func (m *HTTPRuntimeManager) prepareEntry(ctx context.Context, agent agentpkg.Agent, previous httpRuntimeEntry) httpRuntimeEntry {
	cfg := agent.Runtime.HTTP
	entry := httpRuntimeEntry{authRef: cfg.AuthRef, cardURL: cfg.CardURL, disabled: agent.Disabled}
	entry.cardInputFingerprint = fingerprint(struct {
		CardURL  string
		Protocol string
		Limits   [5]int64
	}{cfg.CardURL, cfg.Protocol, [5]int64{int64(httpConnectTimeout), int64(httpHeaderTimeout), int64(httpIdleTimeout), a2acard.MaxBytes, a2aclient.MaxBodyBytes}})
	entry.credentialFingerprint = m.credentialEligibilityFingerprint(agent.ID, cfg.AuthRef)
	entry.definitionInputFingerprint = fingerprint(struct {
		Card, Credential, ID, Name, Description string
	}{entry.cardInputFingerprint, entry.credentialFingerprint, agent.ID, agent.Name, agent.Description})

	if previous.card != nil && previous.cardInputFingerprint == entry.cardInputFingerprint {
		entry.card = previous.card
	} else {
		fetchCtx, cancel := context.WithTimeout(ctx, httpCardTimeout)
		cardSnapshot, _, err := a2acard.Fetch(fetchCtx, m.cardClient, cfg.CardURL, a2acard.Validators{})
		cancel()
		if err != nil {
			entry.configError = boundedConfigError("fetch Agent Card", err)
			return entry
		}
		entry.card = cardSnapshot
	}

	selectedInterface, selectedSecurity, err := m.selectExecution(agent.ID, cfg.CardURL, cfg.AuthRef, entry.card)
	if err != nil {
		entry.configError = boundedConfigError("select Agent Card interface", err)
		return entry
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = httpDefaultTurnTimeout
	}
	entry.executionFingerprint = fingerprint(struct {
		Card, Interface, Tenant, Protocol, AuthRef, Credential                        string
		Timeout, Connect, Header, Idle, CardLimit, BodyLimit, EventLimit, StreamLimit int64
	}{
		entry.cardInputFingerprint, selectedInterface.URL, selectedInterface.Tenant, cfg.Protocol, cfg.AuthRef,
		entry.credentialFingerprint + ":" + string(selectedSecurity.Kind),
		int64(timeout), int64(httpConnectTimeout), int64(httpHeaderTimeout), int64(httpIdleTimeout),
		a2acard.MaxBytes, a2aclient.MaxBodyBytes, a2aclient.MaxEventBytes, a2aclient.MaxStreamBytes,
	})
	if previous.execution != nil && previous.executionFingerprint == entry.executionFingerprint {
		entry.execution = previous.execution
		return entry
	}
	httpClient := newHTTPTransportClient(&liveCredentialTransport{
		base: transportOrDefault(nil), manager: m.credentials, authRef: cfg.AuthRef, agentID: agent.ID,
	})
	typedClient, err := a2aclient.New(ctx, selectedInterface, httpClient)
	if err != nil {
		entry.configError = boundedConfigError("create A2A client", err)
		return entry
	}
	entry.execution = &HTTPExecution{
		Client: typedClient, Interface: selectedInterface, Card: entry.card.Card, AuthRef: cfg.AuthRef,
		Timeout: timeout, Fingerprint: entry.executionFingerprint, Streaming: entry.card.Card.Capabilities.Streaming,
		bindings: newHTTPSessionBindings(),
		runs:     newHTTPRunSlots(),
	}
	return entry
}

type HTTPHealthProbe struct {
	Healthy   bool
	Drift     bool
	CheckedAt time.Time
	Message   string
}

func (m *HTTPRuntimeManager) ProbeHealth(ctx context.Context, agentID string) HTTPHealthProbe {
	now := time.Now().UTC()
	if m == nil {
		return HTTPHealthProbe{CheckedAt: now, Message: "HTTP runtime manager is unavailable"}
	}
	m.mu.RLock()
	entry, ok := m.entries[strings.TrimSpace(agentID)]
	m.mu.RUnlock()
	if !ok || entry.card == nil || entry.execution == nil {
		return HTTPHealthProbe{CheckedAt: now, Message: "HTTP runtime is not ready"}
	}
	m.healthMu.Lock()
	defer m.healthMu.Unlock()
	if cached, ok := m.health[entry.executionFingerprint]; ok && now.Sub(cached.CheckedAt) < 30*time.Second {
		return cached
	}
	probeCtx, cancel := context.WithTimeout(ctx, httpCardTimeout)
	defer cancel()
	fetched, notModified, err := a2acard.Fetch(probeCtx, m.cardClient, entry.cardURL, a2acard.Validators{
		ETag: entry.card.ETag, LastModified: entry.card.LastModified,
	})
	probe := HTTPHealthProbe{CheckedAt: now}
	if err != nil {
		probe.Message = boundedConfigError("probe Agent Card", err)
	} else {
		probe.Healthy = true
		if !notModified {
			probe.Drift = fingerprint(fetched.Card) != fingerprint(entry.card.Card)
			if probe.Drift {
				probe.Message = "Agent Card differs from the accepted runtime snapshot"
			}
		}
	}
	m.health[entry.executionFingerprint] = probe
	return probe
}

func (m *HTTPRuntimeManager) selectExecution(agentID, cardURL, authRef string, snapshot *a2acard.Snapshot) (a2a.AgentInterface, a2acard.SecurityAlternative, error) {
	if snapshot == nil {
		return a2a.AgentInterface{}, a2acard.SecurityAlternative{}, fmt.Errorf("Agent Card is unavailable")
	}
	cardOrigin, err := url.Parse(cardURL)
	if err != nil {
		return a2a.AgentInterface{}, a2acard.SecurityAlternative{}, err
	}
	var selectedInterface *a2a.AgentInterface
	for i := range snapshot.Interfaces {
		candidateURL, err := url.Parse(snapshot.Interfaces[i].URL)
		if err == nil && strings.EqualFold(candidateURL.Scheme, cardOrigin.Scheme) && strings.EqualFold(candidateURL.Host, cardOrigin.Host) {
			copy := snapshot.Interfaces[i]
			selectedInterface = &copy
			break
		}
	}
	if selectedInterface == nil {
		return a2a.AgentInterface{}, a2acard.SecurityAlternative{}, fmt.Errorf("no same-origin A2A 1.0 JSONRPC interface")
	}
	eligible := m.eligibleCredential(agentID, authRef)
	for _, alternative := range snapshot.Security {
		if alternative.Kind == a2acard.SecurityAnonymous && authRef == "" {
			return *selectedInterface, alternative, nil
		}
		if alternative.Kind == a2acard.SecurityBearer && authRef != "" && eligible {
			return *selectedInterface, alternative, nil
		}
	}
	return a2a.AgentInterface{}, a2acard.SecurityAlternative{}, fmt.Errorf("no satisfiable anonymous or HTTP Bearer security alternative")
}

func (m *HTTPRuntimeManager) eligibleCredential(agentID, authRef string) bool {
	if authRef == "" || m.credentials == nil {
		return false
	}
	cred := m.credentials.GetCredential(authRef)
	return credentialUsableForAgent(cred, agentID)
}

func credentialUsableForAgent(cred *credential.ManagedCredential, agentID string) bool {
	if cred == nil || cred.Disabled || strings.TrimSpace(cred.APIKey()) == "" {
		return false
	}
	if cred.Type != credential.TypeAPIKey && cred.Type != credential.TypeOAuthToken {
		return false
	}
	return cred.ScopeValue() == credential.HTTPAgentCredentialScope(agentID) && cred.ProviderType == "" && cred.ProviderID == ""
}

func (m *HTTPRuntimeManager) credentialEligibilityFingerprint(agentID, authRef string) string {
	var value any = struct{ AuthRef string }{authRef}
	if authRef != "" && m.credentials != nil {
		cred := m.credentials.GetCredential(authRef)
		if cred != nil {
			value = struct {
				AuthRef, Type, Scope string
				Disabled, HasSecret  bool
			}{authRef, cred.Type, cred.ScopeValue(), cred.Disabled, strings.TrimSpace(cred.APIKey()) != ""}
		}
	}
	return fingerprint(value)
}

func (m *HTTPRuntimeManager) ResolveExecution(agentID string) (*HTTPExecution, error) {
	if m == nil {
		return nil, fmt.Errorf("HTTP runtime manager is unavailable")
	}
	m.mu.RLock()
	entry, ok := m.entries[strings.TrimSpace(agentID)]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("HTTP runtime config is not loaded for this agent")
	}
	if entry.disabled {
		return nil, fmt.Errorf("HTTP runtime is disabled")
	}
	if entry.configError != "" || entry.execution == nil {
		if entry.configError != "" {
			return nil, fmt.Errorf("%s", entry.configError)
		}
		return nil, fmt.Errorf("HTTP runtime is not ready")
	}
	return entry.execution, nil
}

func buildHTTPReverseIndex(entries map[string]httpRuntimeEntry) map[string][]string {
	out := map[string][]string{}
	for agentID, entry := range entries {
		if entry.authRef != "" {
			out[entry.authRef] = append(out[entry.authRef], agentID)
		}
	}
	return out
}

func (m *HTTPRuntimeManager) OnCredentialRegistered(_ context.Context, cred *credential.ManagedCredential) {
	m.credentialChanged(credentialID(cred))
}
func (m *HTTPRuntimeManager) OnCredentialUpdated(_ context.Context, cred *credential.ManagedCredential) {
	m.credentialChanged(credentialID(cred))
}
func (m *HTTPRuntimeManager) OnCredentialDeregistered(_ context.Context, cred *credential.ManagedCredential) {
	m.credentialChanged(credentialID(cred))
}
func (m *HTTPRuntimeManager) OnCredentialsReplaced(context.Context, []*credential.ManagedCredential) {
	m.credentialChanged()
}

func credentialID(cred *credential.ManagedCredential) string {
	if cred == nil {
		return ""
	}
	return cred.ID
}

func (m *HTTPRuntimeManager) credentialChanged(authRefs ...string) {
	if m == nil || m.agents == nil {
		return
	}
	m.mu.RLock()
	type candidate struct {
		agentID string
		entry   httpRuntimeEntry
	}
	var candidates []candidate
	if len(authRefs) == 0 {
		for authRef, agentIDs := range m.reverse {
			for _, agentID := range agentIDs {
				entry := m.entries[agentID]
				entry.authRef = authRef
				candidates = append(candidates, candidate{agentID: agentID, entry: entry})
			}
		}
	} else {
		seen := map[string]struct{}{}
		for _, authRef := range authRefs {
			for _, agentID := range m.reverse[strings.TrimSpace(authRef)] {
				if _, ok := seen[agentID]; ok {
					continue
				}
				seen[agentID] = struct{}{}
				candidates = append(candidates, candidate{agentID: agentID, entry: m.entries[agentID]})
			}
		}
	}
	m.mu.RUnlock()
	changed := false
	for _, candidate := range candidates {
		if candidate.entry.credentialFingerprint != m.credentialEligibilityFingerprint(candidate.agentID, candidate.entry.authRef) {
			changed = true
			break
		}
	}
	if !changed {
		return
	}
	m.recommitMu.Lock()
	if m.recommit {
		m.recommitMu.Unlock()
		return
	}
	m.recommit = true
	m.recommitMu.Unlock()
	go func() {
		defer func() {
			m.recommitMu.Lock()
			m.recommit = false
			m.recommitMu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := m.agents.Recommit(ctx); err != nil && m.logger != nil {
			m.logger.Error("recommit HTTP Agent definitions", zap.Error(err))
		}
	}()
}

type liveCredentialTransport struct {
	base    http.RoundTripper
	manager *credential.Manager
	authRef string
	agentID string
}

func (t *liveCredentialTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	injectHTTPRuntimeTrace(clone)
	if t.authRef == "" {
		return t.base.RoundTrip(clone)
	}
	if t.manager == nil {
		return nil, fmt.Errorf("HTTP Agent credential manager is unavailable")
	}
	cred := t.manager.GetCredential(t.authRef)
	if !credentialUsableForAgent(cred, t.agentID) {
		return nil, fmt.Errorf("HTTP Agent credential is unavailable or has the wrong owner")
	}
	if cred.Type == credential.TypeOAuthToken {
		var err error
		cred, err = t.manager.RefreshCredentialIfNeeded(req.Context(), t.authRef)
		if err != nil {
			return nil, fmt.Errorf("refresh HTTP Agent credential: %w", err)
		}
		if !credentialUsableForAgent(cred, t.agentID) {
			return nil, fmt.Errorf("refreshed HTTP Agent credential is unusable")
		}
	}
	clone.Header.Set("Authorization", "Bearer "+strings.TrimSpace(cred.APIKey()))
	return t.base.RoundTrip(clone)
}

func injectHTTPRuntimeTrace(req *http.Request) {
	if req == nil {
		return
	}
	dims, _ := usage.DimensionsFromContext(req.Context())
	if usage.ValidTraceID(dims.TraceID) && usage.ValidSpanID(dims.SpanID) {
		req.Header.Set("traceparent", "00-"+dims.TraceID+"-"+dims.SpanID+"-01")
		req.Header.Set("X-Trace-ID", dims.TraceID)
		req.Header.Set("X-Span-ID", dims.SpanID)
	}
	req.Header.Set("X-Agent-Depth", strconv.Itoa(dims.AgentDepth+1))
}

func newHTTPTransportClient(rt http.RoundTripper) *http.Client {
	if rt == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.DialContext = (&net.Dialer{Timeout: httpConnectTimeout, KeepAlive: 30 * time.Second}).DialContext
		transport.TLSHandshakeTimeout = httpConnectTimeout
		transport.ResponseHeaderTimeout = httpHeaderTimeout
		transport.DisableCompression = true
		rt = transport
	}
	return &http.Client{Transport: rt}
}

func transportOrDefault(rt http.RoundTripper) http.RoundTripper {
	if rt != nil {
		return rt
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: httpConnectTimeout, KeepAlive: 30 * time.Second}).DialContext
	transport.TLSHandshakeTimeout = httpConnectTimeout
	transport.ResponseHeaderTimeout = httpHeaderTimeout
	transport.DisableCompression = true
	return transport
}

func fingerprint(value any) string {
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func boundedConfigError(prefix string, err error) string {
	message := prefix + ": " + err.Error()
	if len(message) > 512 {
		message = message[:512]
	}
	return message
}
