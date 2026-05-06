package scheduler

import (
	"time"

	"github.com/agent-guide/caddy-agent-gateway/llm/credentialmgr/model"
)

type Credential = model.Credential
type ManagedCredential = model.ManagedCredential
type QuotaState = model.QuotaState
type ModelState = model.ModelState
type Error = model.Error

type Result struct {
	CredentialID string
	ProviderType string
	Model        string
	Success      bool
	RetryAfter   *time.Duration
	Error        *Error
}

type Filter struct {
	Source       string
	ProviderType string
	ProviderID   string
	Model        string
}
