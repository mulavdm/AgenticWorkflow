package engine

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/user/cognitive-orchestrator/internal/types"
)

type Agent interface {
	Respond(ctx context.Context, state *types.CouncilState, instructions string) (types.AgentResponse, error)
	GetRole() types.Role
	GetConfig() types.AgentConfig
	UpdateConfig(types.AgentConfig)
}

type Grounder interface {
	ValidateResponse(ctx context.Context, state *types.CouncilState, role types.Role, content string, metadata map[string]interface{}) (string, error)
}

type Runtime struct {
	Agents      map[types.Role]Agent
	Grounder    Grounder
	Broadcaster types.Broadcaster
}

func NewRuntime(agents map[types.Role]Agent, grounder Grounder, bc types.Broadcaster) *Runtime {
	return &Runtime{Agents: agents, Grounder: grounder, Broadcaster: bc}
}

// RunPhase executes a single phase of the orchestration.
func (r *Runtime) RunPhase(ctx context.Context, state *types.CouncilState, role types.Role, instructions string) (types.AgentResponse, error) {
	agent, ok := r.Agents[role]
	if !ok {
		return types.AgentResponse{}, fmt.Errorf("agent for role %s not found", role)
	}

	state.Phase = string(role)
	state.AddEvent(types.EventPhaseStarted, map[string]string{"role": string(role)})

	maxRetries := 5
	var lastErr error
	for state.RetryCount <= maxRetries {
		resp, err := agent.Respond(ctx, state, instructions)
		if err != nil {
			lastErr = err
			state.RetryCount++
			continue
		}

		// Validation & Tool Execution Loop
		validatedContent, vErr := r.Grounder.ValidateResponse(ctx, state, role, resp.Content, resp.Metadata)
		if vErr != nil {
			lastErr = vErr
			if vErr == types.ErrToolExecuted {
				// Tool was executed, state was updated, re-prompt the agent.
				state.AddEvent(types.EventToolExecuted, map[string]string{"role": string(role)})
				state.RetryCount = 0
				continue
			}
			if errors.Is(vErr, types.ErrApprovalRequired) {
				state.LastError = &types.OrchestrationError{
					Phase:   state.Phase,
					Role:    role,
					Message: vErr.Error(),
				}
				return types.AgentResponse{}, vErr
			}

			state.RetryCount++
			if validatedContent != "" {
				state.AddMessage(role, strings.TrimSpace(validatedContent), resp.Metadata, r.Broadcaster)
			}
			state.AddEvent(types.EventErrorEncountered, map[string]string{"error": vErr.Error()})
			state.AddMessage(types.RoleSystem, fmt.Sprintf("[SYSTEM ERROR]: %v", vErr), resp.Metadata, r.Broadcaster)
			continue
		}

		if validatedContent != "" {
			state.AddMessageWithMetadata(role, validatedContent, resp.Metadata, r.Broadcaster, resp.Usage)
		}
		return types.AgentResponse{Content: validatedContent, Metadata: resp.Metadata, Usage: resp.Usage}, nil
	}

	errMsg := "max retries exceeded due to external orchestration validation"
	if lastErr != nil {
		errMsg = lastErr.Error()
	}

	state.LastError = &types.OrchestrationError{
		Phase:   state.Phase,
		Role:    role,
		Message: errMsg,
	}

	if lastErr != nil {
		return types.AgentResponse{}, fmt.Errorf("phase %s failed after %d retries: %w", role, maxRetries, lastErr)
	}
	return types.AgentResponse{}, fmt.Errorf("phase %s failed after %d retries: %s", role, maxRetries, errMsg)
}

func (r *Runtime) ParseNextRole(content string) types.Role {
	re := regexp.MustCompile(`(?i)\[?NEXT:\s*(\w+)[^\]]*\]?`)
	matches := re.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return ""
	}

	// Iterate backwards to find the last valid role (handoff is usually at the end)
	for i := len(matches) - 1; i >= 0; i-- {
		roleName := strings.ToLower(matches[i][1])

		// Skip placeholders
		if roleName == "role_name" || roleName == "role" || roleName == "target" {
			continue
		}

		if roleName == "completed" {
			return "completed"
		}

		// Map aliases to internal types
		var target types.Role
		switch roleName {
		case "security", "redteam", "redteamer":
			target = types.RoleRedTeamer
		case "analyst", "context":
			target = types.RoleContext
		case "critic", "critical", "reviewer":
			target = types.RoleCritic
		case "planner", "orchestrator":
			target = types.RolePlanner
		case "synthesizer", "synth":
			target = types.RoleSynthesizer
		default:
			target = types.Role(roleName)
		}

		if _, ok := r.Agents[target]; ok {
			return target
		}
	}

	return ""
}
