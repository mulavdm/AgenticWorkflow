package ui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	afartifact "github.com/ArtifactFlow/ArtifactFlow/pkg/artifact"
	afprocessor "github.com/ArtifactFlow/ArtifactFlow/pkg/processor"
	afstate "github.com/ArtifactFlow/ArtifactFlow/pkg/state"
	"github.com/user/cognitive-orchestrator/internal/memory"
	"github.com/user/cognitive-orchestrator/internal/orchestrator"
	"github.com/user/cognitive-orchestrator/internal/orchestrator/engine"
	"github.com/user/cognitive-orchestrator/internal/orchestrator/grounding"
	"github.com/user/cognitive-orchestrator/internal/types"
)

type apiMockMemory struct{}

func (m *apiMockMemory) SaveLesson(context.Context, memory.Lesson) error { return nil }
func (m *apiMockMemory) SaveLessonsFromSession(context.Context, string, []types.Message) error {
	return nil
}
func (m *apiMockMemory) GetRelevantLessons(context.Context, string) ([]memory.Lesson, error) {
	return []memory.Lesson{}, nil
}
func (m *apiMockMemory) GetVectorStore() types.VectorStore { return nil }
func (m *apiMockMemory) Close() error                      { return nil }

type apiMockExecutor struct {
	mu        sync.Mutex
	started   chan string
	cancelled chan struct{}
	runs      []afstate.WorkflowRun
	decisions []types.Approval
	policy    grounding.ToolPolicyConfig
	caps      []afprocessor.CapabilityState
}

func newAPIMockExecutor() *apiMockExecutor {
	return &apiMockExecutor{
		started:   make(chan string, 1),
		cancelled: make(chan struct{}, 1),
	}
}

func (m *apiMockExecutor) Execute(context.Context, string, types.Role, map[string][]string) ([]types.Message, error) {
	return nil, nil
}

func (m *apiMockExecutor) ExecuteRun(ctx context.Context, runID string, prompt string, targetRole types.Role, graph map[string][]string) ([]types.Message, error) {
	m.mu.Lock()
	m.runs = append(m.runs, afstate.WorkflowRun{RunID: runID, WorkflowName: "codingagents-agentic-runtime", Status: "running", StartedAt: time.Now()})
	m.mu.Unlock()
	m.started <- runID
	<-ctx.Done()
	m.cancelled <- struct{}{}
	return nil, ctx.Err()
}

func (m *apiMockExecutor) GetAgents() map[types.Role]engine.Agent {
	return map[types.Role]engine.Agent{}
}
func (m *apiMockExecutor) UpdateAgentConfig(types.Role, types.AgentConfig) error {
	return nil
}
func (m *apiMockExecutor) ResetAgent(types.Role) error           { return nil }
func (m *apiMockExecutor) GetGlobalConfig() types.GlobalConfig   { return types.GlobalConfig{} }
func (m *apiMockExecutor) UpdateGlobalConfig(types.GlobalConfig) {}
func (m *apiMockExecutor) GetRunMessages(runID string) ([]afstate.Message, error) {
	return []afstate.Message{{ID: "msg-1", RunID: runID, Role: "planner", Content: "plan", Visible: true}}, nil
}
func (m *apiMockExecutor) GetRunEvents(runID string) ([]afstate.Event, error) {
	return []afstate.Event{{ID: "evt-1", RunID: runID, Type: "phase_started"}}, nil
}
func (m *apiMockExecutor) GetRunArtifacts(runID string) ([]afartifact.Artifact, error) {
	return []afartifact.Artifact{{ID: "art-1", Kind: afartifact.KindMarkdown, URI: "plan.md"}}, nil
}
func (m *apiMockExecutor) GetRuns(limit int) ([]afstate.WorkflowRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]afstate.WorkflowRun(nil), m.runs...), nil
}
func (m *apiMockExecutor) GetRun(runID string) (*afstate.WorkflowRun, error) {
	return &afstate.WorkflowRun{RunID: runID, WorkflowName: "codingagents-agentic-runtime", Status: "running", StartedAt: time.Now()}, nil
}
func (m *apiMockExecutor) GetRunTimeline(runID string) ([]afstate.TimelineEntry, error) {
	return []afstate.TimelineEntry{{Kind: "message", ID: "msg-1", RunID: runID, Role: "planner", Content: "plan"}}, nil
}
func (m *apiMockExecutor) ReconstructRun(runID string) (*afstate.RunSnapshot, error) {
	return &afstate.RunSnapshot{Run: afstate.WorkflowRun{RunID: runID, WorkflowName: "codingagents-agentic-runtime", Status: "running"}}, nil
}
func (m *apiMockExecutor) CancelRun(runID string) error { return nil }
func (m *apiMockExecutor) GetRunApprovals(runID string) ([]types.Approval, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.decisions) > 0 {
		return append([]types.Approval(nil), m.decisions...), nil
	}
	return []types.Approval{{
		ID:     "approval-1",
		RunID:  runID,
		Role:   types.RoleCoder,
		Tool:   "writer",
		Reason: "writer path is outside scratch",
		Status: types.ApprovalPending,
	}}, nil
}
func (m *apiMockExecutor) DecideApproval(runID, approvalID, decision, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	status := types.ApprovalDenied
	if decision == "approved" || decision == "approve" {
		status = types.ApprovalApproved
	}
	m.decisions = []types.Approval{{
		ID:             approvalID,
		RunID:          runID,
		Status:         status,
		DecisionReason: reason,
	}}
	return nil
}

func (m *apiMockExecutor) GetIndexerStatus() orchestrator.IndexerStatus {
	return orchestrator.IndexerStatus{FilesScanned: 2, FilesIndexed: 1, ChunksIndexed: 3}
}
func (m *apiMockExecutor) GetCapabilityStatuses() []afprocessor.CapabilityState {
	if m.caps != nil {
		return append([]afprocessor.CapabilityState(nil), m.caps...)
	}
	return []afprocessor.CapabilityState{{
		Name:        "llama.cpp:Gemma-4-E4B-Q8_K_P.gguf",
		Status:      afprocessor.CapabilityReady,
		PID:         1234,
		EndpointURL: "http://127.0.0.1:8081/v1",
	}}
}
func (m *apiMockExecutor) GetToolPolicyConfig() grounding.ToolPolicyConfig {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.policy.CodeRunnerApprovalThresholdMS == 0 && len(m.policy.SafeWritePrefixes) == 0 {
		return grounding.DefaultToolPolicyConfig()
	}
	return m.policy
}
func (m *apiMockExecutor) UpdateToolPolicyConfig(cfg grounding.ToolPolicyConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.policy = cfg
}

func TestServerExecuteReturnsRunIDAndCancelUsesRunID(t *testing.T) {
	exec := newAPIMockExecutor()
	server := NewServer(NewSSESink(), &apiMockMemory{}, exec)

	req := httptest.NewRequest(http.MethodPost, "/api/execute", strings.NewReader(`{"prompt":"plan","graph_preset":"planner-only"}`))
	rec := httptest.NewRecorder()
	server.handleExecute(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("execute status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode execute response: %v", err)
	}
	if body.RunID == "" {
		t.Fatal("expected run_id")
	}

	select {
	case got := <-exec.started:
		if got != body.RunID {
			t.Fatalf("started run %q, response run %q", got, body.RunID)
		}
	case <-time.After(time.Second):
		t.Fatal("run did not start")
	}

	cancelReq := httptest.NewRequest(http.MethodPost, "/api/execute/cancel", strings.NewReader(`{"run_id":"`+body.RunID+`"}`))
	cancelRec := httptest.NewRecorder()
	server.handleExecuteCancel(cancelRec, cancelReq)
	if cancelRec.Code != http.StatusAccepted {
		t.Fatalf("cancel status = %d, body = %s", cancelRec.Code, cancelRec.Body.String())
	}
	select {
	case <-exec.cancelled:
	case <-time.After(time.Second):
		t.Fatal("run context was not cancelled")
	}
}

func TestServerRunDetailEndpoints(t *testing.T) {
	exec := newAPIMockExecutor()
	server := NewServer(NewSSESink(), &apiMockMemory{}, exec)

	for _, path := range []string{
		"/api/runs/run-1",
		"/api/runs/run-1/messages",
		"/api/runs/run-1/events",
		"/api/runs/run-1/artifacts",
		"/api/runs/run-1/timeline",
		"/api/runs/run-1/snapshot",
		"/api/runs/run-1/approvals",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		server.handleRunDetail(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, body = %s", path, rec.Code, rec.Body.String())
		}
	}
}

func TestServerApprovalDecisionEndpoint(t *testing.T) {
	exec := newAPIMockExecutor()
	server := NewServer(NewSSESink(), &apiMockMemory{}, exec)

	req := httptest.NewRequest(http.MethodPost, "/api/runs/run-1/approvals/approval-1", strings.NewReader(`{"decision":"approved","reason":"scoped"}`))
	rec := httptest.NewRecorder()
	server.handleRunDetail(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("approval decision status = %d, body = %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/runs/run-1/approvals", nil)
	rec = httptest.NewRecorder()
	server.handleRunDetail(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("approval list status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var approvals []types.Approval
	if err := json.NewDecoder(rec.Body).Decode(&approvals); err != nil {
		t.Fatalf("decode approvals: %v", err)
	}
	if len(approvals) != 1 || approvals[0].Status != types.ApprovalApproved {
		t.Fatalf("unexpected approvals: %#v", approvals)
	}
}

func TestServerHealthAndIndexerStatusEndpoints(t *testing.T) {
	exec := newAPIMockExecutor()
	server := NewServer(NewSSESink(), &apiMockMemory{}, exec)

	healthReq := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	healthRec := httptest.NewRecorder()
	server.handleHealth(healthRec, healthReq)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("health status = %d, body = %s", healthRec.Code, healthRec.Body.String())
	}
	if !strings.Contains(healthRec.Body.String(), `"ok":true`) {
		t.Fatalf("health response missing ok=true: %s", healthRec.Body.String())
	}
	if !strings.Contains(healthRec.Body.String(), `"capabilities"`) || !strings.Contains(healthRec.Body.String(), `"llama.cpp:Gemma-4-E4B-Q8_K_P.gguf"`) {
		t.Fatalf("health response missing capability status: %s", healthRec.Body.String())
	}

	indexReq := httptest.NewRequest(http.MethodGet, "/api/indexer/status", nil)
	indexRec := httptest.NewRecorder()
	server.handleIndexerStatus(indexRec, indexReq)
	if indexRec.Code != http.StatusOK {
		t.Fatalf("indexer status = %d, body = %s", indexRec.Code, indexRec.Body.String())
	}
	if !strings.Contains(indexRec.Body.String(), `"chunks_indexed":3`) {
		t.Fatalf("indexer response missing chunks_indexed: %s", indexRec.Body.String())
	}
}

func TestServerHealthFailsWhenCapabilityFailed(t *testing.T) {
	exec := newAPIMockExecutor()
	exec.caps = []afprocessor.CapabilityState{{
		Name:   "llama.cpp:Qwen3-Embedding-0.6B-f16.gguf",
		Status: afprocessor.CapabilityFailed,
		Error:  "model missing",
	}}
	server := NewServer(NewSSESink(), &apiMockMemory{}, exec)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	server.handleHealth(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"ok":false`) || !strings.Contains(rec.Body.String(), `"capability_error"`) {
		t.Fatalf("health response did not expose failed capability: %s", rec.Body.String())
	}
}

func TestServerToolPolicyEndpoint(t *testing.T) {
	exec := newAPIMockExecutor()
	server := NewServer(NewSSESink(), &apiMockMemory{}, exec)

	getReq := httptest.NewRequest(http.MethodGet, "/api/tool-policy", nil)
	getRec := httptest.NewRecorder()
	server.handleToolPolicy(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("tool policy get status = %d, body = %s", getRec.Code, getRec.Body.String())
	}
	if !strings.Contains(getRec.Body.String(), "safe_write_prefixes") {
		t.Fatalf("tool policy get missing prefixes: %s", getRec.Body.String())
	}

	putReq := httptest.NewRequest(http.MethodPut, "/api/tool-policy", strings.NewReader(`{"coderunner_approval_threshold_ms":500,"safe_write_prefixes":["scratch","docs/generated"]}`))
	putRec := httptest.NewRecorder()
	server.handleToolPolicy(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("tool policy put status = %d, body = %s", putRec.Code, putRec.Body.String())
	}
	if got := exec.GetToolPolicyConfig(); got.CodeRunnerApprovalThresholdMS != 500 || len(got.SafeWritePrefixes) != 2 {
		t.Fatalf("tool policy was not updated: %#v", got)
	}
}

func TestDashboardSmokeRoutes(t *testing.T) {
	t.Chdir(findModuleRoot(t))
	exec := newAPIMockExecutor()
	server := httptest.NewServer(NewServer(NewSSESink(), &apiMockMemory{}, exec).Handler())
	defer server.Close()

	checks := []struct {
		path string
		want string
	}{
		{path: "/", want: "Cognitive Orchestration"},
		{path: "/api/health", want: `"memory_present":true`},
		{path: "/api/health", want: `"capabilities"`},
		{path: "/api/indexer/status", want: `"chunks_indexed":3`},
		{path: "/api/tool-policy", want: "safe_write_prefixes"},
	}

	for _, check := range checks {
		t.Run(check.path, func(t *testing.T) {
			resp, err := http.Get(server.URL + check.path)
			if err != nil {
				t.Fatalf("GET %s: %v", check.path, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET %s status = %d", check.path, resp.StatusCode)
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read %s: %v", check.path, err)
			}
			if !strings.Contains(string(body), check.want) {
				t.Fatalf("GET %s missing %q in %s", check.path, check.want, string(body))
			}
		})
	}
}

func findModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "web", "index.html")); err == nil {
				return dir
			}
		}
		next := filepath.Dir(dir)
		if next == dir {
			t.Fatal("could not find module root with web/index.html")
		}
		dir = next
	}
}
