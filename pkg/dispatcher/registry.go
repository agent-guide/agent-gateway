package dispatcher

import (
	"sort"
	"strings"
	"sync"
)

var (
	llmAPIHandlerMu    sync.RWMutex
	llmAPIHandlerTypes = map[string]struct{}{}
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

func normalizeLLMAPIHandlerType(handlerType string) string {
	return strings.ToLower(strings.TrimSpace(handlerType))
}
