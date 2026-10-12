package analyst

import (
	"github.com/user/cognitive-orchestrator/internal/agent"
	"github.com/user/cognitive-orchestrator/internal/orchestrator/engine"
	"github.com/user/cognitive-orchestrator/internal/types"
)

func New(model, url, apiKey string, emb types.EmbeddingProvider, vector types.VectorStore) engine.Agent {
	constName := "analyst"
	constitution, _ := types.LoadConstitution(constName)
	prompt, _ := types.LoadPrompt("analyst")

	systemPrompt := constitution + "\n\n" + prompt
	if systemPrompt == "\n\n" {
		systemPrompt = "You are a lead codebase analyst." // Fallback
	}

	cfg := types.AgentConfig{
		Name:          "Codebase Analyst",
		Role:          types.RoleContext,
		Model:         model,
		Tools:         []string{"explorer", "memory_retriever", "codebase_search"},
		Directive:     prompt,
		Constitution:  constitution,
		SystemPrompt:  systemPrompt,
		Embedding:     emb,
		Memory:        vector,
		ContextLength: 24576,
	}
	return &agent.AgentWrapper{
		Agent: agent.GetProvider(cfg, url, apiKey),
	}
}
