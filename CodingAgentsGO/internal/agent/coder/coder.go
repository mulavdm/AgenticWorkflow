package coder

import (
	"fmt"
	"github.com/user/cognitive-orchestrator/internal/agent"
	"github.com/user/cognitive-orchestrator/internal/orchestrator/engine"
	"github.com/user/cognitive-orchestrator/internal/types"
)

func New(model, url, apiKey string, emb types.EmbeddingProvider, vector types.VectorStore) engine.Agent {
	constName := "coder"
	constitution, err := types.LoadConstitution(constName)
	if err != nil {
		fmt.Printf("[Coder] WARNING: Failed to load constitution: %v\n", err)
	}
	prompt, err := types.LoadPrompt("coder")
	if err != nil {
		fmt.Printf("[Coder] WARNING: Failed to load prompt: %v\n", err)
	}

	systemPrompt := constitution + "\n\n" + prompt

	cfg := types.AgentConfig{
		Name:          "Lead Coder",
		Role:          types.RoleCoder,
		Model:         model,
		Tools:         []string{"explorer", "writer", "coderunner", "dockerrunner", "memory_retriever"},
		Directive:     prompt,
		Constitution:  constitution,
		SystemPrompt:  systemPrompt,
		Embedding:     emb,
		Memory:        vector,
		MaxTokens:     8192,
		ContextLength: 32768,
	}
	return &agent.AgentWrapper{
		Agent: agent.GetProvider(cfg, url, apiKey),
	}
}
