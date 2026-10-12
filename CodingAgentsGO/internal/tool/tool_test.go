package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/user/cognitive-orchestrator/internal/types"
)

func TestFileExplorerRejectsTraversal(t *testing.T) {
	explorer := &FileExplorer{BasePath: t.TempDir()}

	_, err := explorer.Execute(context.Background(), `{"action":"read","path":"../secret.txt"}`)
	if err == nil || !strings.Contains(err.Error(), "outside the allowed workspace") {
		t.Fatalf("expected traversal to be rejected, got %v", err)
	}
}

func TestFileExplorerGlobList(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "one.go"), []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "two.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	explorer := &FileExplorer{BasePath: dir}
	out, err := explorer.Execute(context.Background(), `{"action":"list","path":"*.go"}`)
	if err != nil {
		t.Fatalf("glob list failed: %v", err)
	}
	if !strings.Contains(out, "one.go") || strings.Contains(out, "two.txt") {
		t.Fatalf("unexpected glob output: %s", out)
	}
}

func TestFileWriterRejectsMalformedJSON(t *testing.T) {
	writer := &FileWriter{BasePath: t.TempDir()}

	_, err := writer.Execute(context.Background(), `{"action":"write","path":"x.txt","content":`)
	if err == nil || !strings.Contains(err.Error(), "invalid arguments") {
		t.Fatalf("expected malformed JSON error, got %v", err)
	}
}

func TestFileWriterRejectsAbsolutePath(t *testing.T) {
	writer := &FileWriter{BasePath: t.TempDir()}

	_, err := writer.Execute(context.Background(), `{"action":"write","path":"C:\\Windows\\x.txt","content":"nope"}`)
	if err == nil || !strings.Contains(err.Error(), "outside the allowed workspace") {
		t.Fatalf("expected absolute path to be rejected, got %v", err)
	}
}

type mockEmbeddingProvider struct{}

func (m *mockEmbeddingProvider) GenerateEmbedding(ctx context.Context, text string) ([]float32, error) {
	return []float32{0.1}, nil
}
func (m *mockEmbeddingProvider) GetDimensions() int { return 1 }

type mockVectorStore struct {
	types.VectorStore
	msg types.Message
}

func (m *mockVectorStore) SearchMessages(ctx context.Context, vector []float32, limit int) ([]string, error) {
	return []string{"msg-123"}, nil
}

func (m *mockVectorStore) GetMessage(ctx context.Context, id string) (types.Message, error) {
	if id == "msg-123" {
		return m.msg, nil
	}
	return types.Message{}, fmt.Errorf("not found")
}

func TestMemoryRetrieverTool(t *testing.T) {
	mockMsg := types.Message{
		ID:      "msg-123",
		Role:    types.RoleCoder,
		Content: "This is a past coding memory content.",
		Phase:   "coder",
		Visible: true,
	}
	store := &mockVectorStore{msg: mockMsg}
	emb := &mockEmbeddingProvider{}

	retriever := NewMemoryRetriever(store, emb)

	out, err := retriever.Execute(context.Background(), `{"query":"test query"}`)
	if err != nil {
		t.Fatalf("retriever execution failed: %v", err)
	}

	if !strings.Contains(out, "past coding memory content") {
		t.Fatalf("expected retrieved memory content in output, got: %s", out)
	}

	_, err = retriever.Execute(context.Background(), `{"query":""}`)
	if err != nil {
		t.Fatalf("retriever empty query failed: %v", err)
	}
}

func TestFileExplorerOutlineAndDependencies(t *testing.T) {
	dir := t.TempDir()
	code := `package main

import (
	"fmt"
	"net/http"
	"github.com/ArtifactFlow/ArtifactFlow/pkg/framework"
)

type Config struct {
	Port int
	Host string
}

type Service interface {
	Start() error
}

func (c *Config) GetAddr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

func main() {
	fmt.Println("hello")
}
`
	filePath := filepath.Join(dir, "app.go")
	if err := os.WriteFile(filePath, []byte(code), 0644); err != nil {
		t.Fatal(err)
	}

	explorer := &FileExplorer{BasePath: dir}

	out, err := explorer.Execute(context.Background(), `{"action":"outline","path":"app.go"}`)
	if err != nil {
		t.Fatalf("outline failed: %v", err)
	}
	if !strings.Contains(out, "type Config struct") || !strings.Contains(out, "type Service interface") || !strings.Contains(out, "func (c *Config) GetAddr") || !strings.Contains(out, "func main") {
		t.Fatalf("unexpected outline output:\n%s", out)
	}

	deps, err := explorer.Execute(context.Background(), `{"action":"dependencies","path":"app.go"}`)
	if err != nil {
		t.Fatalf("dependencies failed: %v", err)
	}
	if !strings.Contains(deps, "[STANDARD LIBRARY]") || !strings.Contains(deps, "net/http") || !strings.Contains(deps, "[THIRD PARTY]") || !strings.Contains(deps, "github.com/ArtifactFlow/ArtifactFlow/pkg/framework") {
		t.Fatalf("unexpected dependencies output:\n%s", deps)
	}
}
