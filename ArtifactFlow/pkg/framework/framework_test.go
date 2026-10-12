package framework

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveGGUFModelPathSearchesConfiguredDirectoryParent(t *testing.T) {
	root := t.TempDir()
	mainDir := filepath.Join(root, "Gemma-4-E4B")
	embeddingDir := filepath.Join(root, "Qwen3-Embedding-0.6B")
	if err := os.MkdirAll(mainDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(embeddingDir, 0755); err != nil {
		t.Fatal(err)
	}
	embeddingPath := filepath.Join(embeddingDir, "Qwen3-Embedding-0.6B-f16.gguf")
	if err := os.WriteFile(embeddingPath, []byte("gguf"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLAMACPP_MODEL_PATH", mainDir)
	t.Setenv("APPDATA", filepath.Join(root, "appdata"))

	got := resolveGGUFModelPath("Qwen3-Embedding-0.6B-f16.gguf")
	if got != embeddingPath {
		t.Fatalf("expected sibling embedding model path %q, got %q", embeddingPath, got)
	}
}

func TestResolveGGUFModelPathPrefersDirectConfiguredDirectoryMatch(t *testing.T) {
	root := t.TempDir()
	modelDir := filepath.Join(root, "Gemma-4-E4B")
	if err := os.MkdirAll(modelDir, 0755); err != nil {
		t.Fatal(err)
	}
	modelPath := filepath.Join(modelDir, "Gemma-4-E4B-Q8_K_P.gguf")
	if err := os.WriteFile(modelPath, []byte("gguf"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLAMACPP_MODEL_PATH", modelDir)
	t.Setenv("APPDATA", filepath.Join(root, "appdata"))

	got := resolveGGUFModelPath("Gemma-4-E4B-Q8_K_P.gguf")
	if got != modelPath {
		t.Fatalf("expected direct model path %q, got %q", modelPath, got)
	}
}
