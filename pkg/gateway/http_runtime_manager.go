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
	httpHealthCardTimeout  = 10 * time.Second
	httpPrepareCardTimeout = 4 * time.Second
	httpConnectTimeout     = 10 * time.Second
	httpHeaderTimeout      = 30 * time.Second
	httpIdleTimeout        = 60 * time.Second
	httpPrepareConcurrency = 4
	httpCardRetryInitial   = time.Second
	httpCardRetryMaximum   = time.Minute
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
	transport   *http.Transport
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
	cardFetchFailed            bool
}

// HTTPRuntimeManager owns the single immutable Card-derived generation used by
// HTTP Agent execution. It is both an Agent definition listener and a
// credential lifecycle listener.
type HTTPRuntimeManager struct {
	agents      *agentpkg.Manager
	credentials *credential.Manager
	cardClient  *http.Client
	logger      *zap.Logger

	mu              sync.RWMutex
	entries         map[string]httpRuntimeEntry
	reverse         map[string][]string
	recommitMu      sync.Mutex
	recommit        bool
	recommitPending bool
	healthMu        sync.Mutex
	health          map[string]HTTPHealthProbe
	healthInFlight  map[string]*httpHealthCall
	retryMu         sync.Mutex
	retries         map[string]*httpCardRetry
	closed          bool
}

type httpHealthCall struct {
	done  chan struct{}
	probe HTTPHealthProbe
}

type httpCardRetry struct {
	fingerprint string
	attempt     int
	timer       *time.Timer
}

func NewHTTPRuntimeManager(agents *agentpkg.Manager, credentials *credential.Manager, cardClient *http.Client, logger *zap.Logger) *HTTPRuntimeManager {
	if cardClient == nil {
		cardClient = newHTTPTransportClient(nil)
	}
	return &HTTPRuntimeManager{
		agents: agents, credentials: credentials, cardClient: cardClient, logger: logger,
		entries: map[string]httpRuntimeEntry{}, reverse: map[string][]string{},
		health: map[string]HTTPHealthProbe{}, healthInFlight: map[string]*httpHealthCall{}, retries: map[string]*httpCardRetry{},
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
		previousEntry := previous[agent.ID]
		entry, needsCardFetch := m.prepareEntry(ctx, agent, previousEntry, false)
		if !needsCardFetch {
			nextMu.Lock()
			next[agent.ID] = entry
			nextMu.Unlock()
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				entry.configError = "HTTP runtime preparation timed out waiting to fetch Agent Card"
				entry.cardFetchFailed = true
				nextMu.Lock()
				next[agent.ID] = entry
				nextMu.Unlock()
				return
			}
			entry, _ = m.prepareEntry(ctx, agent, previousEntry, true)
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
		m.pruneHealth(next)
		m.syncCardRetries(next)
		var retired []*HTTPExecution
		for id, oldEntry := range old {
			nextEntry, ok := next[id]
			if oldEntry.execution != nil && (!ok || nextEntry.execution != oldEntry.execution) {
				retired = append(retired, oldEntry.execution)
			}
		}
		return func(cleanupCtx context.Context) {
			for _, execution := range retired {
				execution.runs.retire(cleanupCtx, execution)
				if execution.Client != nil {
					_ = execution.Client.Close()
				}
				if execution.transport != nil {
					execution.transport.CloseIdleConnections()
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

// prepareEntry performs all local preparation first. When allowCardFetch is
// false it reports whether the caller must enter the bounded Card-I/O pool.
func (m *HTTPRuntimeManager) prepareEntry(ctx context.Context, agent agentpkg.Agent, previous httpRuntimeEntry, allowCardFetch bool) (httpRuntimeEntry, bool) {
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
	if agent.Disabled {
		return entry, false
	}
	if previous.cardFetchFailed && previous.cardInputFingerprint == entry.cardInputFingerprint && m.cardRetryPending(agent.ID, entry.cardInputFingerprint) {
		entry.cardFetchFailed = true
		entry.configError = previous.configError
		return entry, false
	}

	if previous.card != nil && previous.cardInputFingerprint == entry.cardInputFingerprint {
		entry.card = previous.card
	} else {
		if !allowCardFetch {
			return entry, true
		}
		fetchCtx, cancel := context.WithTimeout(ctx, httpPrepareCardTimeout)
		cardSnapshot, _, err := a2acard.Fetch(fetchCtx, m.cardClient, cfg.CardURL, a2acard.Validators{})
		cancel()
		if err != nil {
			entry.configError = boundedConfigError("fetch Agent Card", err)
			entry.cardFetchFailed = true
			return entry, false
		}
		entry.card = cardSnapshot
	}

	selectedInterface, selectedSecurity, err := m.selectExecution(agent.ID, cfg.CardURL, cfg.AuthRef, entry.card)
	if err != nil {
		entry.configError = boundedConfigError("select Agent Card interface", err)
		return entry, false
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
		return entry, false
	}
	transport := newHTTPTransport()
	httpClient := newHTTPTransportClient(&liveCredentialTransport{
		base: transport, manager: m.credentials, authRef: cfg.AuthRef, agentID: agent.ID,
	})
	typedClient, err := a2aclient.New(ctx, selectedInterface, httpClient)
	if err != nil {
		entry.configError = boundedConfigError("create A2A client", err)
		return entry, false
	}
	entry.execution = &HTTPExecution{
		Client: typedClient, Interface: selectedInterface, Card: entry.card.Card, AuthRef: cfg.AuthRef,
		Timeout: timeout, Fingerprint: entry.executionFingerprint, Streaming: entry.card.Card.Capabilities.Streaming,
		bindings:  newHTTPSessionBindings(),
		runs:      newHTTPRunSlots(),
		transport: transport,
	}
	return entry, false
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
	if !ok {
		return HTTPHealthProbe{CheckedAt: now, Message: "HTTP runtime is not configured"}
	}
	if entry.disabled {
		return HTTPHealthProbe{CheckedAt: now, Message: "HTTP runtime is disabled"}
	}
	if entry.card == nil || entry.execution == nil {
		message := entry.configError
		if message == "" {
			message = "HTTP runtime is not ready"
		}
		return HTTPHealthProbe{CheckedAt: now, Message: message}
	}
	if err := ctx.Err(); err != nil {
		return HTTPHealthProbe{CheckedAt: now, Message: boundedConfigError("probe Agent Card", err)}
	}
	m.healthMu.Lock()
	if cached, ok := m.health[entry.executionFingerprint]; ok && now.Sub(cached.CheckedAt) < 30*time.Second {
		m.healthMu.Unlock()
		return cached
	}
	if call := m.healthInFlight[entry.executionFingerprint]; call != nil {
		m.healthMu.Unlock()
		return waitForHTTPHealthProbe(ctx, now, call)
	}
	call := &httpHealthCall{done: make(chan struct{})}
	m.healthInFlight[entry.executionFingerprint] = call
	m.healthMu.Unlock()
	go m.runHealthProbe(agentID, entry, call)
	return waitForHTTPHealthProbe(ctx, now, call)
}

func waitForHTTPHealthProbe(ctx context.Context, checkedAt time.Time, call *httpHealthCall) HTTPHealthProbe {
	select {
	case <-call.done:
		return call.probe
	case <-ctx.Done():
		return HTTPHealthProbe{CheckedAt: checkedAt, Message: boundedConfigError("probe Agent Card", ctx.Err())}
	}
}

func (m *HTTPRuntimeManager) runHealthProbe(agentID string, entry httpRuntimeEntry, call *httpHealthCall) {
	probeCtx, cancel := context.WithTimeout(context.Background(), httpHealthCardTimeout)
	defer cancel()
	fetched, notModified, err := a2acard.Fetch(probeCtx, m.cardClient, entry.cardURL, a2acard.Validators{
		ETag: entry.card.ETag, LastModified: entry.card.LastModified,
	})
	probe := HTTPHealthProbe{CheckedAt: time.Now().UTC()}
	if err != nil {
		probe.Message = boundedConfigError("probe Agent Card", err)
	} else {
		probe.Healthy = true
		if !notModified {
			fetchedFingerprint, fetchedErr := checkedFingerprint(fetched.Card)
			acceptedFingerprint, acceptedErr := checkedFingerprint(entry.card.Card)
			if fetchedErr != nil || acceptedErr != nil {
				fingerprintErr := fetchedErr
				if fingerprintErr == nil {
					fingerprintErr = acceptedErr
				}
				probe.Healthy = false
				probe.Message = boundedConfigError("fingerprint Agent Card", fingerprintErr)
			} else if fetchedFingerprint != acceptedFingerprint {
				probe.Drift = true
				probe.Message = "Agent Card differs from the accepted runtime snapshot"
			}
		}
	}
	m.healthMu.Lock()
	if m.executionFingerprintCurrent(agentID, entry.executionFingerprint) {
		m.health[entry.executionFingerprint] = probe
	}
	call.probe = probe
	delete(m.healthInFlight, entry.executionFingerprint)
	close(call.done)
	m.healthMu.Unlock()
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
		if err == nil && sameHTTPOrigin(candidateURL, cardOrigin) {
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
	var reasons []string
	for _, alternative := range snapshot.Security {
		if alternative.Reason != "" {
			reason := alternative.Reason
			if alternative.SchemeName != "" {
				reason = alternative.SchemeName + ": " + reason
			}
			reasons = append(reasons, reason)
		}
	}
	message := "no satisfiable anonymous or HTTP Bearer security alternative"
	if len(reasons) > 0 {
		message += " (" + strings.Join(reasons, "; ") + ")"
	}
	return a2a.AgentInterface{}, a2acard.SecurityAlternative{}, fmt.Errorf("%s", message)
}

func (m *HTTPRuntimeManager) eligibleCredential(agentID, authRef string) bool {
	if authRef == "" || m.credentials == nil {
		return false
	}
	cred := m.credentials.GetCredential(authRef)
	return credentialUsableForAgent(cred, agentID)
}

func credentialUsableForAgent(cred *credential.ManagedCredential, agentID string) bool {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || cred == nil || cred.Disabled || strings.TrimSpace(cred.APIKey()) == "" {
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

func (m *HTTPRuntimeManager) executionFingerprintCurrent(agentID, fingerprint string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.entries[strings.TrimSpace(agentID)]
	return ok && entry.executionFingerprint == fingerprint && entry.execution != nil
}

func (m *HTTPRuntimeManager) pruneHealth(entries map[string]httpRuntimeEntry) {
	current := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.execution != nil && entry.executionFingerprint != "" {
			current[entry.executionFingerprint] = struct{}{}
		}
	}
	m.healthMu.Lock()
	for fingerprint := range m.health {
		if _, ok := current[fingerprint]; !ok {
			delete(m.health, fingerprint)
		}
	}
	m.healthMu.Unlock()
}

func (m *HTTPRuntimeManager) syncCardRetries(entries map[string]httpRuntimeEntry) {
	m.retryMu.Lock()
	defer m.retryMu.Unlock()
	if m.closed {
		return
	}
	for agentID, retry := range m.retries {
		entry, ok := entries[agentID]
		if !ok || entry.disabled || !entry.cardFetchFailed || entry.cardInputFingerprint != retry.fingerprint {
			if retry.timer != nil {
				retry.timer.Stop()
			}
			delete(m.retries, agentID)
		}
	}
	if m.agents == nil {
		// Tests and embedders may intentionally omit the Agent manager. Snapshot
		// preparation still works, but there is no owner that can Recommit it.
		return
	}
	for agentID, entry := range entries {
		if entry.disabled || !entry.cardFetchFailed {
			continue
		}
		retry := m.retries[agentID]
		if retry == nil || retry.fingerprint != entry.cardInputFingerprint {
			retry = &httpCardRetry{fingerprint: entry.cardInputFingerprint}
			m.retries[agentID] = retry
		}
		if retry.timer != nil {
			continue
		}
		delay := httpCardRetryDelay(retry.attempt)
		retry.attempt++
		fingerprint := retry.fingerprint
		retry.timer = time.AfterFunc(delay, func() { m.retryCard(agentID, fingerprint) })
	}
}

func (m *HTTPRuntimeManager) cardRetryPending(agentID, fingerprint string) bool {
	m.retryMu.Lock()
	defer m.retryMu.Unlock()
	retry := m.retries[agentID]
	return !m.closed && retry != nil && retry.fingerprint == fingerprint && retry.timer != nil
}

func httpCardRetryDelay(attempt int) time.Duration {
	delay := httpCardRetryInitial
	for i := 0; i < attempt && delay < httpCardRetryMaximum; i++ {
		delay *= 2
		if delay > httpCardRetryMaximum {
			return httpCardRetryMaximum
		}
	}
	return delay
}

func (m *HTTPRuntimeManager) retryCard(agentID, fingerprint string) {
	m.retryMu.Lock()
	retry := m.retries[agentID]
	if m.closed || retry == nil || retry.fingerprint != fingerprint {
		m.retryMu.Unlock()
		return
	}
	retry.timer = nil
	m.retryMu.Unlock()
	m.requestRecommit()
}

func (m *HTTPRuntimeManager) ensureCardRetriesScheduled() {
	m.retryMu.Lock()
	defer m.retryMu.Unlock()
	if m.closed {
		return
	}
	for agentID, retry := range m.retries {
		if retry.timer != nil {
			continue
		}
		delay := httpCardRetryDelay(retry.attempt)
		retry.attempt++
		fingerprint := retry.fingerprint
		retry.timer = time.AfterFunc(delay, func() { m.retryCard(agentID, fingerprint) })
	}
}

func (m *HTTPRuntimeManager) Close() {
	if m == nil {
		return
	}
	m.retryMu.Lock()
	m.closed = true
	for agentID, retry := range m.retries {
		if retry.timer != nil {
			retry.timer.Stop()
		}
		delete(m.retries, agentID)
	}
	m.retryMu.Unlock()
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
	m.requestRecommit()
}

func (m *HTTPRuntimeManager) requestRecommit() {
	if m == nil || m.agents == nil {
		return
	}
	m.recommitMu.Lock()
	m.retryMu.Lock()
	closed := m.closed
	m.retryMu.Unlock()
	if closed {
		m.recommitMu.Unlock()
		return
	}
	if m.recommit {
		m.recommitPending = true
		m.recommitMu.Unlock()
		return
	}
	m.recommit = true
	m.recommitMu.Unlock()
	go func() {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := m.agents.Recommit(ctx)
			cancel()
			if err != nil && m.logger != nil {
				m.logger.Error("recommit HTTP Agent definitions", zap.Error(err))
			}
			m.recommitMu.Lock()
			m.retryMu.Lock()
			closed := m.closed
			m.retryMu.Unlock()
			if m.recommitPending && !closed {
				m.recommitPending = false
				m.recommitMu.Unlock()
				continue
			}
			m.recommitPending = false
			m.recommit = false
			m.recommitMu.Unlock()
			m.ensureCardRetriesScheduled()
			return
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

func sameHTTPOrigin(left, right *url.URL) bool {
	if left == nil || right == nil || !strings.EqualFold(left.Scheme, right.Scheme) || !strings.EqualFold(left.Hostname(), right.Hostname()) {
		return false
	}
	return effectiveHTTPPort(left) == effectiveHTTPPort(right)
}

func effectiveHTTPPort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
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
		rt = newHTTPTransport()
	}
	return &http.Client{Transport: rt}
}

func newHTTPTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = newHTTPDialer().DialContext
	transport.TLSHandshakeTimeout = httpConnectTimeout
	transport.ResponseHeaderTimeout = httpHeaderTimeout
	transport.DisableCompression = true
	return transport
}

func newHTTPDialer() *net.Dialer {
	return &net.Dialer{Timeout: httpConnectTimeout, KeepAlive: 30 * time.Second}
}

func fingerprint(value any) string {
	fingerprint, err := checkedFingerprint(value)
	if err == nil {
		return fingerprint
	}
	// Configuration fingerprints only receive JSON-safe scalar structs. Keep a
	// deterministic fail-closed fallback in case that invariant is violated.
	data := []byte(fmt.Sprintf("%T|marshal-error:%v", value, err))
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func checkedFingerprint(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func boundedConfigError(prefix string, err error) string {
	message := prefix + ": " + err.Error()
	if len(message) > 512 {
		message = message[:512]
	}
	return message
}
