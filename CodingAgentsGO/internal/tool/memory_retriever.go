package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/user/cognitive-orchestrator/internal/types"
)

type MemoryRetriever struct {
	memory    types.VectorStore
	embedding types.EmbeddingProvider
}

func NewMemoryRetriever(memory types.VectorStore, embedding types.EmbeddingProvider) *MemoryRetriever {
	return &MemoryRetriever{memory: memory, embedding: embedding}
}

func (t *MemoryRetriever) Name() string {
	return "memory_retriever"
}

func (t *MemoryRetriever) Description() string {
	return "Queries semantic memory of past agent runs, user requests, and compaction summaries. Use this to retrieve historical context and learn from past actions. Args: {\"query\": \"search term or topic to look up\"}"
}

func (t *MemoryRetriever) Execute(ctx context.Context, args string) (string, error) {
	var input struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(args), &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w (expected JSON like {\"query\":\"...\"})", err)
	}

	if strings.TrimSpace(input.Query) == "" {
		return "Query is empty.", nil
	}

	if t.embedding == nil || t.memory == nil {
		return "Memory store is not initialized or unavailable.", nil
	}

	emb, err := t.embedding.GenerateEmbedding(ctx, input.Query)
	if err != nil {
		return "", fmt.Errorf("failed to generate query embedding: %w", err)
	}

	ids, err := t.memory.SearchMessages(ctx, emb, 5)
	if filtered, ok := t.memory.(interface {
		SearchMessagesByKind(context.Context, []float32, int, string) ([]string, error)
	}); ok {
		ids, err = filtered.SearchMessagesByKind(ctx, emb, 5, "memory")
	}
	if err != nil {
		return "", fmt.Errorf("failed to search messages: %w", err)
	}

	if len(ids) == 0 {
		return "No relevant memories found.", nil
	}

	var sb strings.Builder
	sb.WriteString("Found the following relevant historical context:\n\n")

	count := 0
	for _, id := range ids {
		if strings.HasPrefix(id, "code-") {
			continue // Skip codebase chunks, return ONLY memories!
		}
		msg, err := t.memory.GetMessage(ctx, id)
		if err != nil || !msg.Visible || msg.Content == "" {
			continue
		}

		content := msg.Content
		if msg.Summary != "" {
			content = msg.Summary
		} else if len(content) > 1000 {
			content = content[:1000] + "... [TRUNCATED]"
		}

		roleLabel := strings.ToUpper(string(msg.Role))
		if msg.Phase != "" {
			roleLabel = fmt.Sprintf("%s (%s)", roleLabel, msg.Phase)
		}
		count++
		sb.WriteString(fmt.Sprintf("[%d] ROLE: %s\nCONTENT:\n%s\n---\n", count, roleLabel, content))
	}

	res := sb.String()
	if !strings.Contains(res, "[1]") {
		return "No relevant memories found.", nil
	}

	return res, nil
}
