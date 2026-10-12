package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	afstate "github.com/ArtifactFlow/ArtifactFlow/pkg/state"
	"github.com/user/cognitive-orchestrator/internal/memory"
	"github.com/user/cognitive-orchestrator/internal/tool"
	"github.com/user/cognitive-orchestrator/internal/types"
)

type recordingTool struct {
	name  string
	calls int
	args  []string
}

func (t *recordingTool) Name() string        { return t.name }
func (t *recordingTool) Description() string { return "test tool" }
func (t *recordingTool) Execute(ctx context.Context, args string) (string, error) {
	t.calls++
	t.args = append(t.args, args)
	return "approved tool executed", nil
}

func newApprovalTestRegistry(writer tool.Tool) *tool.Registry {
	registry := tool.NewRegistry()
	registry.Register(writer)
	return registry
}

func TestApprovedWriterResumesPausedRun(t *testing.T) {
	board, err := afstate.NewBlackboard(filepath.Join(t.TempDir(), "artifactflow.db"))
	if err != nil {
		t.Fatalf("NewBlackboard failed: %v", err)
	}
	defer board.Close()

	writer := &recordingTool{name: "writer"}
	tools := newApprovalTestRegistry(writer)
	coord := NewCoordinator(memory.NewArtifactFlowStore(board), tools, nil, nil)
	coord.SetArtifactFlow(board)
	turn := 0
	coord.AddAgent(&functionalMockAgent{
		role: types.RoleCoder,
		respond: func(ctx context.Context, state *types.CouncilState) string {
			turn++
			if turn == 1 {
				return `[TOOL: writer, ARGS: {"action":"write","path":"README.md","content":"x"}]`
			}
			if !hasObservation(state, "writer") {
				t.Fatalf("resumed agent did not receive writer observation")
			}
			return "[NEXT: completed]"
		},
	})

	const runID = "run-approval-resume"
	if _, err := coord.ExecuteRun(context.Background(), runID, "update README.md", types.RoleCoder, nil); err != nil {
		t.Fatalf("ExecuteRun pause path failed: %v", err)
	}
	run, err := coord.GetRun(runID)
	if err != nil {
		t.Fatalf("GetRun failed: %v", err)
	}
	if run.Status != "paused" {
		t.Fatalf("expected paused run, got %#v", run)
	}
	approvals, err := coord.GetRunApprovals(runID)
	if err != nil {
		t.Fatalf("GetRunApprovals failed: %v", err)
	}
	if len(approvals) != 1 {
		t.Fatalf("expected one approval, got %#v", approvals)
	}

	if err := coord.DecideApproval(runID, approvals[0].ID, "approved", "scoped"); err != nil {
		t.Fatalf("DecideApproval approve failed: %v", err)
	}
	if writer.calls != 1 {
		t.Fatalf("expected writer to execute once, got %d", writer.calls)
	}
	run, err = coord.GetRun(runID)
	if err != nil {
		t.Fatalf("GetRun after resume failed: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("expected completed run, got %#v", run)
	}
	approvals, err = coord.GetRunApprovals(runID)
	if err != nil {
		t.Fatalf("GetRunApprovals after resume failed: %v", err)
	}
	if approvals[0].Status != types.ApprovalApproved {
		t.Fatalf("expected approved status, got %#v", approvals[0])
	}
}

func TestDeniedApprovalDoesNotExecuteTool(t *testing.T) {
	board, err := afstate.NewBlackboard(filepath.Join(t.TempDir(), "artifactflow.db"))
	if err != nil {
		t.Fatalf("NewBlackboard failed: %v", err)
	}
	defer board.Close()

	writer := &recordingTool{name: "writer"}
	coord := NewCoordinator(memory.NewArtifactFlowStore(board), newApprovalTestRegistry(writer), nil, nil)
	coord.SetArtifactFlow(board)
	coord.AddAgent(&mockAgent{
		role: types.RoleCoder,
		resp: `[TOOL: writer, ARGS: {"action":"write","path":"README.md","content":"x"}]`,
	})

	const runID = "run-approval-deny"
	if _, err := coord.ExecuteRun(context.Background(), runID, "update README.md", types.RoleCoder, nil); err != nil {
		t.Fatalf("ExecuteRun pause path failed: %v", err)
	}
	approvals, err := coord.GetRunApprovals(runID)
	if err != nil {
		t.Fatalf("GetRunApprovals failed: %v", err)
	}
	if err := coord.DecideApproval(runID, approvals[0].ID, "denied", "too broad"); err != nil {
		t.Fatalf("DecideApproval deny failed: %v", err)
	}
	if writer.calls != 0 {
		t.Fatalf("denied approval executed writer %d times", writer.calls)
	}
	run, err := coord.GetRun(runID)
	if err != nil {
		t.Fatalf("GetRun after deny failed: %v", err)
	}
	if run.Status != "denied" {
		t.Fatalf("expected denied run, got %#v", run)
	}
	if err := coord.DecideApproval(runID, approvals[0].ID, "approved", "late"); err != nil {
		t.Fatalf("duplicate decision should be idempotent, got %v", err)
	}
	if writer.calls != 0 {
		t.Fatalf("duplicate decision executed writer %d times", writer.calls)
	}
}

func TestApprovedWriterResumesAfterCoordinatorRestart(t *testing.T) {
	board, err := afstate.NewBlackboard(filepath.Join(t.TempDir(), "artifactflow.db"))
	if err != nil {
		t.Fatalf("NewBlackboard failed: %v", err)
	}
	defer board.Close()

	firstWriter := &recordingTool{name: "writer"}
	first := NewCoordinator(memory.NewArtifactFlowStore(board), newApprovalTestRegistry(firstWriter), nil, nil)
	first.SetArtifactFlow(board)
	first.AddAgent(&mockAgent{
		role: types.RoleCoder,
		resp: `[TOOL: writer, ARGS: {"action":"write","path":"README.md","content":"x"}]`,
	})

	const runID = "run-restart-resume"
	if _, err := first.ExecuteRun(context.Background(), runID, "update README.md", types.RoleCoder, nil); err != nil {
		t.Fatalf("ExecuteRun pause path failed: %v", err)
	}
	approvals, err := first.GetRunApprovals(runID)
	if err != nil {
		t.Fatalf("GetRunApprovals failed: %v", err)
	}
	if len(approvals) != 1 || approvals[0].ResumeMode != "live-resumable" {
		t.Fatalf("expected live resumable approval, got %#v", approvals)
	}

	restartedWriter := &recordingTool{name: "writer"}
	restarted := NewCoordinator(memory.NewArtifactFlowStore(board), newApprovalTestRegistry(restartedWriter), nil, nil)
	restarted.SetArtifactFlow(board)
	turn := 0
	restarted.AddAgent(&functionalMockAgent{
		role: types.RoleCoder,
		respond: func(ctx context.Context, state *types.CouncilState) string {
			turn++
			if !hasObservation(state, "writer") {
				t.Fatalf("reconstructed state did not include approved writer observation")
			}
			return "[NEXT: completed]"
		},
	})

	approvals, err = restarted.GetRunApprovals(runID)
	if err != nil {
		t.Fatalf("GetRunApprovals after restart failed: %v", err)
	}
	if len(approvals) != 1 || approvals[0].ResumeMode != "reconstructable" {
		t.Fatalf("expected reconstructable approval, got %#v", approvals)
	}
	if err := restarted.DecideApproval(runID, approvals[0].ID, "approved", "scoped"); err != nil {
		t.Fatalf("DecideApproval after restart failed: %v", err)
	}
	if restartedWriter.calls != 1 {
		t.Fatalf("expected restarted writer to execute once, got %d", restartedWriter.calls)
	}
	if turn != 1 {
		t.Fatalf("expected resumed agent to run once, got %d", turn)
	}
	run, err := restarted.GetRun(runID)
	if err != nil {
		t.Fatalf("GetRun after restart resume failed: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("expected completed run, got %#v", run)
	}
}

func TestDeniedApprovalAfterRestartDoesNotExecuteTool(t *testing.T) {
	board, err := afstate.NewBlackboard(filepath.Join(t.TempDir(), "artifactflow.db"))
	if err != nil {
		t.Fatalf("NewBlackboard failed: %v", err)
	}
	defer board.Close()

	writer := &recordingTool{name: "writer"}
	first := NewCoordinator(memory.NewArtifactFlowStore(board), newApprovalTestRegistry(writer), nil, nil)
	first.SetArtifactFlow(board)
	first.AddAgent(&mockAgent{
		role: types.RoleCoder,
		resp: `[TOOL: writer, ARGS: {"action":"write","path":"README.md","content":"x"}]`,
	})

	const runID = "run-restart-deny"
	if _, err := first.ExecuteRun(context.Background(), runID, "update README.md", types.RoleCoder, nil); err != nil {
		t.Fatalf("ExecuteRun pause path failed: %v", err)
	}
	approvals, err := first.GetRunApprovals(runID)
	if err != nil {
		t.Fatalf("GetRunApprovals failed: %v", err)
	}

	restartedWriter := &recordingTool{name: "writer"}
	restarted := NewCoordinator(memory.NewArtifactFlowStore(board), newApprovalTestRegistry(restartedWriter), nil, nil)
	restarted.SetArtifactFlow(board)
	if err := restarted.DecideApproval(runID, approvals[0].ID, "denied", "too broad"); err != nil {
		t.Fatalf("DecideApproval deny after restart failed: %v", err)
	}
	if restartedWriter.calls != 0 {
		t.Fatalf("denied approval after restart executed writer %d times", restartedWriter.calls)
	}
	approvals, err = restarted.GetRunApprovals(runID)
	if err != nil {
		t.Fatalf("GetRunApprovals after deny failed: %v", err)
	}
	if approvals[0].Status != types.ApprovalDenied {
		t.Fatalf("expected denied status, got %#v", approvals[0])
	}
}

func TestApprovalResumeMissingMetadataRecordsFailure(t *testing.T) {
	board, err := afstate.NewBlackboard(filepath.Join(t.TempDir(), "artifactflow.db"))
	if err != nil {
		t.Fatalf("NewBlackboard failed: %v", err)
	}
	defer board.Close()

	coord := NewCoordinator(memory.NewArtifactFlowStore(board), newApprovalTestRegistry(&recordingTool{name: "writer"}), nil, nil)
	coord.SetArtifactFlow(board)
	const runID = "run-missing-metadata"
	const approvalID = "approval-missing-meta"
	if err := board.StartWorkflowRun(runID, "codingagents-agentic-runtime"); err != nil {
		t.Fatalf("StartWorkflowRun failed: %v", err)
	}
	if err := board.RecordNodeStart(runID, "coder"); err != nil {
		t.Fatalf("RecordNodeStart failed: %v", err)
	}
	if err := board.RecordNodeCompletion(runID, "coder", "paused", "approval"); err != nil {
		t.Fatalf("RecordNodeCompletion failed: %v", err)
	}
	if err := board.UpdateWorkflowRun(runID, "paused"); err != nil {
		t.Fatalf("UpdateWorkflowRun failed: %v", err)
	}
	if err := board.SaveWorkflowCursor(afstate.WorkflowCursor{
		RunID:        runID,
		WorkflowName: "codingagents-agentic-runtime",
		StartNode:    "coder",
		CurrentNode:  "coder",
		WorkflowJSON: []byte(`{"Name":"codingagents-agentic-runtime","Nodes":[{"ID":"coder","Processor":"codingagents.role"}]}`),
	}); err != nil {
		t.Fatalf("SaveWorkflowCursor failed: %v", err)
	}
	saveApprovalEvent(t, board, runID, approvalID)

	if err := coord.DecideApproval(runID, approvalID, "approved", "scoped"); err == nil || !strings.Contains(err.Error(), "run metadata unavailable") {
		t.Fatalf("expected metadata error, got %v", err)
	}
	run, err := coord.GetRun(runID)
	if err != nil {
		t.Fatalf("GetRun failed: %v", err)
	}
	if run.Status != "resume_failed" {
		t.Fatalf("expected resume_failed run, got %#v", run)
	}
}

func TestShadowApprovalAfterRestartFailsWhenShadowWorkspaceMissing(t *testing.T) {
	board, err := afstate.NewBlackboard(filepath.Join(t.TempDir(), "artifactflow.db"))
	if err != nil {
		t.Fatalf("NewBlackboard failed: %v", err)
	}
	defer board.Close()

	writer := &recordingTool{name: "writer"}
	first := NewCoordinator(memory.NewArtifactFlowStore(board), newApprovalTestRegistry(writer), nil, nil)
	first.SetArtifactFlow(board)
	first.AddAgent(&mockAgent{
		role: types.RoleCoder,
		resp: `[TOOL: writer, ARGS: {"action":"write","path":"README.md","content":"x"}]`,
	})
	const runID = "run-shadow-missing"
	if _, err := first.ExecuteRun(context.Background(), runID, "@shadow update README.md", types.RoleCoder, nil); err != nil {
		t.Fatalf("ExecuteRun pause path failed: %v", err)
	}
	var meta codingAgentsRunMetadata
	if err := board.LoadConfig("codingagents_run", runID, &meta); err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if meta.ShadowPath == "" {
		t.Fatal("expected persisted shadow path")
	}
	if err := os.RemoveAll(meta.ShadowPath); err != nil {
		t.Fatalf("RemoveAll shadow path failed: %v", err)
	}

	restarted := NewCoordinator(memory.NewArtifactFlowStore(board), newApprovalTestRegistry(&recordingTool{name: "writer"}), nil, nil)
	restarted.SetArtifactFlow(board)
	restarted.AddAgent(&mockAgent{role: types.RoleCoder, resp: "[NEXT: completed]"})
	approvals, err := restarted.GetRunApprovals(runID)
	if err != nil {
		t.Fatalf("GetRunApprovals failed: %v", err)
	}
	if err := restarted.DecideApproval(runID, approvals[0].ID, "approved", "scoped"); err == nil || !strings.Contains(err.Error(), "shadow workspace") {
		t.Fatalf("expected shadow workspace error, got %v", err)
	}
}

func saveApprovalEvent(t *testing.T, board *afstate.Blackboard, runID, approvalID string) {
	t.Helper()
	if err := board.SaveEvent(afstate.Event{
		ID:    approvalID + "-requested",
		RunID: runID,
		Type:  string(types.EventApprovalRequested),
		Payload: map[string]any{
			"approval_id": approvalID,
			"role":        string(types.RoleCoder),
			"tool":        "writer",
			"args":        `{"action":"write","path":"README.md","content":"x"}`,
			"reason":      "writer path is outside scratch or shadow workspace",
			"status":      string(types.ApprovalPending),
		},
	}); err != nil {
		t.Fatalf("SaveEvent approval failed: %v", err)
	}
}

func hasObservation(state *types.CouncilState, toolName string) bool {
	state.RLock()
	defer state.RUnlock()
	needle := "[OBSERVATION from " + toolName + "]"
	for _, msg := range state.Messages {
		if strings.Contains(msg.Content, needle) {
			return true
		}
	}
	return false
}
