package agent

import (
	"context"
	"github.com/user/cognitive-orchestrator/internal/types"
)

// AgentWrapper bridges the provider logic with the orchestration engine
type AgentWrapper struct {
	Agent Agent
}

func (w *AgentWrapper) Respond(ctx context.Context, state *types.CouncilState, instructions string) (types.AgentResponse, error) {
	resp, err := w.Agent.Respond(ctx, state, instructions)
	if err != nil {
		return types.AgentResponse{}, err
	}

	// Centralized Identity Injection (SSoT)
	if resp.Metadata == nil {
		resp.Metadata = make(map[string]interface{})
	}
	// If the provider didn't specify a model, or provided an empty one,
	// use the one from the agent's own configuration.
	if m, ok := resp.Metadata["model"].(string); !ok || m == "" {
		resp.Metadata["model"] = w.Agent.GetConfig().Model
	}

	return resp, nil
}

func (w *AgentWrapper) GetRole() types.Role {
	return w.Agent.GetConfig().Role
}

func (w *AgentWrapper) GetConfig() types.AgentConfig {
	return w.Agent.GetConfig()
}

func (w *AgentWrapper) UpdateConfig(cfg types.AgentConfig) {
	// Dynamically merge the static Constitution and the editable Directive
	cfg.SystemPrompt = cfg.Constitution + "\n\n" + cfg.Directive
	w.Agent.UpdateConfig(cfg)
}

func (w *AgentWrapper) UpdateURL(url string) {
	if updater, ok := w.Agent.(interface{ UpdateURL(string) }); ok {
		updater.UpdateURL(url)
	}
}
