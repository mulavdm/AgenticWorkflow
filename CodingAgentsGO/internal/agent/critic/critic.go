package critic

import (
	"github.com/user/cognitive-orchestrator/internal/agent"
	"github.com/user/cognitive-orchestrator/internal/orchestrator/engine"
	"github.com/user/cognitive-orchestrator/internal/types"
)

func New(model, url, apiKey string, emb types.EmbeddingProvider, vector types.VectorStore) engine.Agent {
	constName := "critic"
	constitution, _ := types.LoadConstitution(constName)
	prompt, _ := types.LoadPrompt("critic")

	systemPrompt := constitution + "\n\n" + prompt
	if systemPrompt == "\n\n" {
		systemPrompt = "You are a critical reviewer." // Fallback
	}

	cfg := types.AgentConfig{
		Name:         "Safety Critic",
		Role:         types.RoleCritic,
		Model:        model,
		Tools:        []string{"explorer", "memory_retriever"},
		Directive:    prompt,
		Constitution: constitution,
		SystemPrompt: systemPrompt,
		Embedding:    emb,
		Memory:       vector,
	}
	return &agent.AgentWrapper{
		Agent: agent.GetProvider(cfg, url, apiKey),
	}
}
