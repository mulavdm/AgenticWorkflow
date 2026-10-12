package memory

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	afstate "github.com/ArtifactFlow/ArtifactFlow/pkg/state"
)

func TestArtifactFlowReconciliation(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_blackboard.db")
	board, err := afstate.NewBlackboard(dbPath)
	if err != nil {
		t.Fatalf("failed to create blackboard: %v", err)
	}
	defer board.Close()

	store := NewArtifactFlowStore(board)
	defer store.Close()
	vectorStore := store.GetVectorStore()

	ctx := context.Background()
	msgID := "msg-test-1"
	runID := "run-test-1"

	// 1. Save message
	msg := afstate.Message{
		ID:        msgID,
		RunID:     runID,
		NodeID:    "planner",
		Role:      "planner",
		Model:     "test-model",
		Content:   "This is a secret key: 12345.",
		Visible:   true,
		CreatedAt: time.Now(),
	}
	if err := board.SaveMessage(msg); err != nil {
		t.Fatalf("failed to save message: %v", err)
	}

	// 2. Index vector
	embedding := []float32{0.1, 0.2, 0.3}
	metadata := map[string]string{
		"run_id":  runID,
		"content": msg.Content,
	}
	if err := vectorStore.IndexMessage(ctx, msgID, embedding, metadata); err != nil {
		t.Fatalf("failed to index message: %v", err)
	}

	// 3. Search messages (should match)
	ids, err := vectorStore.SearchMessages(ctx, embedding, 5)
	if err != nil {
		t.Fatalf("failed to search messages: %v", err)
	}
	if len(ids) != 1 || ids[0] != msgID {
		t.Fatalf("expected search to return msg-test-1, got %v", ids)
	}

	// 4. Hide message
	if err := board.UpdateMessageVisibility(msgID, false); err != nil {
		t.Fatalf("failed to update message visibility: %v", err)
	}

	// 5. Search messages again (should exclude hidden message)
	ids, err = vectorStore.SearchMessages(ctx, embedding, 5)
	if err != nil {
		t.Fatalf("failed to search messages: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("expected search to exclude hidden message, but got %v", ids)
	}
}

func TestCodebaseVectorStoreRetrieval(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_blackboard_code.db")
	board, err := afstate.NewBlackboard(dbPath)
	if err != nil {
		t.Fatalf("failed to create blackboard: %v", err)
	}
	defer board.Close()

	store := NewArtifactFlowStore(board)
	defer store.Close()
	vectorStore := store.GetVectorStore()

	ctx := context.Background()
	chunkID := "code-internal/foo.go-func-Test"
	embedding := []float32{0.5, 0.5, 0.5}
	metadata := map[string]string{
		"role":    "code",
		"phase":   "internal/foo.go",
		"content": "func Test() {}",
	}

	// 1. Index code chunk (NOT stored in messages table)
	if err := vectorStore.IndexMessage(ctx, chunkID, embedding, metadata); err != nil {
		t.Fatalf("failed to index codebase chunk: %v", err)
	}

	// 2. Search codebase chunks (should bypass messages table cross-validation)
	ids, err := vectorStore.SearchMessages(ctx, embedding, 5)
	if err != nil {
		t.Fatalf("failed to search: %v", err)
	}
	if len(ids) != 1 || ids[0] != chunkID {
		t.Fatalf("expected search to return %q, got %v", chunkID, ids)
	}

	// 3. GetMessage should load metadata content directly for code chunks
	msg, err := vectorStore.GetMessage(ctx, chunkID)
	if err != nil {
		t.Fatalf("failed to get message: %v", err)
	}
	if msg.ID != chunkID || msg.Content != "func Test() {}" || string(msg.Role) != "code" || msg.Phase != "internal/foo.go" {
		t.Fatalf("unexpected message content/role/metadata: %+v", msg)
	}

	// 4. DeletePrefix should delete the vector
	if err := vectorStore.DeletePrefix(ctx, "code-internal/"); err != nil {
		t.Fatalf("failed to delete prefix: %v", err)
	}

	// 5. Search again (should be empty)
	ids, err = vectorStore.SearchMessages(ctx, embedding, 5)
	if err != nil {
		t.Fatalf("failed to search: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("expected search to be empty after delete, got %v", ids)
	}
}

func TestArtifactFlowVectorStoreSearchByKindSeparatesCodeAndMemory(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_blackboard_kind.db")
	board, err := afstate.NewBlackboard(dbPath)
	if err != nil {
		t.Fatalf("failed to create blackboard: %v", err)
	}
	defer board.Close()

	store := NewArtifactFlowStore(board)
	defer store.Close()
	vectorStore := store.GetVectorStore().(*ArtifactFlowVectorStore)

	ctx := context.Background()
	embedding := []float32{1, 0, 0}
	if err := board.SaveMessage(afstate.Message{ID: "msg-memory", RunID: "run-1", Role: "planner", Content: "memory", Visible: true}); err != nil {
		t.Fatalf("failed to save memory message: %v", err)
	}
	if err := vectorStore.IndexMessage(ctx, "msg-memory", embedding, map[string]string{"role": "planner", "content": "memory"}); err != nil {
		t.Fatalf("failed to index memory: %v", err)
	}
	if err := vectorStore.IndexMessage(ctx, "code-internal/foo.go-func-Test", embedding, map[string]string{"role": "code", "phase": "internal/foo.go", "content": "func Test() {}"}); err != nil {
		t.Fatalf("failed to index code: %v", err)
	}

	codeIDs, err := vectorStore.SearchMessagesByKind(ctx, embedding, 10, "code")
	if err != nil {
		t.Fatalf("code search failed: %v", err)
	}
	if len(codeIDs) != 1 || codeIDs[0] != "code-internal/foo.go-func-Test" {
		t.Fatalf("unexpected code ids: %v", codeIDs)
	}

	memoryIDs, err := vectorStore.SearchMessagesByKind(ctx, embedding, 10, "memory")
	if err != nil {
		t.Fatalf("memory search failed: %v", err)
	}
	if len(memoryIDs) != 1 || memoryIDs[0] != "msg-memory" {
		t.Fatalf("unexpected memory ids: %v", memoryIDs)
	}
}
