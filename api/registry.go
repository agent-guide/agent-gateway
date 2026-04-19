package api

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// ErrLLMApiHandlerNameDisabled is returned when a registered LLM API handler type is disabled.
var ErrLLMApiHandlerNameDisabled = errors.New("llm api handler name is disabled")

var (
	llmAPIHandlerMu            sync.RWMutex
	llmAPIHandlerNames         = map[string]struct{}{}
	disabledLLMAPIHandlerNames = map[string]struct{}{}
)

// RegisterLLMApiHandlerName registers an available LLM API handler by name.
func RegisterLLMApiHandlerName(name string) {
	name = normalizeLLMAPIHandlerName(name)
	if name == "" {
		return
	}
	llmAPIHandlerMu.Lock()
	llmAPIHandlerNames[name] = struct{}{}
	llmAPIHandlerMu.Unlock()
}

// ListLLMApiHandlerNames returns the names of all registered LLM API handlers.
func ListLLMApiHandlerNames() []string {
	llmAPIHandlerMu.RLock()
	defer llmAPIHandlerMu.RUnlock()
	names := make([]string, 0, len(llmAPIHandlerNames))
	for name := range llmAPIHandlerNames {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// IsLLMApiHandlerNameEnabled reports whether a registered LLM API handler is enabled.
func IsLLMApiHandlerNameEnabled(name string) (bool, bool) {
	name = normalizeLLMAPIHandlerName(name)
	llmAPIHandlerMu.RLock()
	defer llmAPIHandlerMu.RUnlock()
	if _, ok := llmAPIHandlerNames[name]; !ok {
		return false, false
	}
	_, disabled := disabledLLMAPIHandlerNames[name]
	return !disabled, true
}

// EnableLLMApiHandlerName enables a registered LLM API handler.
func EnableLLMApiHandlerName(name string) error {
	name = normalizeLLMAPIHandlerName(name)
	llmAPIHandlerMu.Lock()
	defer llmAPIHandlerMu.Unlock()
	if _, ok := llmAPIHandlerNames[name]; !ok {
		return fmt.Errorf("unknown llm api handler: %s", name)
	}
	delete(disabledLLMAPIHandlerNames, name)
	return nil
}

// DisableLLMApiHandlerName disables a registered LLM API handler.
func DisableLLMApiHandlerName(name string) error {
	name = normalizeLLMAPIHandlerName(name)
	llmAPIHandlerMu.Lock()
	defer llmAPIHandlerMu.Unlock()
	if _, ok := llmAPIHandlerNames[name]; !ok {
		return fmt.Errorf("unknown llm api handler: %s", name)
	}
	disabledLLMAPIHandlerNames[name] = struct{}{}
	return nil
}

func normalizeLLMAPIHandlerName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
