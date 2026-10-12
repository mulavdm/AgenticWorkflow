package ui

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	afartifact "github.com/ArtifactFlow/ArtifactFlow/pkg/artifact"
	afprocessor "github.com/ArtifactFlow/ArtifactFlow/pkg/processor"
	afstate "github.com/ArtifactFlow/ArtifactFlow/pkg/state"
	logger "github.com/user/CentralLogger"
	"github.com/user/cognitive-orchestrator/internal/agent"
	"github.com/user/cognitive-orchestrator/internal/memory"
	"github.com/user/cognitive-orchestrator/internal/orchestrator"
	"github.com/user/cognitive-orchestrator/internal/orchestrator/engine"
	"github.com/user/cognitive-orchestrator/internal/orchestrator/grounding"
	"github.com/user/cognitive-orchestrator/internal/types"
)

type TaskExecutor interface {
	Execute(ctx context.Context, prompt string, targetRole types.Role, graph map[string][]string) ([]types.Message, error)
	ExecuteRun(ctx context.Context, runID string, prompt string, targetRole types.Role, graph map[string][]string) ([]types.Message, error)
	GetAgents() map[types.Role]engine.Agent
	UpdateAgentConfig(role types.Role, cfg types.AgentConfig) error
	ResetAgent(role types.Role) error
	GetGlobalConfig() types.GlobalConfig
	UpdateGlobalConfig(cfg types.GlobalConfig)
	GetRunMessages(runID string) ([]afstate.Message, error)
	GetRunEvents(runID string) ([]afstate.Event, error)
	GetRunArtifacts(runID string) ([]afartifact.Artifact, error)
	GetRuns(limit int) ([]afstate.WorkflowRun, error)
	GetRun(runID string) (*afstate.WorkflowRun, error)
	GetRunTimeline(runID string) ([]afstate.TimelineEntry, error)
	ReconstructRun(runID string) (*afstate.RunSnapshot, error)
	CancelRun(runID string) error
	GetRunApprovals(runID string) ([]types.Approval, error)
	DecideApproval(runID, approvalID, decision, reason string) error
	GetIndexerStatus() orchestrator.IndexerStatus
	GetCapabilityStatuses() []afprocessor.CapabilityState
	GetToolPolicyConfig() grounding.ToolPolicyConfig
	UpdateToolPolicyConfig(grounding.ToolPolicyConfig)
}

type Server struct {
	sse           *SSESink
	memory        memory.Store
	executor      TaskExecutor
	activeMu      sync.Mutex
	activeCancels map[string]context.CancelFunc
}

func NewServer(sse *SSESink, mem memory.Store, executor TaskExecutor) *Server {
	return &Server{
		sse:           sse,
		memory:        mem,
		executor:      executor,
		activeCancels: make(map[string]context.CancelFunc),
	}
}

func (s *Server) Start(addr string) error {
	log.Printf("[UI] Dashboard started on http://%s", addr)
	return http.ListenAndServe(addr, s.Handler())
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// SSE Endpoint
	mux.HandleFunc("/api/stream", s.sse.SSEHandler)

	// Memory Endpoint
	mux.HandleFunc("/api/memory", s.handleMemory)
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/indexer/status", s.handleIndexerStatus)

	// Execute Endpoint
	mux.HandleFunc("/api/execute", s.handleExecute)
	mux.HandleFunc("/api/execute/cancel", s.handleExecuteCancel)
	mux.HandleFunc("/api/runs", s.handleRuns)
	mux.HandleFunc("/api/runs/", s.handleRunDetail)
	// Models Endpoint
	mux.HandleFunc("/api/models", s.handleModels)
	mux.HandleFunc("/api/providers/probe", s.handleProviderProbe)
	// Agents Endpoints
	mux.HandleFunc("/api/agents", s.handleAgents)
	mux.HandleFunc("/api/agents/", s.handleAgentDetail)
	mux.HandleFunc("/api/agents/constitution/", s.handleAgentConstitution)

	// Global Config Endpoint
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/tool-policy", s.handleToolPolicy)

	// Static Files
	mux.Handle("/", http.FileServer(http.Dir(types.ResolveAssetPath("web"))))

	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	capabilities := s.executor.GetCapabilityStatuses()
	payload := map[string]any{
		"ok":             true,
		"logger":         logger.GetDiagnostics(),
		"indexer":        s.executor.GetIndexerStatus(),
		"capabilities":   capabilities,
		"memory_present": s.memory != nil,
	}
	for _, capability := range capabilities {
		switch capability.Status {
		case afprocessor.CapabilityFailed, afprocessor.CapabilityCancelled, afprocessor.CapabilityMissing:
			payload["ok"] = false
			payload["capability_error"] = capability.Name + ": " + string(capability.Status)
		}
	}
	if afStore, ok := s.memory.(*memory.ArtifactFlowStore); ok && afStore.ArtifactFlowBoard() != nil {
		if version, err := afStore.ArtifactFlowBoard().SchemaVersion(); err == nil {
			payload["blackboard_schema_version"] = version
		} else {
			payload["ok"] = false
			payload["blackboard_error"] = err.Error()
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(payload)
}

func (s *Server) handleIndexerStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.executor.GetIndexerStatus())
}

func (s *Server) handleRunDetail(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/runs/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "expected /api/runs/{run_id}", http.StatusBadRequest)
		return
	}
	runID := parts[0]
	var payload any
	var err error
	resource := ""
	if len(parts) > 1 {
		resource = parts[1]
	}
	switch resource {
	case "":
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		payload, err = s.executor.GetRun(runID)
	case "messages":
		if r.Method == http.MethodGet {
			payload, err = s.executor.GetRunMessages(runID)
			break
		}
		if len(parts) == 4 && r.Method == http.MethodPost {
			messageID := parts[2]
			action := parts[3]
			var visible bool
			if action == "hide" {
				visible = false
			} else if action == "unhide" {
				visible = true
			} else {
				http.Error(w, "Unknown action, expected hide or unhide", http.StatusBadRequest)
				return
			}

			if afStore, ok := s.memory.(*memory.ArtifactFlowStore); ok {
				err = afStore.ArtifactFlowBoard().UpdateMessageVisibility(messageID, visible)
			} else {
				http.Error(w, "Underlying memory store does not support updating message visibility", http.StatusBadRequest)
				return
			}

			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			payload = map[string]any{
				"status":     "success",
				"message_id": messageID,
				"visible":    visible,
			}
			break
		}
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	case "events":
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		payload, err = s.executor.GetRunEvents(runID)
	case "artifacts":
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		payload, err = s.executor.GetRunArtifacts(runID)
	case "timeline":
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		payload, err = s.executor.GetRunTimeline(runID)
	case "snapshot":
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		payload, err = s.executor.ReconstructRun(runID)
	case "approvals":
		if len(parts) == 2 && r.Method == http.MethodGet {
			payload, err = s.executor.GetRunApprovals(runID)
			break
		}
		if len(parts) == 3 && r.Method == http.MethodPost {
			var req struct {
				Decision string `json:"decision"`
				Reason   string `json:"reason"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if err := s.executor.DecideApproval(runID, parts[2], req.Decision, req.Reason); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			payload = map[string]string{"status": "recorded"}
			break
		}
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	default:
		http.Error(w, "unknown run resource", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(payload)
}

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	runs, err := s.executor.GetRuns(limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(runs)
}

func (s *Server) handleMemory(w http.ResponseWriter, r *http.Request) {
	lessons, err := s.memory.GetRelevantLessons(r.Context(), "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(lessons)
}

func (s *Server) handleExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Prompt string              `json:"prompt"`
		Role   types.Role          `json:"role"`
		Graph  map[string][]string `json:"graph"`
		Preset string              `json:"graph_preset"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Prompt) == "" {
		http.Error(w, "prompt is required", http.StatusBadRequest)
		return
	}
	if len(req.Graph) == 0 && req.Preset != "" {
		req.Graph = orchestrator.RoleGraphPreset(req.Preset)
		if req.Graph == nil {
			http.Error(w, "unknown graph_preset", http.StatusBadRequest)
			return
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	runID := "session-" + time.Now().Format("150405.000000")
	s.activeMu.Lock()
	if len(s.activeCancels) > 0 {
		s.activeMu.Unlock()
		cancel()
		http.Error(w, "another execution is already running", http.StatusConflict)
		return
	}
	s.activeCancels[runID] = cancel
	s.activeMu.Unlock()

	// Trigger execution in background so SSE can stream it
	go func() {
		defer func() {
			s.activeMu.Lock()
			delete(s.activeCancels, runID)
			s.activeMu.Unlock()
			cancel()
		}()

		if _, err := s.executor.ExecuteRun(ctx, runID, req.Prompt, req.Role, req.Graph); err != nil {
			log.Printf("[UI] execution failed: %v", err)
			s.sse.OnEvent(types.UIEvent{
				Type: "session_error",
				Payload: types.Message{
					ID:        time.Now().Format("20060102150405.000000"),
					Role:      types.RoleSystem,
					Content:   "Execution failed: " + err.Error(),
					Visible:   true,
					Timestamp: time.Now(),
					Phase:     "error",
				},
			})
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"run_id": runID})
}

func (s *Server) handleExecuteCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		RunID string `json:"run_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	s.activeMu.Lock()
	var cancel context.CancelFunc
	if req.RunID != "" {
		cancel = s.activeCancels[req.RunID]
		delete(s.activeCancels, req.RunID)
	} else if len(s.activeCancels) == 1 {
		for runID, fn := range s.activeCancels {
			req.RunID = runID
			cancel = fn
			delete(s.activeCancels, runID)
		}
	}
	s.activeMu.Unlock()
	if cancel == nil {
		if req.RunID != "" {
			if err := s.executor.CancelRun(req.RunID); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusAccepted)
			return
		}
		http.Error(w, "no active execution", http.StatusNotFound)
		return
	}

	cancel()
	if req.RunID != "" {
		_ = s.executor.CancelRun(req.RunID)
	}
	w.WriteHeader(http.StatusAccepted)
}
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	models := agent.DiscoverModels()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models)
}

func (s *Server) handleProviderProbe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.URL) == "" {
		http.Error(w, "url is required", http.StatusBadRequest)
		return
	}
	probe := agent.ProbeProvider(r.Context(), req.URL)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(probe)
}

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	agents := s.executor.GetAgents()
	configs := make(map[string]types.AgentConfig)
	for role, a := range agents {
		configs[string(role)] = a.GetConfig()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(configs)
}

func (s *Server) handleAgentDetail(w http.ResponseWriter, r *http.Request) {
	role := types.Role(r.URL.Path[len("/api/agents/"):])

	if r.Method == http.MethodGet {
		agents := s.executor.GetAgents()
		a, ok := agents[role]
		if !ok {
			http.Error(w, "Agent not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(a.GetConfig())
		return
	}

	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		// Check for reset action
		if strings.HasSuffix(r.URL.Path, "/reset") {
			role = types.Role(strings.TrimSuffix(r.URL.Path[len("/api/agents/"):], "/reset"))
			if err := s.executor.ResetAgent(role); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}

		var cfg types.AgentConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := s.executor.UpdateAgentConfig(role, cfg); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		cfg := s.executor.GetGlobalConfig()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cfg)
		return
	}

	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		var cfg types.GlobalConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.executor.UpdateGlobalConfig(cfg)
		w.WriteHeader(http.StatusOK)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleToolPolicy(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s.executor.GetToolPolicyConfig())
		return
	}
	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		var cfg grounding.ToolPolicyConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.executor.UpdateToolPolicyConfig(cfg)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleAgentConstitution(w http.ResponseWriter, r *http.Request) {
	// Pattern: /api/agents/constitution/{role}
	roleStr := strings.TrimPrefix(r.URL.Path, "/api/agents/constitution/")

	// Map ANY variant (role or package name) to the folder name
	pkgMap := map[string]string{
		"analyst":     "analyst",
		"context":     "analyst",
		"planner":     "planner",
		"critic":      "critic",
		"security":    "security",
		"redteamer":   "security",
		"coder":       "coder",
		"synthesizer": "synthesizer",
	}

	pkgName, ok := pkgMap[roleStr]
	if !ok {
		// Fallback to raw string if not in map
		pkgName = roleStr
	}

	content, err := types.LoadConstitution(pkgName)
	if err != nil {
		log.Printf("[Server] Constitution fetch failed for %s (%s): %v", roleStr, pkgName, err)
		http.Error(w, "Constitution not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/markdown")
	w.Write([]byte(content))
}
