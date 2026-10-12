package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/user/cognitive-orchestrator/internal/types"
)

type CodebaseSearch struct {
	memory    types.VectorStore
	embedding types.EmbeddingProvider
}

func NewCodebaseSearch(memory types.VectorStore, embedding types.EmbeddingProvider) *CodebaseSearch {
	return &CodebaseSearch{memory: memory, embedding: embedding}
}

func (t *CodebaseSearch) Name() string {
	return "codebase_search"
}

func (t *CodebaseSearch) Description() string {
	return "Queries the indexed codebase semantically for relevant code snippets (structs, functions, interfaces, and methods) matching the query. Args: {\"query\": \"search term or function signature to locate\"}"
}

func (t *CodebaseSearch) Execute(ctx context.Context, args string) (string, error) {
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
		return "Vector store is not initialized or unavailable.", nil
	}

	emb, err := t.embedding.GenerateEmbedding(ctx, input.Query)
	if err != nil {
		return "", fmt.Errorf("failed to generate query embedding: %w", err)
	}

	ids, err := t.memory.SearchMessages(ctx, emb, 5)
	if filtered, ok := t.memory.(interface {
		SearchMessagesByKind(context.Context, []float32, int, string) ([]string, error)
	}); ok {
		ids, err = filtered.SearchMessagesByKind(ctx, emb, 5, "code")
	}
	if err != nil {
		return "", fmt.Errorf("failed to search codebase: %w", err)
	}

	var sb strings.Builder
	sb.WriteString("Found the following matching codebase snippets:\n\n")

	count := 0
	for _, id := range ids {
		if !strings.HasPrefix(id, "code-") {
			continue // Skip history/message memories, return ONLY code chunks!
		}

		msg, err := t.memory.GetMessage(ctx, id)
		if err != nil || !msg.Visible || msg.Content == "" {
			continue
		}

		count++
		filePath := msg.Phase
		sb.WriteString(fmt.Sprintf("[%d] FILE: %s\nCONTENT:\n%s\n---\n", count, filePath, msg.Content))
	}

	if count == 0 {
		return "No matching codebase snippets found.", nil
	}

	return sb.String(), nil
}
