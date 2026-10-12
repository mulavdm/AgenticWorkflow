package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	afprocessor "github.com/ArtifactFlow/ArtifactFlow/pkg/processor"
	afstate "github.com/ArtifactFlow/ArtifactFlow/pkg/state"
	afworkflow "github.com/ArtifactFlow/ArtifactFlow/pkg/workflow"
	"github.com/user/cognitive-orchestrator/internal/agent/summarizer"
	"github.com/user/cognitive-orchestrator/internal/artifactflowbridge"
	"github.com/user/cognitive-orchestrator/internal/memory"
	"github.com/user/cognitive-orchestrator/internal/orchestrator/consensus"
	"github.com/user/cognitive-orchestrator/internal/orchestrator/engine"
	"github.com/user/cognitive-orchestrator/internal/orchestrator/grounding"
	"github.com/user/cognitive-orchestrator/internal/tool"
	"github.com/user/cognitive-orchestrator/internal/types"
	"github.com/user/cognitive-orchestrator/internal/workspace"
)

type Coordinator struct {
	Agents       map[types.Role]engine.Agent
	Summarizer   *summarizer.Summarizer
	Memory       memory.Store
	Embedding    types.EmbeddingProvider
	Tools        *tool.Registry
	Broadcaster  types.Broadcaster
	ReloadAgents func(prompt string)
	GlobalConfig types.GlobalConfig
	BuildVersion string
	Shadow       *workspace.ShadowWorkspace
	CouncilSize  int
	ConfigPath   string

	runtime    *engine.Runtime
	grounder   *grounding.Guard
	consensus  *consensus.Engine
	afBoard    *afstate.Blackboard
	afRegistry *afprocessor.Registry
	afEngine   *afworkflow.Engine

	compactionMu     sync.Mutex
	compactionActive map[string]bool
	activeMu         sync.RWMutex
	activeStates     map[string]*types.CouncilState
	pausedRuns       map[string]*pausedRun

	// LoadedOverrides stores configurations loaded from disk that are waiting for agents to be initialized
	LoadedOverrides map[types.Role]types.AgentConfig
	indexingWg      sync.WaitGroup
	indexingMu      sync.RWMutex
	indexingStatus  IndexerStatus
}

type pausedRun struct {
	state    *types.CouncilState
	workflow *afworkflow.Workflow
	nodeID   string
	shadow   bool
}

type IndexerStatus struct {
	Running        bool      `json:"running"`
	LastStartedAt  time.Time `json:"last_started_at,omitempty"`
	LastFinishedAt time.Time `json:"last_finished_at,omitempty"`
	FilesScanned   int       `json:"files_scanned"`
	FilesIndexed   int       `json:"files_indexed"`
	FilesSkipped   int       `json:"files_skipped"`
	ChunksIndexed  int       `json:"chunks_indexed"`
	LastError      string    `json:"last_error,omitempty"`
}

func NewCoordinator(mem memory.Store, tools *tool.Registry, bc types.Broadcaster, reloader func(string)) *Coordinator {
	agents := make(map[types.Role]engine.Agent)
	guard := grounding.NewGuard(tools, bc)
	runtime := engine.NewRuntime(agents, guard, bc)
	afRegistry := afprocessor.NewRegistry()
	var afBoard *afstate.Blackboard
	if provider, ok := mem.(interface{ ArtifactFlowBoard() *afstate.Blackboard }); ok {
		afBoard = provider.ArtifactFlowBoard()
	} else {
		var err error
		afBoard, err = afstate.NewBlackboard(":memory:")
		if err != nil {
			log.Printf("[Coordinator] ArtifactFlow blackboard unavailable: %v", err)
		}
	}
	var afEngine *afworkflow.Engine
	if afBoard != nil {
		afEngine = afworkflow.NewEngine(afRegistry, afBoard)
	}

	c := &Coordinator{
		Agents:           agents,
		Memory:           mem,
		Tools:            tools,
		Broadcaster:      bc,
		ReloadAgents:     reloader,
		CouncilSize:      3,
		runtime:          runtime,
		grounder:         guard,
		consensus:        consensus.NewEngine(3),
		afBoard:          afBoard,
		afRegistry:       afRegistry,
		afEngine:         afEngine,
		compactionActive: make(map[string]bool),
		activeStates:     make(map[string]*types.CouncilState),
		pausedRuns:       make(map[string]*pausedRun),
		LoadedOverrides:  make(map[types.Role]types.AgentConfig),
	}
	afRegistry.Register(&thinkProcessor{coord: c})
	afRegistry.Register(&groundProcessor{coord: c})
	afRegistry.Register(&policyProcessor{coord: c})
	afRegistry.Register(&executeProcessor{coord: c})
	afRegistry.Register(&routeProcessor{coord: c})

	return c
}

func (c *Coordinator) SetArtifactFlow(board *afstate.Blackboard) {
	c.afBoard = board
	c.afRegistry = afprocessor.NewRegistry()
	c.afRegistry.Register(&thinkProcessor{coord: c})
	c.afRegistry.Register(&groundProcessor{coord: c})
	c.afRegistry.Register(&policyProcessor{coord: c})
	c.afRegistry.Register(&executeProcessor{coord: c})
	c.afRegistry.Register(&routeProcessor{coord: c})
	c.afEngine = afworkflow.NewEngine(c.afRegistry, board)
}

func (c *Coordinator) SetRegistry(registry *afprocessor.Registry) {
	c.afRegistry = registry
	c.afRegistry.Register(&thinkProcessor{coord: c})
	c.afRegistry.Register(&groundProcessor{coord: c})
	c.afRegistry.Register(&policyProcessor{coord: c})
	c.afRegistry.Register(&executeProcessor{coord: c})
	c.afRegistry.Register(&routeProcessor{coord: c})
	if c.afBoard != nil {
		c.afEngine = afworkflow.NewEngine(registry, c.afBoard)
	}
}

func (c *Coordinator) AddAgent(agent engine.Agent) {
	role := agent.GetRole()
	c.Agents[role] = agent

	// If we have a pending override for this role, apply it now
	if override, ok := c.LoadedOverrides[role]; ok {
		log.Printf("[Coordinator] Applying pending override for %s", role)
		agent.UpdateConfig(override)
		delete(c.LoadedOverrides, role)
	}
}

func (c *Coordinator) GetAgents() map[types.Role]engine.Agent {
	return c.Agents
}

func (c *Coordinator) GetIndexerStatus() IndexerStatus {
	c.indexingMu.RLock()
	defer c.indexingMu.RUnlock()
	return c.indexingStatus
}

func (c *Coordinator) GetCapabilityStatuses() []afprocessor.CapabilityState {
	if c.afRegistry == nil {
		return nil
	}
	return c.afRegistry.CapabilityStatuses()
}

func (c *Coordinator) GetToolPolicyConfig() grounding.ToolPolicyConfig {
	return c.grounder.GetToolPolicyConfig()
}

func (c *Coordinator) UpdateToolPolicyConfig(cfg grounding.ToolPolicyConfig) {
	c.grounder.SetToolPolicyConfig(cfg)
	c.SaveState()
}

func (c *Coordinator) runProvenance() RunProvenance {
	out := RunProvenance{
		BuildVersion:     c.BuildVersion,
		DefaultMode:      c.GlobalConfig.DefaultMode,
		StructuredOutput: structuredOutputEnabled(),
		ProviderURLs: map[string]string{
			"ollama":   c.GlobalConfig.OllamaURL,
			"lmstudio": c.GlobalConfig.LMStudioURL,
			"openai":   redactSecretURL(c.GlobalConfig.OpenAIURL),
		},
		Agents: make(map[types.Role]AgentProvenance, len(c.Agents)),
	}
	for role, agent := range c.Agents {
		cfg := agent.GetConfig()
		out.Agents[role] = AgentProvenance{
			Model:            cfg.Model,
			Temperature:      cfg.Temperature,
			MaxTokens:        cfg.MaxTokens,
			ContextLength:    cfg.ContextLength,
			Tools:            append([]string(nil), cfg.Tools...),
			ConstitutionHash: hashString(cfg.Constitution),
			DirectiveHash:    hashString(cfg.Directive),
			SystemPromptHash: hashString(cfg.SystemPrompt),
			Custom:           cfg.IsCustom,
		}
	}
	return out
}

func structuredOutputEnabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("CODINGAGENTS_STRUCTURED_OUTPUT")))
	return value == "1" || value == "true" || value == "json" || value == "schema"
}

func redactSecretURL(raw string) string {
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "@") {
		parts := strings.Split(raw, "@")
		return parts[len(parts)-1]
	}
	return raw
}

func hashString(value string) string {
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (c *Coordinator) UpdateAgentConfig(role types.Role, cfg types.AgentConfig) error {
	agent, ok := c.Agents[role]
	if !ok {
		return fmt.Errorf("agent %s not found", role)
	}
	cfg.IsCustom = true // Mark as overridden
	agent.UpdateConfig(cfg)

	// Persist changes
	c.SaveState()
	return nil
}

func (c *Coordinator) ResetAgent(role types.Role) error {
	if c.ReloadAgents != nil {
		// Just re-run the reloader for this agent?
		// Actually, simpler: clear the Custom flag and trigger a full reload
		agent, ok := c.Agents[role]
		if !ok {
			return fmt.Errorf("agent %s not found", role)
		}
		cfg := agent.GetConfig()
		cfg.IsCustom = false
		agent.UpdateConfig(cfg)

		// Force re-initialization to catch the current global defaults
		c.ReloadAgents("")
		return nil
	}
	return fmt.Errorf("reloader not initialized")
}

func (c *Coordinator) GetGlobalConfig() types.GlobalConfig {
	return c.GlobalConfig
}

func (c *Coordinator) UpdateGlobalConfig(cfg types.GlobalConfig) {
	c.GlobalConfig = cfg
	// Optionally trigger a reload if mode changed
	if c.ReloadAgents != nil {
		c.ReloadAgents("")
	}
	c.SaveState()
}

func (c *Coordinator) SaveState() {
	state := struct {
		Global     types.GlobalConfig               `json:"global"`
		Agents     map[types.Role]types.AgentConfig `json:"agents"`
		ToolPolicy grounding.ToolPolicyConfig       `json:"tool_policy"`
	}{
		Global:     c.GlobalConfig,
		Agents:     make(map[types.Role]types.AgentConfig),
		ToolPolicy: c.grounder.GetToolPolicyConfig(),
	}

	for role, agent := range c.Agents {
		cfg := agent.GetConfig()
		if cfg.IsCustom {
			state.Agents[role] = cfg
		}
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		log.Printf("[Coordinator] Failed to marshal state: %v", err)
		return
	}

	path := c.ConfigPath
	if path == "" {
		path = "agents_config.json"
	}

	_ = os.WriteFile(path, data, 0644)
	log.Printf("[Coordinator] State persisted to %s", path)
}

func (c *Coordinator) LoadState() {
	path := c.ConfigPath
	if path == "" {
		path = "agents_config.json"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return // File might not exist yet
	}

	var state struct {
		Global     types.GlobalConfig               `json:"global"`
		Agents     map[types.Role]types.AgentConfig `json:"agents"`
		ToolPolicy grounding.ToolPolicyConfig       `json:"tool_policy"`
	}

	if err := json.Unmarshal(data, &state); err != nil {
		log.Printf("[Coordinator] Failed to unmarshal state: %v", err)
		return
	}

	c.GlobalConfig = mergeGlobalConfig(c.GlobalConfig, state.Global)
	if state.ToolPolicy.CodeRunnerApprovalThresholdMS > 0 || len(state.ToolPolicy.SafeWritePrefixes) > 0 {
		c.grounder.SetToolPolicyConfig(state.ToolPolicy)
	}
	c.LoadedOverrides = state.Agents
	log.Printf("[Coordinator] Loaded global config and %d manual overrides from %s", len(state.Agents), path)
}

func mergeGlobalConfig(base, override types.GlobalConfig) types.GlobalConfig {
	if override.DefaultMode != "" {
		base.DefaultMode = override.DefaultMode
	}
	if override.OllamaModel != "" {
		base.OllamaModel = override.OllamaModel
	}
	if override.LMStudioModel != "" {
		base.LMStudioModel = override.LMStudioModel
	}
	if override.OpenAIModel != "" {
		base.OpenAIModel = override.OpenAIModel
	}
	if override.OllamaURL != "" {
		base.OllamaURL = override.OllamaURL
	}
	if override.LMStudioURL != "" {
		base.LMStudioURL = override.LMStudioURL
	}
	if override.OpenAIURL != "" {
		base.OpenAIURL = override.OpenAIURL
	}
	if override.OpenAIKey != "" {
		base.OpenAIKey = override.OpenAIKey
	}
	return base
}

func (c *Coordinator) Execute(ctx context.Context, prompt string, targetRole types.Role, graph map[string][]string) ([]types.Message, error) {
	sessionID := "session-" + time.Now().Format("150405.000000")
	return c.ExecuteRun(ctx, sessionID, prompt, targetRole, graph)
}

func (c *Coordinator) ExecuteRun(ctx context.Context, runID string, prompt string, targetRole types.Role, graph map[string][]string) ([]types.Message, error) {
	// 1. Tag Parsing
	if c.ReloadAgents != nil {
		c.ReloadAgents(prompt)
	}
	graph = normalizeGraph(graph)
	if err := artifactflowbridge.ValidateRoleGraph(graph); err != nil {
		return nil, err
	}

	useShadow := strings.Contains(prompt, "@shadow") || c.Shadow != nil
	cleanupShadow := false
	if useShadow {
		if c.Shadow == nil {
			if err := c.EnableShadowMode("."); err != nil {
				return nil, fmt.Errorf("failed to enable shadow mode: %w", err)
			}
			cleanupShadow = true
		}
		// Redirect all tools to the shadow path
		c.Tools.SetBasePath(c.Shadow.ShadowPath)
	}
	defer func() {
		if cleanupShadow {
			c.CleanupShadow()
			c.Tools.SetBasePath("") // Reset tools after run
		}
	}()

	// 2. Initialization
	traceID := "trace-" + time.Now().Format("150405.000000")
	state := types.NewCouncilState(runID, traceID)

	// Support for @legacy tag to disable Shared Workspace and use linear history
	if strings.Contains(prompt, "@legacy") || strings.Contains(prompt, "@linear") {
		state.WorkspaceMode = false
	}

	state.WorkspaceDir = filepath.Join("logs", runID, "workspace")
	_ = os.MkdirAll(state.WorkspaceDir, 0755)

	// Initialize Chain of Thought file
	c.writeCoT(state, "SYSTEM", "Session Initialized. TraceID: "+traceID)

	c.attachStateHooks(ctx, state)

	state.AddEvent(types.EventPhaseStarted, map[string]string{"phase": "initialization", "prompt": prompt})

	// State Anchoring: Inject root context so agents aren't blind

	state.AddMessage(types.RoleUser, prompt, nil, c.Broadcaster)

	// Pre-emptive RAG Context Injection
	if c.Embedding != nil && c.Memory != nil && c.Memory.GetVectorStore() != nil {
		emb, err := c.Embedding.GenerateEmbedding(ctx, prompt)
		if err == nil {
			ids, errSearch := c.Memory.GetVectorStore().SearchMessages(ctx, emb, 5)
			if errSearch == nil && len(ids) > 0 {
				var codeContext strings.Builder
				codeContext.WriteString("[SYSTEM CONTEXT: RELEVANT CODEBASE MATCHES]\n")
				found := false
				for _, id := range ids {
					msg, errGet := c.Memory.GetVectorStore().GetMessage(ctx, id)
					if errGet == nil && msg.Role == "code" {
						found = true
						fileName := msg.Phase
						codeContext.WriteString(fmt.Sprintf("\n--- File: %s ---\n```go\n%s\n```\n", fileName, msg.Content))
					}
				}
				if found {
					state.AddMessage(types.RoleSystem, codeContext.String(), nil, c.Broadcaster)
				}
			}
		}
	}

	if c.afEngine == nil || c.afBoard == nil {
		c.complete(ctx, state)
		return nil, fmt.Errorf("artifactflow runtime is unavailable")
	}

	startRole := targetRole
	if startRole == "" {
		startRole = types.RoleContext
	}
	c.activeMu.Lock()
	c.activeStates[runID] = state
	c.activeMu.Unlock()
	wf := c.compileAgentWorkflow(runID, graph, prompt, targetRole != "")
	if err := c.saveRunMetadata(codingAgentsRunMetadata{
		RunID:         runID,
		Prompt:        prompt,
		TargetRole:    targetRole,
		StartRole:     normalizeRole(startRole),
		Graph:         graph,
		Targeted:      targetRole != "",
		Shadow:        useShadow,
		ShadowPath:    c.shadowPath(),
		WorkspaceMode: state.WorkspaceMode,
		Provenance:    c.runProvenance(),
	}); err != nil {
		return nil, err
	}
	// Persist the initial state (including the user's prompt) to the blackboard before running the engine.
	c.persistRunSnapshot(state)

	startNodeID := string(normalizeRole(startRole))
	if startNodeID != "completed" && startNodeID != "" {
		startNodeID = fmt.Sprintf("%s_think_1", startNodeID)
	}
	if err := c.afEngine.RunInteractive(ctx, wf, runID, startNodeID); err != nil {
		if errors.Is(err, types.ErrApprovalRequired) {
			c.activeMu.Lock()
			latestState := c.activeStates[runID]
			if latestState == nil {
				latestState = state
			}
			c.pausedRuns[runID] = &pausedRun{
				state:    latestState,
				workflow: wf,
				nodeID:   latestState.Phase,
				shadow:   useShadow,
			}
			c.activeMu.Unlock()
			c.persistRunSnapshot(latestState)
			cleanupShadow = false
			return latestState.SnapshotMessages(), nil
		}
		c.activeMu.Lock()
		latestState := c.activeStates[runID]
		if latestState == nil {
			latestState = state
		}
		delete(c.activeStates, runID)
		c.activeMu.Unlock()
		c.complete(ctx, latestState)
		return nil, err
	}

	c.activeMu.Lock()
	latestState := c.activeStates[runID]
	if latestState == nil {
		latestState = state
	}
	delete(c.activeStates, runID)
	c.activeMu.Unlock()
	return c.complete(ctx, latestState)
}

func (c *Coordinator) attachStateHooks(ctx context.Context, state *types.CouncilState) {
	state.OnMessageAdded = func(role types.Role, content string) {
		c.updateArtifacts(state, role, content)
		c.writeCoT(state, string(role), content)

		// Background Indexing for Vector Memory
		if c.Embedding != nil && c.Memory != nil && c.Memory.GetVectorStore() != nil {
			messages := state.SnapshotMessages()
			if len(messages) == 0 {
				return
			}
			msg := messages[len(messages)-1]
			c.indexingWg.Add(1)
			go func(msgID, text, phase string) {
				defer c.indexingWg.Done()
				log.Printf("[Orchestrator] Generating embedding for message %s...", msgID)

				var emb []float32
				var err error
				for attempt := 1; attempt <= 30; attempt++ {
					emb, err = c.Embedding.GenerateEmbedding(ctx, text)
					if err == nil {
						break
					}
					// If it failed, wait and retry (gives the dynamic llama.cpp sidecar time to spawn and become healthy)
					log.Printf("[Orchestrator] Embedding generation attempt %d failed: %v. Retrying in 2s...", attempt, err)
					select {
					case <-ctx.Done():
						return
					case <-time.After(2 * time.Second):
					}
				}

				if err != nil {
					log.Printf("[Orchestrator] Failed to generate embedding for %s after retries: %v", msgID, err)
					return
				}
				log.Printf("[Orchestrator] Embedding generated for %s. Indexing...", msgID)
				err = c.Memory.GetVectorStore().IndexMessage(ctx, msgID, emb, map[string]string{
					"role":    string(role),
					"phase":   phase,
					"content": text,
				})
				if err != nil {
					log.Printf("[Orchestrator] Failed to index message %s: %v", msgID, err)
				}
			}(msg.ID, content, msg.Phase)
		} else if c.Embedding == nil {
			log.Printf("[Orchestrator] Warning: No embedding provider configured. Skipping indexing.")
		}
	}
}

func (c *Coordinator) shadowPath() string {
	if c.Shadow == nil {
		return ""
	}
	return c.Shadow.ShadowPath
}

func normalizeGraph(graph map[string][]string) map[string][]string {
	if len(graph) == 0 {
		return graph
	}

	normalized := make(map[string][]string, len(graph))
	for from, transitions := range graph {
		fromRole := string(normalizeRole(types.Role(from)))
		if _, ok := normalized[fromRole]; !ok {
			normalized[fromRole] = []string{}
		}

		seen := make(map[string]bool, len(normalized[fromRole])+len(transitions))
		for _, existing := range normalized[fromRole] {
			seen[existing] = true
		}

		for _, to := range transitions {
			toRole := string(normalizeRole(types.Role(to)))
			if toRole == "" || seen[toRole] {
				continue
			}
			normalized[fromRole] = append(normalized[fromRole], toRole)
			seen[toRole] = true
		}
	}
	return normalized
}

func normalizeRole(role types.Role) types.Role {
	switch strings.ToLower(string(role)) {
	case "analyst", "context", "context_isolated":
		return types.RoleContext
	case "security", "redteam", "redteamer":
		return types.RoleRedTeamer
	case "critical", "reviewer", "critic":
		return types.RoleCritic
	case "orchestrator", "planner":
		return types.RolePlanner
	case "coder", "writer", "implementer", "developer":
		return types.RoleCoder
	case "synth", "synthesizer":
		return types.RoleSynthesizer
	default:
		return types.Role(strings.ToLower(string(role)))
	}
}

func (c *Coordinator) loadInstructions(role types.Role) string {
	inst, _ := types.LoadPrompt("phase_" + string(role))
	if inst == "" {
		inst = "Execute your mission."
	}
	return inst
}

func (c *Coordinator) complete(ctx context.Context, state *types.CouncilState) ([]types.Message, error) {
	// Wait for all background indexing to complete to avoid overlap printing issues on CLI
	c.indexingWg.Wait()

	state.Phase = "completed"
	if c.Broadcaster != nil {
		c.Broadcaster.Broadcast(types.UIEvent{Type: "session_complete", Payload: state})
	}
	messages := state.SnapshotMessages()
	if c.Memory != nil {
		c.Memory.SaveLessonsFromSession(ctx, state.SessionID, messages)
	}
	return messages, nil
}

// MaterializeArtifacts synchronizes the state artifacts with the current event log.
func (c *Coordinator) MaterializeArtifacts(state *types.CouncilState) {
	// For now, artifacts are updated incrementally in the loop.
	// This function can be expanded to perform full reconstruction from events if needed.
}

// updateArtifacts parses the agent's response for structured tags and updates the state artifacts.
func (c *Coordinator) updateArtifacts(state *types.CouncilState, role types.Role, content string) {
	if !state.WorkspaceMode {
		return
	}

	if role == types.RoleSystem {
		if strings.Contains(content, "[OBSERVATION") {
			state.Artifacts.ExecutionLogs += fmt.Sprintf("\n%s\n", content)
			c.persistArtifact(state, "execution_logs", state.Artifacts.ExecutionLogs)
		}
		return
	}

	// Extract Project Map
	if (role == types.RoleContext || role == types.RoleAnalyst) && strings.Contains(content, "[PROJECT MAP]") {
		state.Artifacts.ProjectMap = extractTagContent(content, "[PROJECT MAP]")
		c.persistArtifact(state, "project_map", state.Artifacts.ProjectMap)
	}

	// Extract Technical Plan
	if role == types.RolePlanner && strings.Contains(content, "[TECHNICAL PLAN]") {
		state.Artifacts.TechnicalPlan = extractTagContent(content, "[TECHNICAL PLAN]")
		c.persistArtifact(state, "technical_plan", state.Artifacts.TechnicalPlan)
	}

	// Extract Execution Logs
	if role == types.RoleCoder && strings.Contains(content, "[EXECUTION LOGS]") {
		state.Artifacts.ExecutionLogs = extractTagContent(content, "[EXECUTION LOGS]")
		c.persistArtifact(state, "execution_logs", state.Artifacts.ExecutionLogs)
	}

	// Extract Critique
	if role == types.RoleCritic && strings.Contains(content, "[CRITIQUE]") {
		state.Artifacts.Critique = extractTagContent(content, "[CRITIQUE]")
		c.persistArtifact(state, "critique", state.Artifacts.Critique)
	}

	// Extract Security Report
	if (role == types.RoleRedTeamer || role == types.RoleSecurity) && strings.Contains(content, "[SECURITY REPORT]") {
		state.Artifacts.SecurityReport = extractTagContent(content, "[SECURITY REPORT]")
		c.persistArtifact(state, "security_report", state.Artifacts.SecurityReport)
	}
}

func (c *Coordinator) persistArtifact(state *types.CouncilState, name, content string) {
	if state.WorkspaceDir == "" {
		return
	}
	path := filepath.Join(state.WorkspaceDir, name+".md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		log.Printf("[Coordinator] Error persisting artifact %s: %v", name, err)
	}
}

func (c *Coordinator) writeCoT(state *types.CouncilState, role, content string) {
	if state.WorkspaceDir == "" {
		return
	}
	path := filepath.Join(state.WorkspaceDir, "chain_of_thought.log")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("[Coordinator] Error opening CoT file: %v", err)
		return
	}
	defer f.Close()

	timestamp := time.Now().Format("15:04:05")
	entry := fmt.Sprintf("[%s] [%s]: %s\n", timestamp, strings.ToUpper(role), content)
	if _, err := f.WriteString(entry); err != nil {
		log.Printf("[Coordinator] Error writing to CoT file: %v", err)
	}
}

func (c *Coordinator) CompactHistory(ctx context.Context, state *types.CouncilState) {
	if c.Summarizer == nil {
		return
	}

	// Threshold: Summarize everything older than the last 6 messages
	// to ensure immediate context (Tool -> Observation -> Planner -> Coder) remains raw.
	recentThreshold := 6

	// Prevent multiple compactions on the same session
	c.compactionMu.Lock()
	if c.compactionActive[state.SessionID] {
		c.compactionMu.Unlock()
		return
	}
	c.compactionActive[state.SessionID] = true
	c.compactionMu.Unlock()

	defer func() {
		c.compactionMu.Lock()
		delete(c.compactionActive, state.SessionID)
		c.compactionMu.Unlock()
	}()

	// Use a dedicated timeout for compaction to ensure it doesn't run forever
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	log.Printf("[Orchestrator] Starting background compaction for session %s...", state.SessionID)

	// Copy messages to work on them outside the main lock
	state.RLock()
	msgs := make([]types.Message, len(state.Messages))
	copy(msgs, state.Messages)
	state.RUnlock()

	if len(msgs) <= recentThreshold {
		return
	}

	// Dynamically update Summarizer endpoint before summarization to match the active GGUF sidecar port
	if c.afBoard != nil && c.Summarizer != nil {
		var dbEndpoint string
		modelName := c.GlobalConfig.OpenAIModel
		if strings.Contains(strings.ToLower(modelName), ".gguf") {
			if boardErr := c.afBoard.LoadConfig("runtimes", "llama.cpp:"+modelName, &dbEndpoint); boardErr == nil && dbEndpoint != "" {
				log.Printf("[Orchestrator] Updating Summarizer active endpoint: %s/chat/completions", dbEndpoint)
				c.Summarizer.UpdateURL(dbEndpoint + "/chat/completions")
			}
		}
	}

	var msgsToCompact []types.Message
	for i := 0; i < len(msgs)-recentThreshold; i++ {
		msg := msgs[i]
		if msg.Summary == "" && len(msg.Content) >= 2000 && msg.Role == types.RoleSystem {
			msgsToCompact = append(msgsToCompact, msg)
		}
	}

	if len(msgsToCompact) == 0 {
		log.Printf("[Orchestrator] No messages need compaction for session %s.", state.SessionID)
		return
	}

	for _, m := range msgsToCompact {
		log.Printf("[Orchestrator] Compacting message %s (%s, len: %d)...", m.ID, m.Role, len(m.Content))

		// Use a fresh sub-timeout per message
		subCtx, subCancel := context.WithTimeout(ctx, 60*time.Second)
		summary, err := c.Summarizer.SummarizeWithRetry(subCtx, m.Content)
		subCancel()

		if err != nil {
			log.Printf("[Orchestrator] Failed to compact message %s: %v", m.ID, err)
			continue
		}

		// Update the message in the actual state using thread-safe method
		state.SetMessageSummary(m.ID, summary)

		// Background update to Vector Store with the semantic summary
		if c.Embedding != nil && c.Memory != nil && c.Memory.GetVectorStore() != nil {
			emb, _ := c.Embedding.GenerateEmbedding(ctx, summary)
			if emb != nil {
				_ = c.Memory.GetVectorStore().IndexMessage(ctx, m.ID, emb, map[string]string{
					"role":    string(m.Role),
					"type":    "summary",
					"phase":   m.Phase,
					"content": m.Content,
					"summary": summary,
				})
			}
		}
	}
	log.Printf("[Orchestrator] Background compaction complete for session %s.", state.SessionID)
}

func extractTagContent(content, tag string) string {
	parts := strings.Split(content, tag)
	if len(parts) < 2 {
		return ""
	}
	// Take the content after the tag until the next tag or end of string
	rest := parts[1]
	// Look for the next [ tag
	nextTagIdx := strings.Index(rest, "[")
	if nextTagIdx != -1 {
		return strings.TrimSpace(rest[:nextTagIdx])
	}
	return strings.TrimSpace(rest)
}

type codeChunk struct {
	ID        string
	Content   string
	StartLine int
	EndLine   int
	Label     string
}

func fileSHA256Hash(data []byte) string {
	h := sha256.New()
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

func chunkGoFile(path string, content string) []codeChunk {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, content, parser.ParseComments)
	if err != nil {
		// Fallback to standard line chunking if there is an AST parsing error (e.g. invalid syntax during edits)
		return fallbackLineChunking(path, content)
	}

	lines := strings.Split(content, "\n")
	var chunks []codeChunk

	// 1. Group package and imports
	var importEnd = 1
	if file.Name != nil {
		importEnd = fset.Position(file.Name.End()).Line
	}
	for _, imp := range file.Imports {
		if fset.Position(imp.End()).Line > importEnd {
			importEnd = fset.Position(imp.End()).Line
		}
	}
	if importEnd > 0 && importEnd <= len(lines) {
		pkgContent := strings.Join(lines[0:importEnd], "\n")
		if len(strings.TrimSpace(pkgContent)) > 0 {
			chunks = append(chunks, codeChunk{
				ID:        fmt.Sprintf("code-%s-imports", path),
				Content:   pkgContent,
				StartLine: 1,
				EndLine:   importEnd,
				Label:     "package_imports",
			})
		}
	}

	// 2. Iterate declarations (structs, interfaces, funcs, methods)
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			// Handle type, var, const declarations
			for _, spec := range d.Specs {
				if typeSpec, ok := spec.(*ast.TypeSpec); ok {
					start := fset.Position(d.Pos()).Line
					end := fset.Position(d.End()).Line
					if start <= len(lines) && end <= len(lines) && start > 0 && end > 0 {
						declContent := strings.Join(lines[start-1:end], "\n")
						if len(strings.TrimSpace(declContent)) > 0 {
							chunks = append(chunks, codeChunk{
								ID:        fmt.Sprintf("code-%s-type-%s", path, typeSpec.Name.Name),
								Content:   declContent,
								StartLine: start,
								EndLine:   end,
								Label:     typeSpec.Name.Name,
							})
						}
					}
				}
			}
		case *ast.FuncDecl:
			// Handle functions and methods
			start := fset.Position(d.Pos()).Line
			end := fset.Position(d.End()).Line
			if start <= len(lines) && end <= len(lines) && start > 0 && end > 0 {
				funcContent := strings.Join(lines[start-1:end], "\n")
				if len(strings.TrimSpace(funcContent)) > 0 {
					name := d.Name.Name
					if d.Recv != nil && len(d.Recv.List) > 0 {
						recvType := ""
						switch t := d.Recv.List[0].Type.(type) {
						case *ast.Ident:
							recvType = t.Name
						case *ast.StarExpr:
							if ident, ok := t.X.(*ast.Ident); ok {
								recvType = ident.Name
							}
						}
						if recvType != "" {
							name = recvType + "." + name
						}
					}
					chunks = append(chunks, codeChunk{
						ID:        fmt.Sprintf("code-%s-func-%s", path, name),
						Content:   funcContent,
						StartLine: start,
						EndLine:   end,
						Label:     name,
					})
				}
			}
		}
	}

	// If no chunks were created, fallback to line chunking
	if len(chunks) == 0 {
		return fallbackLineChunking(path, content)
	}

	return chunks
}

func fallbackLineChunking(path string, content string) []codeChunk {
	lines := strings.Split(content, "\n")
	chunkSize := 50
	var chunks []codeChunk
	for i := 0; i < len(lines); i += chunkSize {
		end := i + chunkSize
		if end > len(lines) {
			end = len(lines)
		}
		chunkContent := strings.Join(lines[i:end], "\n")
		if len(strings.TrimSpace(chunkContent)) < 50 {
			continue
		}
		chunks = append(chunks, codeChunk{
			ID:        fmt.Sprintf("code-%s-L%d-L%d", path, i+1, end),
			Content:   chunkContent,
			StartLine: i + 1,
			EndLine:   end,
			Label:     fmt.Sprintf("lines %d-%d", i+1, end),
		})
	}
	return chunks
}

func (c *Coordinator) IndexCodebase(ctx context.Context) {
	if c.Embedding == nil || c.Memory == nil || c.Memory.GetVectorStore() == nil {
		return
	}

	log.Printf("[CodebaseIndexer] Starting background codebase indexing...")
	c.indexingMu.Lock()
	c.indexingStatus = IndexerStatus{
		Running:       true,
		LastStartedAt: time.Now(),
	}
	c.indexingMu.Unlock()

	root := "."
	status := IndexerStatus{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return nil
		}
		if info.IsDir() {
			name := info.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules" || name == "logs" || name == "scratch" || name == "bin" || name == "build") {
				return filepath.SkipDir
			}
			return nil
		}

		ext := strings.ToLower(filepath.Ext(info.Name()))
		if ext != ".go" && ext != ".md" && ext != ".json" && ext != ".txt" && ext != ".yaml" && ext != ".yml" {
			return nil
		}
		status.FilesScanned++

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		content := string(data)
		if len(strings.TrimSpace(content)) == 0 {
			return nil
		}

		normalizedPath := filepath.ToSlash(path)

		// Incremental Hashing Cache check
		currentHash := fileSHA256Hash(data)
		var cachedHash string
		hasCache := false
		if c.afBoard != nil {
			if errHash := c.afBoard.LoadConfig("file_hashes", normalizedPath, &cachedHash); errHash == nil {
				hasCache = true
			}
		}

		if hasCache && cachedHash == currentHash {
			status.FilesSkipped++
			return nil // Skip unchanged file!
		}

		log.Printf("[CodebaseIndexer] Indexing changed/new file: %s", normalizedPath)
		status.FilesIndexed++

		// Clean up any stale chunks/vectors for this file path before re-indexing
		_ = c.Memory.GetVectorStore().DeletePrefix(ctx, "code-"+normalizedPath)

		var chunks []codeChunk
		if ext == ".go" {
			chunks = chunkGoFile(normalizedPath, content)
		} else {
			chunks = fallbackLineChunking(normalizedPath, content)
		}

		for _, chunk := range chunks {
			emb, errEmb := c.Embedding.GenerateEmbedding(ctx, chunk.Content)
			if errEmb != nil {
				continue
			}

			errIdx := c.Memory.GetVectorStore().IndexMessage(ctx, chunk.ID, emb, map[string]string{
				"kind":               "code",
				"role":               "code",
				"phase":              normalizedPath,
				"source_path":        normalizedPath,
				"content":            chunk.Content,
				"embedding_model_id": embeddingModelID(c.Embedding),
			})
			if errIdx == nil {
				status.ChunksIndexed++
			}
		}

		// Save new hash to cache
		if c.afBoard != nil {
			_ = c.afBoard.SaveConfig("file_hashes", normalizedPath, currentHash)
		}

		return nil
	})

	if err != nil {
		log.Printf("[CodebaseIndexer] Codebase indexing failed: %v", err)
	} else {
		log.Printf("[CodebaseIndexer] Codebase indexing completed. Indexed %d new/updated chunks.", status.ChunksIndexed)
	}
	c.indexingMu.Lock()
	status.Running = false
	status.LastStartedAt = c.indexingStatus.LastStartedAt
	status.LastFinishedAt = time.Now()
	if err != nil {
		status.LastError = err.Error()
	}
	c.indexingStatus = status
	c.indexingMu.Unlock()
}

func embeddingModelID(provider types.EmbeddingProvider) string {
	if provider == nil {
		return ""
	}
	if named, ok := provider.(interface{ GetModel() string }); ok {
		return named.GetModel()
	}
	return fmt.Sprintf("%T", provider)
}
