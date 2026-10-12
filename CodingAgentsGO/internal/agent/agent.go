package agent

import (
	"context"
	"github.com/user/cognitive-orchestrator/internal/types"
)

// Agent defines the interface for any reasoning entity in the framework.
type Agent interface {
	// Respond takes the current state and returns a response.
	Respond(ctx context.Context, state *types.CouncilState, instructions string) (types.AgentResponse, error)

	// GetConfig returns the agent's configuration.
	GetConfig() types.AgentConfig

	// UpdateConfig updates the agent's configuration.
	UpdateConfig(types.AgentConfig)
}
