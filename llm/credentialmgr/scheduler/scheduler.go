package scheduler

import (
	"context"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CredentialScheduler manages an in-memory set of credentials and selects
// the best available one for each request.
type CredentialScheduler interface {
	// Pick selects the best available credential for the given provider type and model.
	// tried is an optional set of credential IDs that have already been attempted.
	Pick(ctx context.Context, providerType, model string, tried map[string]struct{}) (*Credential, error)

	// SetStrategy replaces the active selection strategy.
	SetStrategy(s CredentialSelector)

	// RegisterCredential adds a new credential to the scheduler.
	RegisterCredential(cred *Credential)

	// UpdateCredential synchronizes updated credential state into the scheduler.
	UpdateCredential(cred *Credential)

	// DeregisterCredential removes a credential from the scheduler.
	DeregisterCredential(id string)

	// Rebuild recreates the complete scheduler state from a credential snapshot.
	Rebuild(creds []*Credential)
}

// NewScheduler constructs a CredentialScheduler with the given selection strategy.
// If strategy is nil, RoundRobinSelector is used.
func NewScheduler(strategy CredentialSelector) CredentialScheduler {
	if strategy == nil {
		strategy = &RoundRobinSelector{}
	}
	return &authScheduler{
		strategy:      strategy,
		providers:     make(map[string]*providerScheduler),
		credProviders: make(map[string]string),
	}
}

// scheduledState describes how a credential participates in a model shard.
type scheduledState int

const (
	scheduledStateReady    scheduledState = iota
	scheduledStateCooldown                // quota exceeded, known reset time
	scheduledStateBlocked                 // unavailable, unknown reset or other
	scheduledStateDisabled                // intentionally disabled
)

type authScheduler struct {
	mu            sync.Mutex
	strategy      CredentialSelector
	providers     map[string]*providerScheduler
	credProviders map[string]string // credID -> providerTypeKey
}

type providerScheduler struct {
	creds       map[string]*Credential
	modelShards map[string]*modelScheduler
}

type modelScheduler struct {
	modelKey        string
	entries         map[string]*scheduledCred
	priorityOrder   []int
	readyByPriority map[int]*ReadyBucket
	blocked         []*scheduledCred
}

type scheduledCred struct {
	cred        *Credential
	priority    int
	state       scheduledState
	nextRetryAt time.Time
}

func (s *authScheduler) SetStrategy(strategy CredentialSelector) {
	if s == nil {
		return
	}
	if strategy == nil {
		strategy = &RoundRobinSelector{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.strategy = strategy
}

func (s *authScheduler) Rebuild(creds []*Credential) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.providers = make(map[string]*providerScheduler)
	s.credProviders = make(map[string]string)
	now := time.Now()
	for _, cred := range creds {
		s.upsertLocked(cred, now)
	}
}

func (s *authScheduler) RegisterCredential(cred *Credential) {
	s.upsert(cred)
}

func (s *authScheduler) UpdateCredential(cred *Credential) {
	s.upsert(cred)
}

func (s *authScheduler) upsert(cred *Credential) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upsertLocked(cred, time.Now())
}

func (s *authScheduler) DeregisterCredential(id string) {
	if s == nil {
		return
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeLocked(id)
}

func (s *authScheduler) Pick(_ context.Context, providerType, model string, tried map[string]struct{}) (*Credential, error) {
	if s == nil {
		return nil, &Error{Code: "credential_not_found", Message: "no credential available"}
	}
	providerKey := strings.ToLower(strings.TrimSpace(providerType))
	modelKey := canonicalModelKey(model)

	s.mu.Lock()
	defer s.mu.Unlock()

	ps := s.providers[providerKey]
	if ps == nil {
		return nil, &Error{Code: "credential_not_found", Message: "no credential available"}
	}

	shard := ps.ensureModelLocked(modelKey, time.Now())
	if shard == nil {
		return nil, &Error{Code: "credential_not_found", Message: "no credential available"}
	}

	predicate := func(cred *Credential) bool {
		if cred == nil {
			return false
		}
		if len(tried) > 0 {
			if _, ok := tried[cred.ID]; ok {
				return false
			}
		}
		return true
	}

	if picked := shard.pickReadyLocked(s.strategy, predicate); picked != nil {
		return picked, nil
	}
	return nil, shard.unavailableErrorLocked(providerType, model, predicate)
}

func (s *authScheduler) upsertLocked(cred *Credential, now time.Time) {
	if cred == nil {
		return
	}
	credID := strings.TrimSpace(cred.ID)
	providerKey := strings.ToLower(strings.TrimSpace(cred.ProviderType))
	if credID == "" || providerKey == "" || cred.IsDisabled() {
		s.removeLocked(credID)
		return
	}
	if prev := s.credProviders[credID]; prev != "" && prev != providerKey {
		if prevPS := s.providers[prev]; prevPS != nil {
			prevPS.removeLocked(credID)
		}
	}
	s.credProviders[credID] = providerKey
	s.ensureProviderLocked(providerKey).upsertLocked(cred, now)
}

func (s *authScheduler) removeLocked(credID string) {
	if credID == "" {
		return
	}
	if providerKey := s.credProviders[credID]; providerKey != "" {
		if ps := s.providers[providerKey]; ps != nil {
			ps.removeLocked(credID)
		}
		delete(s.credProviders, credID)
	}
}

func (s *authScheduler) ensureProviderLocked(providerKey string) *providerScheduler {
	ps := s.providers[providerKey]
	if ps == nil {
		ps = &providerScheduler{
			creds:       make(map[string]*Credential),
			modelShards: make(map[string]*modelScheduler),
		}
		s.providers[providerKey] = ps
	}
	return ps
}

func (p *providerScheduler) upsertLocked(cred *Credential, now time.Time) {
	if p == nil || cred == nil {
		return
	}
	p.creds[cred.ID] = cred
	for _, shard := range p.modelShards {
		if shard != nil {
			shard.upsertEntryLocked(cred, now)
		}
	}
}

func (p *providerScheduler) removeLocked(credID string) {
	if p == nil || credID == "" {
		return
	}
	delete(p.creds, credID)
	for _, shard := range p.modelShards {
		if shard != nil {
			shard.removeEntryLocked(credID)
		}
	}
}

func (p *providerScheduler) ensureModelLocked(modelKey string, now time.Time) *modelScheduler {
	if p == nil {
		return nil
	}
	modelKey = canonicalModelKey(modelKey)
	if shard, ok := p.modelShards[modelKey]; ok && shard != nil {
		shard.promoteExpiredLocked(now)
		return shard
	}
	shard := &modelScheduler{
		modelKey:        modelKey,
		entries:         make(map[string]*scheduledCred),
		readyByPriority: make(map[int]*ReadyBucket),
	}
	for _, cred := range p.creds {
		if cred != nil {
			shard.upsertEntryLocked(cred, now)
		}
	}
	p.modelShards[modelKey] = shard
	return shard
}

func (m *modelScheduler) upsertEntryLocked(cred *Credential, now time.Time) {
	if m == nil || cred == nil {
		return
	}
	entry, ok := m.entries[cred.ID]
	if !ok || entry == nil {
		entry = &scheduledCred{}
		m.entries[cred.ID] = entry
	}
	prevCred := entry.cred
	prevState := entry.state
	prevNext := entry.nextRetryAt
	prevPriority := entry.priority

	entry.cred = cred
	entry.priority = credentialPriority(cred)
	entry.nextRetryAt = time.Time{}

	blocked, reason, next := isCredentialBlockedForModel(cred, m.modelKey, now)
	switch {
	case !blocked:
		entry.state = scheduledStateReady
	case reason == blockReasonCooldown:
		entry.state = scheduledStateCooldown
		entry.nextRetryAt = next
	case reason == blockReasonDisabled:
		entry.state = scheduledStateDisabled
	default:
		entry.state = scheduledStateBlocked
		entry.nextRetryAt = next
	}

	// Rebuild indexes only when something changed.
	if ok && prevCred == cred && prevState == entry.state && prevNext.Equal(entry.nextRetryAt) && prevPriority == entry.priority {
		return
	}
	m.rebuildIndexesLocked()
}

func (m *modelScheduler) removeEntryLocked(credID string) {
	if m == nil || credID == "" {
		return
	}
	if _, ok := m.entries[credID]; !ok {
		return
	}
	delete(m.entries, credID)
	m.rebuildIndexesLocked()
}

func (m *modelScheduler) promoteExpiredLocked(now time.Time) {
	if m == nil || len(m.blocked) == 0 {
		return
	}
	changed := false
	for _, entry := range m.blocked {
		if entry == nil || entry.cred == nil || entry.nextRetryAt.IsZero() || entry.nextRetryAt.After(now) {
			continue
		}
		blocked, reason, next := isCredentialBlockedForModel(entry.cred, m.modelKey, now)
		switch {
		case !blocked:
			entry.state = scheduledStateReady
			entry.nextRetryAt = time.Time{}
		case reason == blockReasonCooldown:
			entry.state = scheduledStateCooldown
			entry.nextRetryAt = next
		case reason == blockReasonDisabled:
			entry.state = scheduledStateDisabled
			entry.nextRetryAt = time.Time{}
		default:
			entry.state = scheduledStateBlocked
			entry.nextRetryAt = next
		}
		changed = true
	}
	if changed {
		m.rebuildIndexesLocked()
	}
}

func (m *modelScheduler) pickReadyLocked(strategy CredentialSelector, predicate func(*Credential) bool) *Credential {
	if m == nil {
		return nil
	}
	m.promoteExpiredLocked(time.Now())

	// Find the highest priority bucket with a matching ready credential.
	bestPriority := 0
	found := false
	for _, p := range m.priorityOrder {
		bucket := m.readyByPriority[p]
		if bucket == nil {
			continue
		}
		if hasMatch(bucket, predicate) {
			if !found || p > bestPriority {
				bestPriority = p
				found = true
			}
		}
	}
	if !found {
		return nil
	}
	return strategy.PickFromBucket(m.readyByPriority[bestPriority], predicate)
}

func hasMatch(bucket *ReadyBucket, predicate func(*Credential) bool) bool {
	for _, cred := range bucket.creds {
		if predicate == nil || predicate(cred) {
			return true
		}
	}
	return false
}

func (m *modelScheduler) unavailableErrorLocked(providerType, model string, predicate func(*Credential) bool) error {
	now := time.Now()
	total := 0
	cooldownCount := 0
	earliest := time.Time{}
	for _, entry := range m.entries {
		if predicate != nil && !predicate(entry.cred) {
			continue
		}
		total++
		if entry.state != scheduledStateCooldown {
			continue
		}
		cooldownCount++
		if !entry.nextRetryAt.IsZero() && (earliest.IsZero() || entry.nextRetryAt.Before(earliest)) {
			earliest = entry.nextRetryAt
		}
	}
	if total == 0 {
		return &Error{Code: "credential_not_found", Message: "no credential available"}
	}
	if cooldownCount == total && !earliest.IsZero() {
		resetIn := earliest.Sub(now)
		if resetIn < 0 {
			resetIn = 0
		}
		return &cooldownError{model: model, providerType: providerType, resetIn: formatDuration(resetIn)}
	}
	return &Error{Code: "credential_unavailable", Message: "no credential available"}
}

func formatDuration(d time.Duration) string {
	secs := int(math.Ceil(d.Seconds()))
	if secs <= 0 {
		return "0s"
	}
	return strconv.Itoa(secs) + "s"
}

func (m *modelScheduler) rebuildIndexesLocked() {
	m.readyByPriority = make(map[int]*ReadyBucket)
	m.priorityOrder = m.priorityOrder[:0]
	m.blocked = m.blocked[:0]

	byPriority := make(map[int][]*scheduledCred)
	for _, entry := range m.entries {
		if entry == nil || entry.cred == nil {
			continue
		}
		switch entry.state {
		case scheduledStateReady:
			p := entry.priority
			byPriority[p] = append(byPriority[p], entry)
		case scheduledStateCooldown, scheduledStateBlocked:
			m.blocked = append(m.blocked, entry)
		}
	}

	for priority, entries := range byPriority {
		sort.Slice(entries, func(i, j int) bool { return entries[i].cred.ID < entries[j].cred.ID })
		creds := make([]*Credential, len(entries))
		for i, e := range entries {
			creds[i] = e.cred
		}
		m.readyByPriority[priority] = &ReadyBucket{creds: creds}
		m.priorityOrder = append(m.priorityOrder, priority)
	}
	sort.Slice(m.priorityOrder, func(i, j int) bool { return m.priorityOrder[i] > m.priorityOrder[j] })
	sort.Slice(m.blocked, func(i, j int) bool {
		l, r := m.blocked[i], m.blocked[j]
		if l == nil || r == nil {
			return l != nil
		}
		if l.nextRetryAt.Equal(r.nextRetryAt) {
			return l.cred.ID < r.cred.ID
		}
		if l.nextRetryAt.IsZero() {
			return false
		}
		if r.nextRetryAt.IsZero() {
			return true
		}
		return l.nextRetryAt.Before(r.nextRetryAt)
	})
}
