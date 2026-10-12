package synthesizer

import (
	"github.com/user/cognitive-orchestrator/internal/agent"
	"github.com/user/cognitive-orchestrator/internal/orchestrator/engine"
	"github.com/user/cognitive-orchestrator/internal/types"
)

func New(model, url, apiKey string, emb types.EmbeddingProvider, vector types.VectorStore) engine.Agent {
	constName := "synthesizer"
	constitution, _ := types.LoadConstitution(constName)
	prompt, _ := types.LoadPrompt("synthesizer")

	systemPrompt := constitution + "\n\n" + prompt
	if systemPrompt == "\n\n" {
		systemPrompt = "You are a synthesizer." // Fallback
	}

	cfg := types.AgentConfig{
		Name:         "Final Synthesizer",
		Role:         types.RoleSynthesizer,
		Model:        model,
		Tools:        []string{},
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
