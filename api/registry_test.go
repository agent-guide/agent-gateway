package api

import "testing"

func TestLLMApiHandlerRegistryEnableDisableName(t *testing.T) {
	const handlerName = "test-registry-llm-api-handler"
	RegisterLLMApiHandlerName(handlerName)
	defer func() {
		if err := EnableLLMApiHandlerName(handlerName); err != nil {
			t.Fatalf("restore llm api handler name: %v", err)
		}
	}()

	enabled, ok := IsLLMApiHandlerNameEnabled(handlerName)
	if !ok {
		t.Fatalf("llm api handler name %q not registered", handlerName)
	}
	if !enabled {
		t.Fatalf("llm api handler name %q enabled = false, want true", handlerName)
	}

	if err := DisableLLMApiHandlerName(handlerName); err != nil {
		t.Fatalf("disable llm api handler name: %v", err)
	}
	enabled, ok = IsLLMApiHandlerNameEnabled(handlerName)
	if !ok || enabled {
		t.Fatalf("llm api handler name state after disable: enabled=%v registered=%v", enabled, ok)
	}

	if err := EnableLLMApiHandlerName(handlerName); err != nil {
		t.Fatalf("enable llm api handler name: %v", err)
	}
	enabled, ok = IsLLMApiHandlerNameEnabled(handlerName)
	if !ok || !enabled {
		t.Fatalf("llm api handler name state after enable: enabled=%v registered=%v", enabled, ok)
	}
}
