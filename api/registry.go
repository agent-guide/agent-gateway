package api

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// ErrLLMApiHandlerTypeDisabled is returned when a registered LLM API handler type is disabled.
var ErrLLMApiHandlerTypeDisabled = errors.New("llm api handler type is disabled")

var (
	llmAPIHandlerMu            sync.RWMutex
	llmAPIHandlerTypes         = map[string]struct{}{}
	disabledLLMAPIHandlerTypes = map[string]struct{}{}
)

// RegisterLLMApiHandlerType registers an available LLM API handler type.
func RegisterLLMApiHandlerType(handlerType string) {
	handlerType = normalizeLLMAPIHandlerType(handlerType)
	if handlerType == "" {
		return
	}
	llmAPIHandlerMu.Lock()
	llmAPIHandlerTypes[handlerType] = struct{}{}
	llmAPIHandlerMu.Unlock()
}

// ListLLMApiHandlerTypes returns the types of all registered LLM API handlers.
func ListLLMApiHandlerTypes() []string {
	llmAPIHandlerMu.RLock()
	defer llmAPIHandlerMu.RUnlock()
	types := make([]string, 0, len(llmAPIHandlerTypes))
	for handlerType := range llmAPIHandlerTypes {
		types = append(types, handlerType)
	}
	sort.Strings(types)
	return types
}

// IsLLMApiHandlerTypeEnabled reports whether a registered LLM API handler type is enabled.
func IsLLMApiHandlerTypeEnabled(handlerType string) (bool, bool) {
	handlerType = normalizeLLMAPIHandlerType(handlerType)
	llmAPIHandlerMu.RLock()
	defer llmAPIHandlerMu.RUnlock()
	if _, ok := llmAPIHandlerTypes[handlerType]; !ok {
		return false, false
	}
	_, disabled := disabledLLMAPIHandlerTypes[handlerType]
	return !disabled, true
}

// EnableLLMApiHandlerType enables a registered LLM API handler type.
func EnableLLMApiHandlerType(handlerType string) error {
	handlerType = normalizeLLMAPIHandlerType(handlerType)
	llmAPIHandlerMu.Lock()
	defer llmAPIHandlerMu.Unlock()
	if _, ok := llmAPIHandlerTypes[handlerType]; !ok {
		return fmt.Errorf("unknown llm api handler type: %s", handlerType)
	}
	delete(disabledLLMAPIHandlerTypes, handlerType)
	return nil
}

// DisableLLMApiHandlerType disables a registered LLM API handler type.
func DisableLLMApiHandlerType(handlerType string) error {
	handlerType = normalizeLLMAPIHandlerType(handlerType)
	llmAPIHandlerMu.Lock()
	defer llmAPIHandlerMu.Unlock()
	if _, ok := llmAPIHandlerTypes[handlerType]; !ok {
		return fmt.Errorf("unknown llm api handler type: %s", handlerType)
	}
	disabledLLMAPIHandlerTypes[handlerType] = struct{}{}
	return nil
}

func normalizeLLMAPIHandlerType(handlerType string) string {
	return strings.ToLower(strings.TrimSpace(handlerType))
}
