package agent

import "testing"

func TestDiscoverModelsIncludesConfiguredLocalLlamaCppModel(t *testing.T) {
	t.Setenv("LOCAL_OLLAMA_URL", "")
	t.Setenv("LMSTUDIO_URL", "")
	t.Setenv("OPENAI_URL", "http://127.0.0.1:8081/v1/chat/completions")
	t.Setenv("OPENAI_MODEL", "Gemma-4-E4B-Q8_K_P.gguf")

	models := DiscoverModels()
	if len(models) != 1 {
		t.Fatalf("expected one discovered model, got %#v", models)
	}
	if models[0].ID != "Gemma-4-E4B-Q8_K_P.gguf" || models[0].Provider != "llamacpp" || models[0].Type != "llm" {
		t.Fatalf("unexpected model metadata: %#v", models[0])
	}
}
