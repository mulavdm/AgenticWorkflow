package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	afartifact "github.com/ArtifactFlow/ArtifactFlow/pkg/artifact"
	afstate "github.com/ArtifactFlow/ArtifactFlow/pkg/state"
	afworkflow "github.com/ArtifactFlow/ArtifactFlow/pkg/workflow"
	"github.com/user/cognitive-orchestrator/internal/agent"
	"github.com/user/cognitive-orchestrator/internal/types"
)

type codingAgentsRunMetadata struct {
	RunID         string              `json:"run_id"`
	Prompt        string              `json:"prompt"`
	TargetRole    types.Role          `json:"target_role,omitempty"`
	StartRole     types.Role          `json:"start_role"`
	Graph         map[string][]string `json:"graph,omitempty"`
	Targeted      bool                `json:"targeted"`
	Shadow        bool                `json:"shadow"`
	ShadowPath    string              `json:"shadow_path,omitempty"`
	WorkspaceMode bool                `json:"workspace_mode"`
	Provenance    RunProvenance       `json:"provenance,omitempty"`
}

type RunProvenance struct {
	BuildVersion     string                         `json:"build_version,omitempty"`
	DefaultMode      string                         `json:"default_mode,omitempty"`
	ProviderURLs     map[string]string              `json:"provider_urls,omitempty"`
	StructuredOutput bool                           `json:"structured_output"`
	Agents           map[types.Role]AgentProvenance `json:"agents,omitempty"`
}

type AgentProvenance struct {
	Model            string   `json:"model,omitempty"`
	Temperature      float32  `json:"temperature,omitempty"`
	MaxTokens        int      `json:"max_tokens,omitempty"`
	ContextLength    int      `json:"context_length,omitempty"`
	Tools            []string `json:"tools,omitempty"`
	ConstitutionHash string   `json:"constitution_hash,omitempty"`
	DirectiveHash    string   `json:"directive_hash,omitempty"`
	SystemPromptHash string   `json:"system_prompt_hash,omitempty"`
	Custom           bool     `json:"custom"`
}

type dynamicCapabilityProvider struct {
	caps []string
}

func (d *dynamicCapabilityProvider) Name() string                   { return "dynamic.llama" }
func (d *dynamicCapabilityProvider) RequiredCapabilities() []string { return d.caps }
func (d *dynamicCapabilityProvider) Process(ctx context.Context, config map[string]any, inputs []afartifact.Artifact) ([]afartifact.Artifact, error) {
	return nil, nil
}

func (c *Coordinator) rehydrateState(runID string) (*types.CouncilState, codingAgentsRunMetadata, error) {
	meta, err := c.loadRunMetadata(runID)
	if err != nil {
		return nil, meta, err
	}
	state, err := c.reconstructCouncilState(runID, meta)
	if err != nil {
		return nil, meta, err
	}
	c.activeMu.Lock()
	c.activeStates[runID] = state
	c.activeMu.Unlock()
	return state, meta, nil
}

// 1. thinkProcessor
type thinkProcessor struct {
	coord *Coordinator
}

func (p *thinkProcessor) Name() string                   { return "codingagents.think" }
func (p *thinkProcessor) RequiredCapabilities() []string { return nil }
func (p *thinkProcessor) Process(ctx context.Context, config map[string]any, inputs []afartifact.Artifact) ([]afartifact.Artifact, error) {
	runID, _ := config["run_id"].(string)
	roleName, _ := config["role"].(string)
	prompt, _ := config["prompt"].(string)
	iteration := parseIntVal(config["iteration"])
	role := normalizeRole(types.Role(roleName))

	state, _, err := p.coord.rehydrateState(runID)
	if err != nil {
		return nil, err
	}

	// Dynamic capability check: if the agent uses a GGUF model, ensure its server capability is ready
	if agentInst := p.coord.Agents[role]; agentInst != nil {
		modelName := agentInst.GetConfig().Model
		if strings.Contains(strings.ToLower(modelName), ".gguf") {
			capKey := "llama.cpp:" + modelName
			log.Printf("[thinkProcessor] Ensuring capability %s is active...", capKey)
			dp := &dynamicCapabilityProvider{caps: []string{capKey}}
			if err := p.coord.afRegistry.EnsureCapabilities(ctx, dp); err != nil {
				return nil, fmt.Errorf("failed to ensure capability %s: %w", capKey, err)
			}

			// Resolve active dynamically allocated port/endpoint from the blackboard config
			var dbEndpoint string
			if boardErr := p.coord.afBoard.LoadConfig("runtimes", capKey, &dbEndpoint); boardErr == nil && dbEndpoint != "" {
				log.Printf("[thinkProcessor] Re-routing agent %s to dynamic server: %s", role, dbEndpoint)
				if agentWrapper, ok := agentInst.(*agent.AgentWrapper); ok {
					if providerWithURL, ok := agentWrapper.Agent.(interface{ UpdateURL(string) }); ok {
						providerWithURL.UpdateURL(dbEndpoint + "/chat/completions")
					}
				}
				if p.coord.Embedding != nil {
					shouldReRoute := true
					if modelGetter, ok := p.coord.Embedding.(interface{ GetModel() string }); ok {
						if modelGetter.GetModel() == "Qwen3-Embedding-0.6B-f16.gguf" {
							shouldReRoute = false
							log.Printf("[thinkProcessor] Embedding provider is using dedicated model %s. Skipping re-route.", modelGetter.GetModel())
						}
					}
					if shouldReRoute {
						if providerWithURL, ok := p.coord.Embedding.(interface{ UpdateURL(string) }); ok {
							log.Printf("[thinkProcessor] Re-routing embedding provider to dynamic server: %s", dbEndpoint)
							providerWithURL.UpdateURL(dbEndpoint + "/chat/completions")
						}
					}
				}
			}
		}
	}

	// Synchronous Pre-Think Compaction Gate: Ensure all older messages are summarized before building history
	if p.coord.Summarizer != nil {
		// 1. If background compaction is currently active, wait for it to complete to avoid concurrent LLM requests
		for {
			p.coord.compactionMu.Lock()
			active := p.coord.compactionActive[state.SessionID]
			p.coord.compactionMu.Unlock()
			if !active {
				break
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
		}

		recentThreshold := 6
		state.RLock()
		msgs := make([]types.Message, len(state.Messages))
		copy(msgs, state.Messages)
		state.RUnlock()

		if len(msgs) > recentThreshold {
			var uncompacted []types.Message
			for i := 0; i < len(msgs)-recentThreshold; i++ {
				m := msgs[i]
				if m.Summary == "" && len(m.Content) >= 2000 && m.Role == types.RoleSystem {
					uncompacted = append(uncompacted, m)
				}
			}

			if len(uncompacted) > 0 {
				log.Printf("[thinkProcessor] Pre-Think Gate: Found %d uncompacted messages. Running synchronous compaction...", len(uncompacted))

				// Resolve dynamic server endpoint before running compaction
				if p.coord.afBoard != nil {
					var dbEndpoint string
					modelName := p.coord.GlobalConfig.OpenAIModel
					if strings.Contains(strings.ToLower(modelName), ".gguf") {
						if boardErr := p.coord.afBoard.LoadConfig("runtimes", "llama.cpp:"+modelName, &dbEndpoint); boardErr == nil && dbEndpoint != "" {
							p.coord.Summarizer.UpdateURL(dbEndpoint + "/chat/completions")
						}
					}
				}

				for _, m := range uncompacted {
					subCtx, subCancel := context.WithTimeout(ctx, 90*time.Second)
					summary, err := p.coord.Summarizer.SummarizeWithRetry(subCtx, m.Content)
					subCancel()

					if err == nil {
						state.SetMessageSummary(m.ID, summary)
					} else {
						log.Printf("[thinkProcessor] Sync compaction failed for %s: %v", m.ID, err)
					}
				}
			}
		}
	}

	instructions := p.coord.roleInstructions(role, prompt)
	state.Phase = fmt.Sprintf("%s_think_%d", role, iteration)
	state.AddEvent(types.EventPhaseStarted, map[string]string{"role": string(role), "step": "think", "iteration": fmt.Sprint(iteration)})

	var resp types.AgentResponse
	agentInst, ok := p.coord.Agents[role]
	if !ok {
		return nil, fmt.Errorf("agent for role %s not found", role)
	}

	if strings.Contains(prompt, "@council") {
		critic := p.coord.Agents[types.RoleCritic]
		synth := p.coord.Agents[types.RoleSynthesizer]
		swarmSize := 0
		re := regexp.MustCompile(`@council:(\d+)`)
		if match := re.FindStringSubmatch(prompt); len(match) > 1 {
			if parsed, err := strconv.Atoi(match[1]); err == nil {
				swarmSize = parsed
			}
		}
		resp, err = p.coord.consensus.ResolveSwarm(ctx, state, agentInst, critic, synth, instructions, swarmSize, p.coord.Broadcaster)
	} else {
		resp, err = agentInst.Respond(ctx, state, instructions)
	}
	if err != nil {
		p.coord.persistRunSnapshot(state)
		return nil, err
	}

	p.coord.persistRunSnapshot(state)
	return []afartifact.Artifact{{
		ID:   fmt.Sprintf("%s-%s-think-%d", runID, role, iteration),
		Kind: afartifact.KindText,
		URI:  "thought.txt",
		Metadata: map[string]any{
			"content":   resp.Content,
			"role":      string(role),
			"iteration": iteration,
			"usage":     resp.Usage,
			"next_node": fmt.Sprintf("%s_ground_%d", role, iteration),
		},
	}}, nil
}

// 2. groundProcessor
type groundProcessor struct {
	coord *Coordinator
}

func (p *groundProcessor) Name() string                   { return "codingagents.ground" }
func (p *groundProcessor) RequiredCapabilities() []string { return nil }
func (p *groundProcessor) Process(ctx context.Context, config map[string]any, inputs []afartifact.Artifact) ([]afartifact.Artifact, error) {
	runID, _ := config["run_id"].(string)
	roleName, _ := config["role"].(string)
	iteration := parseIntVal(config["iteration"])
	role := normalizeRole(types.Role(roleName))

	state, _, err := p.coord.rehydrateState(runID)
	if err != nil {
		return nil, err
	}

	var thoughtContent string
	var thoughtUsage any
	for _, input := range inputs {
		if strings.Contains(input.ID, "think") {
			thoughtContent, _ = input.Metadata["content"].(string)
			thoughtUsage = input.Metadata["usage"]
			break
		}
	}

	state.Phase = fmt.Sprintf("%s_ground_%d", role, iteration)
	state.AddEvent(types.EventPhaseStarted, map[string]string{"role": string(role), "step": "ground", "iteration": fmt.Sprint(iteration)})

	toolName, toolArgs, cleanContent, gErr := p.coord.grounder.ParseAndValidateResponse(state, role, thoughtContent)

	// Format usage if present
	var usage *types.Usage
	if thoughtUsage != nil {
		if uBytes, err := json.Marshal(thoughtUsage); err == nil {
			var u types.Usage
			if json.Unmarshal(uBytes, &u) == nil {
				usage = &u
			}
		}
	}

	if gErr != nil {
		state.AddEvent(types.EventErrorEncountered, map[string]string{"error": gErr.Error()})
		state.AddMessage(types.RoleSystem, fmt.Sprintf("[SYSTEM ERROR]: %v", gErr), nil, p.coord.Broadcaster)
		p.coord.persistRunSnapshot(state)

		return []afartifact.Artifact{{
			ID:   fmt.Sprintf("%s-%s-ground-%d", runID, role, iteration),
			Kind: afartifact.KindAgentEvent,
			Metadata: map[string]any{
				"error":     gErr.Error(),
				"role":      string(role),
				"iteration": iteration,
				"next_node": fmt.Sprintf("%s_policy_%d", role, iteration),
			},
		}}, nil
	}

	if cleanContent != "" {
		state.AddMessageWithMetadata(role, cleanContent, nil, p.coord.Broadcaster, usage)
	}

	if toolName != "" {
		state.AddEvent(types.EventToolRequested, map[string]string{
			"role": string(role),
			"tool": toolName,
			"args": toolArgs,
		})
	}

	p.coord.persistRunSnapshot(state)
	return []afartifact.Artifact{{
		ID:   fmt.Sprintf("%s-%s-ground-%d", runID, role, iteration),
		Kind: afartifact.KindAgentEvent,
		Metadata: map[string]any{
			"tool":      toolName,
			"args":      toolArgs,
			"role":      string(role),
			"iteration": iteration,
			"next_node": fmt.Sprintf("%s_policy_%d", role, iteration),
		},
	}}, nil
}

// 3. policyProcessor
type policyProcessor struct {
	coord *Coordinator
}

func (p *policyProcessor) Name() string                   { return "codingagents.policy" }
func (p *policyProcessor) RequiredCapabilities() []string { return nil }
func (p *policyProcessor) Process(ctx context.Context, config map[string]any, inputs []afartifact.Artifact) ([]afartifact.Artifact, error) {
	runID, _ := config["run_id"].(string)
	roleName, _ := config["role"].(string)
	iteration := parseIntVal(config["iteration"])
	role := normalizeRole(types.Role(roleName))

	state, _, err := p.coord.rehydrateState(runID)
	if err != nil {
		return nil, err
	}

	var toolName, toolArgs, carryError string
	for _, input := range inputs {
		if strings.Contains(input.ID, "ground") {
			toolName, _ = input.Metadata["tool"].(string)
			toolArgs, _ = input.Metadata["args"].(string)
			carryError, _ = input.Metadata["error"].(string)
			break
		}
	}

	state.Phase = fmt.Sprintf("%s_policy_%d", role, iteration)

	// If grounding failed or no tool call, bypass policy
	if carryError != "" || toolName == "" {
		return []afartifact.Artifact{{
			ID:   fmt.Sprintf("%s-%s-policy-%d", runID, role, iteration),
			Kind: afartifact.KindAgentEvent,
			Metadata: map[string]any{
				"error":     carryError,
				"role":      string(role),
				"iteration": iteration,
				"next_node": fmt.Sprintf("%s_execute_%d", role, iteration),
			},
		}}, nil
	}

	state.AddEvent(types.EventPhaseStarted, map[string]string{"role": string(role), "step": "policy", "iteration": fmt.Sprint(iteration)})

	// Deterministic approval ID
	approvalID := fmt.Sprintf("%s-%s-policy-%d", runID, role, iteration)

	// Check if this approval was already decided in the event log (after a resume)
	var decision string
	var decisionReason string
	for _, evt := range state.Events {
		if evt.Type == types.EventApprovalDecided && evt.Payload != nil {
			if payload, ok := evt.Payload.(map[string]any); ok {
				if payload["approval_id"] == approvalID {
					decision, _ = payload["decision"].(string)
					decisionReason, _ = payload["reason"].(string)
					break
				}
			} else if payload, ok := evt.Payload.(map[string]string); ok {
				if payload["approval_id"] == approvalID {
					decision = payload["decision"]
					decisionReason = payload["reason"]
					break
				}
			}
		}
	}

	if decision != "" {
		log.Printf("[policyProcessor] Discovered existing approval decision for %s: %s", approvalID, decision)
		if decision == string(types.ApprovalApproved) {
			return []afartifact.Artifact{{
				ID:   fmt.Sprintf("%s-%s-policy-%d", runID, role, iteration),
				Kind: afartifact.KindAgentEvent,
				Metadata: map[string]any{
					"tool":      toolName,
					"args":      toolArgs,
					"decision":  "approved",
					"role":      string(role),
					"iteration": iteration,
					"next_node": fmt.Sprintf("%s_execute_%d", role, iteration),
				},
			}}, nil
		} else {
			// Denial path: record user denial and inject warning into system messages so agent is redirected
			state.AddMessage(types.RoleSystem, fmt.Sprintf("[SYSTEM WARNING]: The user DENIED permission to run [TOOL: %s, ARGS: %s]. Reason: %s. You MUST find a different way to solve the task without this tool.", toolName, toolArgs, decisionReason), nil, p.coord.Broadcaster)
			state.AddEvent(types.EventErrorEncountered, map[string]string{
				"approval_id": approvalID,
				"error":       "approval denied",
				"reason":      decisionReason,
			})
			p.coord.persistRunSnapshot(state)
			return []afartifact.Artifact{{
				ID:   fmt.Sprintf("%s-%s-policy-%d", runID, role, iteration),
				Kind: afartifact.KindAgentEvent,
				Metadata: map[string]any{
					"error":     "approval denied",
					"role":      string(role),
					"iteration": iteration,
					"next_node": fmt.Sprintf("%s_execute_%d", role, iteration),
				},
			}}, nil
		}
	}

	// Evaluate policy for the first time
	_, pErr := p.coord.grounder.CheckPolicy(state, role, toolName, toolArgs, approvalID)
	if pErr != nil {
		p.coord.persistRunSnapshot(state)
		return nil, pErr // returns ApprovalRequiredError which pauses ArtifactFlow
	}

	p.coord.persistRunSnapshot(state)
	return []afartifact.Artifact{{
		ID:   fmt.Sprintf("%s-%s-policy-%d", runID, role, iteration),
		Kind: afartifact.KindAgentEvent,
		Metadata: map[string]any{
			"tool":      toolName,
			"args":      toolArgs,
			"decision":  "approved",
			"role":      string(role),
			"iteration": iteration,
			"next_node": fmt.Sprintf("%s_execute_%d", role, iteration),
		},
	}}, nil
}

// 4. executeProcessor
type executeProcessor struct {
	coord *Coordinator
}

func (p *executeProcessor) Name() string                   { return "codingagents.execute" }
func (p *executeProcessor) RequiredCapabilities() []string { return nil }
func (p *executeProcessor) Process(ctx context.Context, config map[string]any, inputs []afartifact.Artifact) ([]afartifact.Artifact, error) {
	runID, _ := config["run_id"].(string)
	roleName, _ := config["role"].(string)
	iteration := parseIntVal(config["iteration"])
	role := normalizeRole(types.Role(roleName))

	state, meta, err := p.coord.rehydrateState(runID)
	if err != nil {
		return nil, err
	}

	// Activate shadow workspace base path if active for the duration of this tool execution
	if meta.Shadow && meta.ShadowPath != "" {
		p.coord.Tools.SetBasePath(meta.ShadowPath)
		defer p.coord.Tools.SetBasePath("")
	}

	var toolName, toolArgs, carryError, decision string
	for _, input := range inputs {
		if strings.Contains(input.ID, "policy") {
			toolName, _ = input.Metadata["tool"].(string)
			toolArgs, _ = input.Metadata["args"].(string)
			carryError, _ = input.Metadata["error"].(string)
			decision, _ = input.Metadata["decision"].(string)
			break
		}
	}
	log.Printf("[executeProcessor] Found inputs: %d, toolName: %q, decision: %q, carryError: %q", len(inputs), toolName, decision, carryError)

	state.Phase = fmt.Sprintf("%s_execute_%d", role, iteration)

	// If grounding or policy failed, or no tool was requested, skip execution
	if carryError != "" || toolName == "" || (decision != "approved" && carryError == "") {
		return []afartifact.Artifact{{
			ID:   fmt.Sprintf("%s-%s-execute-%d", runID, role, iteration),
			Kind: afartifact.KindAgentEvent,
			Metadata: map[string]any{
				"error":     carryError,
				"role":      string(role),
				"iteration": iteration,
				"next_node": fmt.Sprintf("%s_route_%d", role, iteration),
			},
		}}, nil
	}

	state.AddEvent(types.EventPhaseStarted, map[string]string{"role": string(role), "step": "execute", "iteration": fmt.Sprint(iteration)})

	output, eErr := p.coord.grounder.ExecuteTool(ctx, state, role, toolName, toolArgs)
	log.Printf("[executeProcessor] ExecuteTool returned output: %q, error: %v", output, eErr)

	// Compact history in the background after tool execution
	go p.coord.CompactHistory(ctx, state)

	if eErr != nil {
		state.AddEvent(types.EventErrorEncountered, map[string]string{"error": eErr.Error()})
		p.coord.persistRunSnapshot(state)

		return []afartifact.Artifact{{
			ID:   fmt.Sprintf("%s-%s-execute-%d", runID, role, iteration),
			Kind: afartifact.KindAgentEvent,
			Metadata: map[string]any{
				"error":     eErr.Error(),
				"role":      string(role),
				"iteration": iteration,
				"next_node": fmt.Sprintf("%s_route_%d", role, iteration),
			},
		}}, nil
	}

	p.coord.persistRunSnapshot(state)
	return []afartifact.Artifact{{
		ID:   fmt.Sprintf("%s-%s-execute-%d", runID, role, iteration),
		Kind: afartifact.KindAgentEvent,
		Metadata: map[string]any{
			"observation": output,
			"role":        string(role),
			"iteration":   iteration,
			"next_node":   fmt.Sprintf("%s_route_%d", role, iteration),
		},
	}}, nil
}

// 5. routeProcessor
type routeProcessor struct {
	coord *Coordinator
}

func (p *routeProcessor) Name() string                   { return "codingagents.route" }
func (p *routeProcessor) RequiredCapabilities() []string { return nil }
func (p *routeProcessor) Process(ctx context.Context, config map[string]any, inputs []afartifact.Artifact) ([]afartifact.Artifact, error) {
	runID, _ := config["run_id"].(string)
	roleName, _ := config["role"].(string)
	iteration := parseIntVal(config["iteration"])
	maxLoops := parseIntVal(config["max_loops"])
	targeted, _ := config["targeted"].(bool)
	role := normalizeRole(types.Role(roleName))

	state, _, err := p.coord.rehydrateState(runID)
	if err != nil {
		return nil, err
	}

	state.Phase = fmt.Sprintf("%s_route_%d", role, iteration)

	// Determine next role from last message in state
	messages := state.SnapshotMessages()
	var lastMsg types.Message
	if len(messages) > 0 {
		lastMsg = messages[len(messages)-1]
	}

	// If the last message is a tool observation, the agent hasn't processed it yet.
	// We must loop back to let the agent think and respond to the observation.
	isObservation := lastMsg.Role == types.RoleSystem && strings.HasPrefix(lastMsg.Content, "[OBSERVATION")

	nextRole := p.coord.runtime.ParseNextRole(lastMsg.Content)
	if targeted {
		nextRole = "completed"
	}

	if isObservation {
		// Force loop back if we haven't hit max loops
		if iteration < maxLoops {
			p.coord.persistRunSnapshot(state)
			return []afartifact.Artifact{{
				ID:   fmt.Sprintf("%s-%s-route-%d", runID, role, iteration),
				Kind: afartifact.KindAgentEvent,
				Metadata: map[string]any{
					"next_node": fmt.Sprintf("%s_think_%d", role, iteration+1),
					"role":      string(role),
					"iteration": iteration,
				},
			}}, nil
		}
		// Otherwise, fallback
		log.Printf("[routeProcessor] Max loops (%d) reached after tool observation. Forcing exit.", maxLoops)
		state.AddEvent(types.EventErrorEncountered, map[string]string{
			"error": fmt.Sprintf("max loops (%d) reached for role %s after tool observation", maxLoops, role),
		})
		fallbackRole := types.RoleSynthesizer
		if role == types.RoleSynthesizer {
			fallbackRole = "completed"
		}
		p.coord.persistRunSnapshot(state)
		return []afartifact.Artifact{{
			ID:   fmt.Sprintf("%s-%s-route-%d", runID, role, iteration),
			Kind: afartifact.KindAgentEvent,
			Metadata: map[string]any{
				"next_node": func() string {
					fr := string(normalizeRole(fallbackRole))
					if fr == "completed" || fr == "" {
						return "completed"
					}
					return fmt.Sprintf("%s_think_1", fr)
				}(),
				"role":      string(role),
				"iteration": iteration,
			},
		}}, nil
	}

	// If agent is done or decided to handoff
	if nextRole != "" && nextRole != role {
		p.coord.persistRunSnapshot(state)
		return []afartifact.Artifact{{
			ID:   fmt.Sprintf("%s-%s-route-%d", runID, role, iteration),
			Kind: afartifact.KindAgentEvent,
			Metadata: map[string]any{
				"next_node": func() string {
					nr := string(normalizeRole(nextRole))
					if nr == "completed" || nr == "" {
						return "completed"
					}
					return fmt.Sprintf("%s_think_1", nr)
				}(),
				"role":      string(role),
				"iteration": iteration,
			},
		}}, nil
	}

	// If no valid handoff, or they want to loop back (same role)
	// If we haven't reached the max loops, route to the next think step of this role
	if iteration < maxLoops {
		p.coord.persistRunSnapshot(state)
		return []afartifact.Artifact{{
			ID:   fmt.Sprintf("%s-%s-route-%d", runID, role, iteration),
			Kind: afartifact.KindAgentEvent,
			Metadata: map[string]any{
				"next_node": fmt.Sprintf("%s_think_%d", role, iteration+1),
				"role":      string(role),
				"iteration": iteration,
			},
		}}, nil
	}

	// If max loops reached, force exit to completed or synthesizer fallback
	log.Printf("[routeProcessor] Max loops (%d) reached for role %s. Forcing exit.", maxLoops, role)
	state.AddEvent(types.EventErrorEncountered, map[string]string{
		"error": fmt.Sprintf("max loops (%d) reached for role %s", maxLoops, role),
	})
	fallbackRole := types.RoleSynthesizer
	if role == types.RoleSynthesizer {
		fallbackRole = "completed"
	}
	p.coord.persistRunSnapshot(state)
	return []afartifact.Artifact{{
		ID:   fmt.Sprintf("%s-%s-route-%d", runID, role, iteration),
		Kind: afartifact.KindAgentEvent,
		Metadata: map[string]any{
			"next_node": func() string {
				fr := string(normalizeRole(fallbackRole))
				if fr == "completed" || fr == "" {
					return "completed"
				}
				return fmt.Sprintf("%s_think_1", fr)
			}(),
			"role":      string(role),
			"iteration": iteration,
		},
	}}, nil
}

func (c *Coordinator) roleInstructions(role types.Role, prompt string) string {
	instructions := c.loadInstructions(role)
	if strings.TrimSpace(prompt) == "" {
		return instructions
	}
	if role == types.RolePlanner {
		return fmt.Sprintf("TASK: Plan the implementation of: %s\n\n%s", prompt, instructions)
	}
	return fmt.Sprintf("TASK: %s\n\n%s", prompt, instructions)
}

func (c *Coordinator) compileAgentWorkflow(runID string, graph map[string][]string, prompt string, targeted bool) *afworkflow.Workflow {
	if len(graph) == 0 {
		graph = defaultAgentGraph()
	}
	graph = normalizeGraph(graph)

	// Parse unrolling depth (loops)
	maxLoops := 10 // Default
	reLoops := regexp.MustCompile(`@loops:(\d+)`)
	if match := reLoops.FindStringSubmatch(prompt); len(match) > 1 {
		if parsed, err := strconv.Atoi(match[1]); err == nil && parsed > 0 {
			maxLoops = parsed
		}
	}

	nodes := make(map[string]afworkflow.Node)

	// Step 1: Create all unrolled nodes for each role
	for roleStr := range graph {
		roleStr = string(normalizeRole(types.Role(roleStr)))
		for i := 1; i <= maxLoops; i++ {
			thinkID := fmt.Sprintf("%s_think_%d", roleStr, i)
			groundID := fmt.Sprintf("%s_ground_%d", roleStr, i)
			policyID := fmt.Sprintf("%s_policy_%d", roleStr, i)
			executeID := fmt.Sprintf("%s_execute_%d", roleStr, i)
			routeID := fmt.Sprintf("%s_route_%d", roleStr, i)

			nodes[thinkID] = afworkflow.Node{
				ID:        thinkID,
				Processor: "codingagents.think",
				Config: map[string]any{
					"run_id":    runID,
					"role":      roleStr,
					"iteration": i,
					"prompt":    prompt,
				},
			}

			nodes[groundID] = afworkflow.Node{
				ID:        groundID,
				Processor: "codingagents.ground",
				Config: map[string]any{
					"run_id":    runID,
					"role":      roleStr,
					"iteration": i,
				},
				DependsOn: []string{thinkID},
			}

			nodes[policyID] = afworkflow.Node{
				ID:        policyID,
				Processor: "codingagents.policy",
				Config: map[string]any{
					"run_id":    runID,
					"role":      roleStr,
					"iteration": i,
				},
				DependsOn: []string{groundID},
			}

			nodes[executeID] = afworkflow.Node{
				ID:        executeID,
				Processor: "codingagents.execute",
				Config: map[string]any{
					"run_id":    runID,
					"role":      roleStr,
					"iteration": i,
				},
				DependsOn: []string{policyID},
			}

			nodes[routeID] = afworkflow.Node{
				ID:        routeID,
				Processor: "codingagents.route",
				Config: map[string]any{
					"run_id":    runID,
					"role":      roleStr,
					"iteration": i,
					"max_loops": maxLoops,
					"targeted":  targeted,
				},
				DependsOn: []string{executeID},
			}
		}
	}

	// Step 2: Establish dependencies across iterations and roles
	for roleStr, tos := range graph {
		roleStr = string(normalizeRole(types.Role(roleStr)))

		// Connect iterations within the same role
		for i := 2; i <= maxLoops; i++ {
			thinkID := fmt.Sprintf("%s_think_%d", roleStr, i)
			prevRouteID := fmt.Sprintf("%s_route_%d", roleStr, i-1)

			node := nodes[thinkID]
			node.DependsOn = append(node.DependsOn, prevRouteID)
			nodes[thinkID] = node
		}

		// Connect across roles in the graph
		for _, to := range tos {
			to = string(normalizeRole(types.Role(to)))
			if to == "" || to == "completed" {
				continue
			}

			// The first think node of the downstream role depends on all routing nodes of the upstream role
			toThink1ID := fmt.Sprintf("%s_think_1", to)
			toNode, ok := nodes[toThink1ID]
			if ok {
				for i := 1; i <= maxLoops; i++ {
					fromRouteID := fmt.Sprintf("%s_route_%d", roleStr, i)
					if !contains(toNode.DependsOn, fromRouteID) {
						toNode.DependsOn = append(toNode.DependsOn, fromRouteID)
					}
				}
				nodes[toThink1ID] = toNode
			}
		}

		// Connect all non-synthesizer roles to the synthesizer as a valid fallback transition in the DAG
		if roleStr != string(types.RoleSynthesizer) {
			toThink1ID := fmt.Sprintf("%s_think_1", string(types.RoleSynthesizer))
			toNode, ok := nodes[toThink1ID]
			if ok {
				for i := 1; i <= maxLoops; i++ {
					fromRouteID := fmt.Sprintf("%s_route_%d", roleStr, i)
					if !contains(toNode.DependsOn, fromRouteID) {
						toNode.DependsOn = append(toNode.DependsOn, fromRouteID)
					}
				}
				nodes[toThink1ID] = toNode
			}
		}
	}

	// Step 3: Compile into the final workflow in topological-friendly order
	wf := &afworkflow.Workflow{Name: "codingagents-agentic-runtime"}

	// Add nodes role-by-role
	orderedRoles := []types.Role{
		types.RoleContext,
		types.RolePlanner,
		types.RoleCritic,
		types.RoleRedTeamer,
		types.RoleCoder,
		types.RoleSynthesizer,
	}

	for _, r := range orderedRoles {
		roleStr := string(r)
		for i := 1; i <= maxLoops; i++ {
			thinkID := fmt.Sprintf("%s_think_%d", roleStr, i)
			if _, ok := nodes[thinkID]; ok {
				wf.Nodes = append(wf.Nodes, nodes[thinkID])
				wf.Nodes = append(wf.Nodes, nodes[fmt.Sprintf("%s_ground_%d", roleStr, i)])
				wf.Nodes = append(wf.Nodes, nodes[fmt.Sprintf("%s_policy_%d", roleStr, i)])
				wf.Nodes = append(wf.Nodes, nodes[fmt.Sprintf("%s_execute_%d", roleStr, i)])
				wf.Nodes = append(wf.Nodes, nodes[fmt.Sprintf("%s_route_%d", roleStr, i)])
			}
		}
	}

	// Add any remaining nodes that weren't in the ordered roles list
	for _, node := range nodes {
		if !containsWorkflowNode(wf.Nodes, node.ID) {
			wf.Nodes = append(wf.Nodes, node)
		}
	}

	return wf
}

func defaultAgentGraph() map[string][]string {
	return map[string][]string{
		string(types.RoleContext):     {string(types.RolePlanner)},
		string(types.RolePlanner):     {string(types.RoleCritic), string(types.RoleCoder), string(types.RoleSynthesizer)},
		string(types.RoleCritic):      {string(types.RoleRedTeamer), string(types.RoleCoder), string(types.RoleSynthesizer)},
		string(types.RoleRedTeamer):   {string(types.RoleCoder), string(types.RoleSynthesizer)},
		string(types.RoleCoder):       {string(types.RoleSynthesizer)},
		string(types.RoleSynthesizer): {},
	}
}

func RoleGraphPreset(name string) map[string][]string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "default":
		return defaultAgentGraph()
	case "planner-only":
		return map[string][]string{
			string(types.RolePlanner): {},
		}
	case "code-review":
		return map[string][]string{
			string(types.RoleContext):     {string(types.RoleCritic)},
			string(types.RoleCritic):      {string(types.RoleRedTeamer), string(types.RoleSynthesizer)},
			string(types.RoleRedTeamer):   {string(types.RoleSynthesizer)},
			string(types.RoleSynthesizer): {},
		}
	case "implementation":
		return map[string][]string{
			string(types.RoleContext):     {string(types.RolePlanner)},
			string(types.RolePlanner):     {string(types.RoleCoder)},
			string(types.RoleCoder):       {string(types.RoleCritic), string(types.RoleSynthesizer)},
			string(types.RoleCritic):      {string(types.RoleSynthesizer)},
			string(types.RoleSynthesizer): {},
		}
	case "security-review":
		return map[string][]string{
			string(types.RoleContext):     {string(types.RoleRedTeamer)},
			string(types.RoleRedTeamer):   {string(types.RoleCritic), string(types.RoleSynthesizer)},
			string(types.RoleCritic):      {string(types.RoleSynthesizer)},
			string(types.RoleSynthesizer): {},
		}
	case "artifactflow-platform":
		return map[string][]string{
			string(types.RoleContext):     {string(types.RolePlanner)},
			string(types.RolePlanner):     {string(types.RoleCritic), string(types.RoleCoder)},
			string(types.RoleCritic):      {string(types.RoleRedTeamer), string(types.RoleCoder)},
			string(types.RoleRedTeamer):   {string(types.RoleCoder)},
			string(types.RoleCoder):       {string(types.RoleSynthesizer)},
			string(types.RoleSynthesizer): {},
		}
	default:
		return nil
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsWorkflowNode(nodes []afworkflow.Node, target string) bool {
	for _, node := range nodes {
		if node.ID == target {
			return true
		}
	}
	return false
}

func (c *Coordinator) persistRunSnapshot(state *types.CouncilState) {
	if c.afBoard == nil || state == nil {
		return
	}
	for _, msg := range state.SnapshotMessages() {
		_ = c.afBoard.SaveMessage(toArtifactFlowMessage(state.SessionID, msg))
	}
	state.RLock()
	events := make([]types.StateEvent, len(state.Events))
	copy(events, state.Events)
	state.RUnlock()
	for _, evt := range events {
		_ = c.afBoard.SaveEvent(toArtifactFlowEvent(state.SessionID, evt))
	}
	c.persistArtifactRecord(state, "project_map", state.Artifacts.ProjectMap)
	c.persistArtifactRecord(state, "technical_plan", state.Artifacts.TechnicalPlan)
	c.persistArtifactRecord(state, "execution_logs", state.Artifacts.ExecutionLogs)
	c.persistArtifactRecord(state, "critique", state.Artifacts.Critique)
	c.persistArtifactRecord(state, "security_report", state.Artifacts.SecurityReport)
}

func (c *Coordinator) GetRunMessages(runID string) ([]afstate.Message, error) {
	if c.afBoard == nil {
		return nil, fmt.Errorf("artifactflow runtime is unavailable")
	}
	return c.afBoard.GetMessages(runID)
}

func (c *Coordinator) GetRunEvents(runID string) ([]afstate.Event, error) {
	if c.afBoard == nil {
		return nil, fmt.Errorf("artifactflow runtime is unavailable")
	}
	return c.afBoard.GetEvents(runID)
}

func (c *Coordinator) GetRunArtifacts(runID string) ([]afartifact.Artifact, error) {
	if c.afBoard == nil {
		return nil, fmt.Errorf("artifactflow runtime is unavailable")
	}
	return c.afBoard.GetRunArtifacts(runID)
}

func (c *Coordinator) GetRuns(limit int) ([]afstate.WorkflowRun, error) {
	if c.afBoard == nil {
		return nil, fmt.Errorf("artifactflow runtime is unavailable")
	}
	return c.afBoard.ListWorkflowRuns(limit)
}

func (c *Coordinator) GetRun(runID string) (*afstate.WorkflowRun, error) {
	if c.afBoard == nil {
		return nil, fmt.Errorf("artifactflow runtime is unavailable")
	}
	return c.afBoard.GetWorkflowRun(runID)
}

func (c *Coordinator) GetRunTimeline(runID string) ([]afstate.TimelineEntry, error) {
	if c.afBoard == nil {
		return nil, fmt.Errorf("artifactflow runtime is unavailable")
	}
	return c.afBoard.GetTimeline(runID)
}

func (c *Coordinator) ReconstructRun(runID string) (*afstate.RunSnapshot, error) {
	if c.afBoard == nil {
		return nil, fmt.Errorf("artifactflow runtime is unavailable")
	}
	return c.afBoard.ReconstructRun(runID)
}

func (c *Coordinator) CancelRun(runID string) error {
	if c.afBoard == nil {
		return fmt.Errorf("artifactflow runtime is unavailable")
	}
	c.activeMu.Lock()
	if paused := c.pausedRuns[runID]; paused != nil && paused.shadow {
		c.CleanupShadow()
		c.Tools.SetBasePath("")
	}
	delete(c.pausedRuns, runID)
	delete(c.activeStates, runID)
	c.activeMu.Unlock()
	return c.afBoard.CancelWorkflowRun(runID)
}

func (c *Coordinator) GetRunApprovals(runID string) ([]types.Approval, error) {
	if c.afBoard == nil {
		return nil, fmt.Errorf("artifactflow runtime is unavailable")
	}
	events, err := c.afBoard.GetEvents(runID)
	if err != nil {
		return nil, err
	}
	approvalsByID := make(map[string]*types.Approval)
	var ordered []string
	for _, evt := range events {
		switch evt.Type {
		case string(types.EventApprovalRequested):
			id := stringPayload(evt.Payload, "approval_id")
			if id == "" {
				continue
			}
			approval, ok := approvalsByID[id]
			if !ok {
				ordered = append(ordered, id)
				approval = &types.Approval{ID: id, RunID: runID, Status: types.ApprovalPending}
				approvalsByID[id] = approval
			}
			approval.Role = types.Role(stringPayload(evt.Payload, "role"))
			approval.Tool = stringPayload(evt.Payload, "tool")
			approval.Args = stringPayload(evt.Payload, "args")
			approval.Reason = stringPayload(evt.Payload, "reason")
			approval.CreatedAt = evt.CreatedAt
			if approval.Status == "" {
				approval.Status = types.ApprovalPending
			}
		case string(types.EventApprovalDecided):
			id := stringPayload(evt.Payload, "approval_id")
			if id == "" {
				continue
			}
			approval, ok := approvalsByID[id]
			if !ok {
				approval = &types.Approval{ID: id, RunID: runID, Status: types.ApprovalPending}
				approvalsByID[id] = approval
				ordered = append(ordered, id)
			}
			switch strings.ToLower(stringPayload(evt.Payload, "decision")) {
			case string(types.ApprovalApproved), "approve", "allow":
				approval.Status = types.ApprovalApproved
			case string(types.ApprovalDenied), "deny":
				approval.Status = types.ApprovalDenied
			}
			approval.DecisionReason = stringPayload(evt.Payload, "reason")
			decidedAt := evt.CreatedAt
			approval.DecidedAt = &decidedAt
		}
	}
	approvals := make([]types.Approval, 0, len(ordered))
	for _, id := range ordered {
		approval := *approvalsByID[id]
		approval.ResumeMode = c.approvalResumeMode(runID, approval)
		approvals = append(approvals, approval)
	}
	return approvals, nil
}

func (c *Coordinator) DecideApproval(runID, approvalID, decision, reason string) error {
	if c.afBoard == nil {
		return fmt.Errorf("artifactflow runtime is unavailable")
	}
	approval, err := c.findApproval(runID, approvalID)
	if err != nil {
		return err
	}
	if approval.Status == types.ApprovalApproved || approval.Status == types.ApprovalDenied {
		return nil
	}
	decision = strings.ToLower(strings.TrimSpace(decision))
	switch decision {
	case "approve", "approved", "allow":
		decision = string(types.ApprovalApproved)
	case "deny", "denied":
		decision = string(types.ApprovalDenied)
	default:
		return fmt.Errorf("approval decision must be approved or denied")
	}
	if strings.TrimSpace(approvalID) == "" {
		return fmt.Errorf("approval_id is required")
	}
	now := time.Now()
	if err := c.afBoard.SaveEvent(afstate.Event{
		ID:    fmt.Sprintf("%s-decision-%d", approvalID, now.UnixNano()),
		RunID: runID,
		Type:  string(types.EventApprovalDecided),
		Payload: map[string]any{
			"approval_id": approvalID,
			"decision":    decision,
			"reason":      reason,
		},
		CreatedAt: now,
	}); err != nil {
		return err
	}

	c.activeMu.RLock()
	paused := c.pausedRuns[runID]
	c.activeMu.RUnlock()
	if paused == nil {
		log.Printf("[DecideApproval] paused run is nil in memory, loading from DB...")
		run, err := c.afBoard.GetWorkflowRun(runID)
		if err != nil {
			return err
		}
		log.Printf("[DecideApproval] DB run status: %q", run.Status)
		if run.Status != "paused" {
			return nil
		}
		paused, err = c.reconstructPausedRun(runID, approval)
		if err != nil {
			c.recordResumeFailure(runID, approvalID, err)
			return err
		}
		log.Printf("[DecideApproval] Reconstructed paused run, nodeID: %q", paused.nodeID)
	}

	if decision == string(types.ApprovalDenied) {
		paused.state.AddMessage(types.RoleSystem, fmt.Sprintf("[APPROVAL DENIED]: %s", reason), nil, c.Broadcaster)
		paused.state.AddEvent(types.EventErrorEncountered, map[string]string{
			"approval_id": approvalID,
			"error":       "approval denied",
			"reason":      reason,
		})
		c.persistRunSnapshot(paused.state)
		_ = c.afBoard.UpdateWorkflowRun(runID, "denied")
		c.finishPausedRun(runID, paused, true)
		return nil
	}

	// For approved decisions, we resume execution at the paused policy node.
	// The policy processor will detect the approval decision on the blackboard
	// and transition directly to tool execution natively within the DAG.
	c.persistRunSnapshot(paused.state)
	log.Printf("[DecideApproval] Resuming interactive run: %q starting at node: %q", runID, paused.nodeID)
	if err := c.afEngine.ResumeInteractive(context.Background(), paused.workflow, runID, paused.nodeID); err != nil {
		log.Printf("[DecideApproval] ResumeInteractive returned error: %v", err)
		if isApprovalRequired(err) {
			c.activeMu.Lock()
			c.pausedRuns[runID] = &pausedRun{
				state:    paused.state,
				workflow: paused.workflow,
				nodeID:   paused.state.Phase,
				shadow:   paused.shadow,
			}
			c.activeMu.Unlock()
			c.persistRunSnapshot(paused.state)
			return nil
		}
		paused.state.AddEvent(types.EventErrorEncountered, map[string]string{"error": err.Error()})
		c.persistRunSnapshot(paused.state)
		c.finishPausedRun(runID, paused, true)
		return err
	}
	c.finishPausedRun(runID, paused, false)
	_, err = c.complete(context.Background(), paused.state)
	return err
}

func (c *Coordinator) saveRunMetadata(meta codingAgentsRunMetadata) error {
	if c.afBoard == nil {
		return fmt.Errorf("artifactflow runtime is unavailable")
	}
	return c.afBoard.SaveConfig("codingagents_run", meta.RunID, meta)
}

func (c *Coordinator) loadRunMetadata(runID string) (codingAgentsRunMetadata, error) {
	var meta codingAgentsRunMetadata
	if c.afBoard == nil {
		return meta, fmt.Errorf("artifactflow runtime is unavailable")
	}
	if err := c.afBoard.LoadConfig("codingagents_run", runID, &meta); err != nil {
		return meta, fmt.Errorf("codingagents run metadata unavailable for %s: %w", runID, err)
	}
	return meta, nil
}

func (c *Coordinator) reconstructPausedRun(runID string, approval types.Approval) (*pausedRun, error) {
	meta, err := c.loadRunMetadata(runID)
	if err != nil {
		return nil, err
	}
	cursor, err := c.afBoard.LoadWorkflowCursor(runID)
	if err != nil {
		return nil, err
	}
	if meta.Shadow {
		if meta.ShadowPath == "" {
			return nil, fmt.Errorf("shadow run %s has no persisted shadow path", runID)
		}
		if info, err := os.Stat(meta.ShadowPath); err != nil || !info.IsDir() {
			return nil, fmt.Errorf("shadow workspace for run %s is unavailable: %s", runID, meta.ShadowPath)
		}
		c.Tools.SetBasePath(meta.ShadowPath)
	}
	state, err := c.reconstructCouncilState(runID, meta)
	if err != nil {
		return nil, err
	}
	state.Phase = cursor.CurrentNode
	if state.Phase == "" {
		state.Phase = string(approval.Role)
	}
	paused := &pausedRun{
		state:  state,
		nodeID: cursor.CurrentNode,
		shadow: meta.Shadow,
	}
	c.activeMu.Lock()
	c.activeStates[runID] = state
	c.pausedRuns[runID] = paused
	c.activeMu.Unlock()
	return paused, nil
}

func (c *Coordinator) reconstructCouncilState(runID string, meta codingAgentsRunMetadata) (*types.CouncilState, error) {
	state := types.NewCouncilState(runID, "reconstructed-"+time.Now().Format("150405.000000"))
	state.WorkspaceMode = meta.WorkspaceMode
	state.WorkspaceDir = filepath.Join("logs", runID, "workspace")
	_ = os.MkdirAll(state.WorkspaceDir, 0755)
	messages, err := c.afBoard.GetMessages(runID)
	if err != nil {
		return nil, err
	}
	for _, msg := range messages {
		state.Messages = append(state.Messages, types.Message{
			ID:        msg.ID,
			TraceID:   state.TraceID,
			Role:      types.Role(msg.Role),
			Model:     msg.Model,
			Content:   msg.Content,
			Visible:   msg.Visible,
			Timestamp: msg.CreatedAt,
			Phase:     msg.NodeID,
			Metadata:  msg.Metadata,
		})
	}
	events, err := c.afBoard.GetEvents(runID)
	if err != nil {
		return nil, err
	}
	for _, evt := range events {
		state.Events = append(state.Events, types.StateEvent{
			ID:        evt.ID,
			Type:      types.EventType(evt.Type),
			Timestamp: evt.CreatedAt,
			Phase:     evt.NodeID,
			TraceID:   state.TraceID,
			Payload:   evt.Payload,
		})
	}
	artifacts, err := c.afBoard.GetRunArtifacts(runID)
	if err != nil {
		return nil, err
	}
	for _, art := range artifacts {
		name, _ := art.Metadata["name"].(string)
		content, _ := art.Metadata["content"].(string)
		switch name {
		case "project_map":
			state.Artifacts.ProjectMap = content
		case "technical_plan":
			state.Artifacts.TechnicalPlan = content
		case "execution_logs":
			state.Artifacts.ExecutionLogs = content
		case "critique":
			state.Artifacts.Critique = content
		case "security_report":
			state.Artifacts.SecurityReport = content
		}
	}
	c.attachStateHooks(context.Background(), state)
	return state, nil
}

func (c *Coordinator) recordResumeFailure(runID, approvalID string, err error) {
	now := time.Now()
	_ = c.afBoard.SaveEvent(afstate.Event{
		ID:    fmt.Sprintf("%s-resume-failed-%d", approvalID, now.UnixNano()),
		RunID: runID,
		Type:  string(types.EventErrorEncountered),
		Payload: map[string]any{
			"approval_id": approvalID,
			"error":       err.Error(),
		},
		CreatedAt: now,
	})
	_ = c.afBoard.UpdateWorkflowRun(runID, "resume_failed")
}

func (c *Coordinator) approvalResumeMode(runID string, approval types.Approval) string {
	if approval.Status != types.ApprovalPending {
		return ""
	}
	c.activeMu.RLock()
	_, live := c.pausedRuns[runID]
	c.activeMu.RUnlock()
	if live {
		return "live-resumable"
	}
	if _, err := c.loadRunMetadata(runID); err != nil {
		return "decision-only"
	}
	if _, err := c.afBoard.LoadWorkflowCursor(runID); err != nil {
		return "decision-only"
	}
	return "reconstructable"
}

func (c *Coordinator) findApproval(runID, approvalID string) (types.Approval, error) {
	if strings.TrimSpace(approvalID) == "" {
		return types.Approval{}, fmt.Errorf("approval_id is required")
	}
	approvals, err := c.GetRunApprovals(runID)
	if err != nil {
		return types.Approval{}, err
	}
	for _, approval := range approvals {
		if approval.ID == approvalID {
			return approval, nil
		}
	}
	return types.Approval{}, fmt.Errorf("approval not found: %s", approvalID)
}

func (c *Coordinator) finishPausedRun(runID string, paused *pausedRun, skipComplete bool) {
	c.activeMu.Lock()
	delete(c.pausedRuns, runID)
	delete(c.activeStates, runID)
	c.activeMu.Unlock()
	if paused != nil && paused.shadow {
		c.CleanupShadow()
		c.Tools.SetBasePath("")
	}
	if skipComplete && c.Broadcaster != nil && paused != nil && paused.state != nil {
		c.Broadcaster.Broadcast(types.UIEvent{Type: "session_complete", Payload: paused.state})
	}
}

func isApprovalRequired(err error) bool {
	return errors.Is(err, types.ErrApprovalRequired)
}

func (c *Coordinator) persistArtifactRecord(state *types.CouncilState, name, content string) {
	if content == "" {
		return
	}
	_ = c.afBoard.SaveArtifact(state.SessionID, "workspace", afartifact.Artifact{
		ID:   fmt.Sprintf("%s-%s", state.SessionID, name),
		Kind: afartifact.KindMarkdown,
		URI:  name + ".md",
		MIME: "text/markdown",
		Metadata: map[string]any{
			"name":    name,
			"content": content,
		},
	})
}

func toArtifactFlowMessage(runID string, msg types.Message) afstate.Message {
	metadata := make(map[string]any, len(msg.Metadata))
	for k, v := range msg.Metadata {
		metadata[k] = v
	}
	usage := map[string]any{}
	if msg.Usage != nil {
		usage["total_duration"] = msg.Usage.TotalDuration
		usage["load_duration"] = msg.Usage.LoadDuration
		usage["prompt_eval_count"] = msg.Usage.PromptEvalCount
		usage["prompt_eval_duration"] = msg.Usage.PromptEvalDuration
		usage["eval_count"] = msg.Usage.EvalCount
		usage["eval_duration"] = msg.Usage.EvalDuration
	}
	return afstate.Message{
		ID:        msg.ID,
		RunID:     runID,
		NodeID:    msg.Phase,
		Role:      string(msg.Role),
		Model:     msg.Model,
		Content:   msg.Content,
		Visible:   msg.Visible,
		Metadata:  metadata,
		Usage:     usage,
		CreatedAt: msg.Timestamp,
	}
}

func toArtifactFlowEvent(runID string, evt types.StateEvent) afstate.Event {
	payload := map[string]any{}
	if p, ok := evt.Payload.(map[string]string); ok {
		for k, v := range p {
			payload[k] = v
		}
	} else if p, ok := evt.Payload.(map[string]int); ok {
		for k, v := range p {
			payload[k] = v
		}
	} else if evt.Payload != nil {
		payload["value"] = evt.Payload
	}
	return afstate.Event{
		ID:        evt.ID,
		RunID:     runID,
		NodeID:    evt.Phase,
		Type:      string(evt.Type),
		Payload:   payload,
		CreatedAt: evt.Timestamp,
	}
}

func stringPayload(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	switch v := payload[key].(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		if v != nil {
			return fmt.Sprint(v)
		}
	}
	return ""
}

func parseIntVal(val any) int {
	if val == nil {
		return 0
	}
	switch v := val.(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float64:
		return int(v)
	case float32:
		return int(v)
	case string:
		if parsed, err := strconv.Atoi(v); err == nil {
			return parsed
		}
	}
	return 0
}
