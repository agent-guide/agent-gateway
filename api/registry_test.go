package api

import "testing"

func TestLLMApiHandlerRegistryEnableDisableType(t *testing.T) {
	const handlerType = "test-registry-llm-api-handler"
	RegisterLLMApiHandlerType(handlerType)
	defer func() {
		if err := EnableLLMApiHandlerType(handlerType); err != nil {
			t.Fatalf("restore llm api handler type: %v", err)
		}
	}()

	enabled, ok := IsLLMApiHandlerTypeEnabled(handlerType)
	if !ok {
		t.Fatalf("llm api handler type %q not registered", handlerType)
	}
	if !enabled {
		t.Fatalf("llm api handler type %q enabled = false, want true", handlerType)
	}

	if err := DisableLLMApiHandlerType(handlerType); err != nil {
		t.Fatalf("disable llm api handler type: %v", err)
	}
	enabled, ok = IsLLMApiHandlerTypeEnabled(handlerType)
	if !ok || enabled {
		t.Fatalf("llm api handler type state after disable: enabled=%v registered=%v", enabled, ok)
	}

	if err := EnableLLMApiHandlerType(handlerType); err != nil {
		t.Fatalf("enable llm api handler type: %v", err)
	}
	enabled, ok = IsLLMApiHandlerTypeEnabled(handlerType)
	if !ok || !enabled {
		t.Fatalf("llm api handler type state after enable: enabled=%v registered=%v", enabled, ok)
	}
}
