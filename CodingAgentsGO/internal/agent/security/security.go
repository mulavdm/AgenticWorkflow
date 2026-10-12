package security

import (
	"github.com/user/cognitive-orchestrator/internal/agent"
	"github.com/user/cognitive-orchestrator/internal/orchestrator/engine"
	"github.com/user/cognitive-orchestrator/internal/types"
)

func New(model, url, apiKey string, emb types.EmbeddingProvider, vector types.VectorStore) engine.Agent {
	constName := "security"
	constitution, _ := types.LoadConstitution(constName)
	prompt, _ := types.LoadPrompt("security")

	systemPrompt := constitution + "\n\n" + prompt
	if systemPrompt == "\n\n" {
		systemPrompt = "You are an adversarial security researcher." // Fallback
	}

	cfg := types.AgentConfig{
		Name:         "Red Team Security",
		Role:         types.RoleRedTeamer,
		Model:        model,
		Tools:        []string{"explorer", "memory_retriever", "coderunner"},
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
