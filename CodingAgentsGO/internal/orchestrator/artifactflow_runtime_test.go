package orchestrator

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	afstate "github.com/ArtifactFlow/ArtifactFlow/pkg/state"
	"github.com/user/cognitive-orchestrator/internal/artifactflowbridge"
	"github.com/user/cognitive-orchestrator/internal/memory"
	"github.com/user/cognitive-orchestrator/internal/tool"
	"github.com/user/cognitive-orchestrator/internal/types"
)

func TestArtifactFlowRuntimePersistsRunMessagesAndArtifacts(t *testing.T) {
	board, err := afstate.NewBlackboard(filepath.Join(t.TempDir(), "artifactflow.db"))
	if err != nil {
		t.Fatalf("NewBlackboard failed: %v", err)
	}
	defer board.Close()

	store := memory.NewArtifactFlowStore(board)
	bc := &captureBroadcaster{}
	coord := NewCoordinator(store, tool.NewRegistry(), bc, nil)
	coord.SetArtifactFlow(board)
	coord.AddAgent(&mockAgent{
		role: types.RolePlanner,
		resp: "[TECHNICAL PLAN]\n1. Use ArtifactFlow.\n[/TECHNICAL PLAN]\n[NEXT: completed]",
	})

	messages, err := coord.Execute(context.Background(), "plan this", types.RolePlanner, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if len(messages) == 0 {
		t.Fatal("expected messages")
	}
	if bc.state == nil {
		t.Fatal("session_complete event was not broadcast")
	}

	persistedMessages, err := board.GetMessages(bc.state.SessionID)
	if err != nil {
		t.Fatalf("GetMessages failed: %v", err)
	}
	if len(persistedMessages) < 2 {
		t.Fatalf("expected persisted user and planner messages, got %#v", persistedMessages)
	}

	artifacts, err := board.GetRunArtifacts(bc.state.SessionID)
	if err != nil {
		t.Fatalf("GetRunArtifacts failed: %v", err)
	}
	foundPlan := false
	for _, art := range artifacts {
		if art.Metadata["name"] == "technical_plan" {
			foundPlan = true
			break
		}
	}
	if !foundPlan {
		t.Fatalf("expected technical_plan artifact, got %#v", artifacts)
	}
}

func TestArtifactFlowRuntimeExecuteRunUsesProvidedRunID(t *testing.T) {
	board, err := afstate.NewBlackboard(filepath.Join(t.TempDir(), "artifactflow.db"))
	if err != nil {
		t.Fatalf("NewBlackboard failed: %v", err)
	}
	defer board.Close()

	store := memory.NewArtifactFlowStore(board)
	coord := NewCoordinator(store, tool.NewRegistry(), nil, nil)
	coord.SetArtifactFlow(board)
	coord.AddAgent(&mockAgent{
		role: types.RolePlanner,
		resp: "[TECHNICAL PLAN]\n1. Stable run IDs.\n[/TECHNICAL PLAN]\n[NEXT: completed]",
	})

	const runID = "run-api-1"
	if _, err := coord.ExecuteRun(context.Background(), runID, "plan this", types.RolePlanner, nil); err != nil {
		t.Fatalf("ExecuteRun failed: %v", err)
	}

	run, err := coord.GetRun(runID)
	if err != nil {
		t.Fatalf("GetRun failed: %v", err)
	}
	if run.RunID != runID || run.Status != "completed" {
		t.Fatalf("unexpected run: %#v", run)
	}

	timeline, err := coord.GetRunTimeline(runID)
	if err != nil {
		t.Fatalf("GetRunTimeline failed: %v", err)
	}
	if len(timeline) == 0 {
		t.Fatal("expected timeline entries")
	}

	snapshot, err := coord.ReconstructRun(runID)
	if err != nil {
		t.Fatalf("ReconstructRun failed: %v", err)
	}
	if snapshot.Run.RunID != runID || len(snapshot.Messages) == 0 || len(snapshot.Artifacts) == 0 {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
}

func TestArtifactFlowRuntimeIncludesPromptForTargetedCoder(t *testing.T) {
	board, err := afstate.NewBlackboard(filepath.Join(t.TempDir(), "artifactflow.db"))
	if err != nil {
		t.Fatalf("NewBlackboard failed: %v", err)
	}
	defer board.Close()

	var gotInstructions string
	store := memory.NewArtifactFlowStore(board)
	coord := NewCoordinator(store, tool.NewRegistry(), nil, nil)
	coord.SetArtifactFlow(board)
	coord.AddAgent(&instructionCaptureAgent{
		role: types.RoleCoder,
		respond: func(instructions string) string {
			gotInstructions = instructions
			return "[NEXT: completed]"
		},
	})

	const prompt = "create logs/dogfood_writer_smoke.md only"
	if _, err := coord.ExecuteRun(context.Background(), "run-targeted-coder", prompt, types.RoleCoder, nil); err != nil {
		t.Fatalf("ExecuteRun failed: %v", err)
	}
	if !strings.Contains(gotInstructions, "TASK: "+prompt) {
		t.Fatalf("instructions did not include prompt:\n%s", gotInstructions)
	}
}

func TestRoleGraphPresetsAreValidDAGs(t *testing.T) {
	presets := []string{
		"default",
		"planner-only",
		"code-review",
		"implementation",
		"security-review",
		"artifactflow-platform",
	}
	for _, preset := range presets {
		t.Run(preset, func(t *testing.T) {
			graph := RoleGraphPreset(preset)
			if graph == nil {
				t.Fatalf("preset %q returned nil", preset)
			}
			if err := artifactflowbridge.ValidateRoleGraph(graph); err != nil {
				t.Fatalf("preset %q is invalid: %v", preset, err)
			}
		})
	}
}

type instructionCaptureAgent struct {
	role    types.Role
	respond func(instructions string) string
}

func (a *instructionCaptureAgent) Respond(ctx context.Context, state *types.CouncilState, instructions string) (types.AgentResponse, error) {
	return types.AgentResponse{Content: a.respond(instructions)}, nil
}

func (a *instructionCaptureAgent) GetRole() types.Role { return a.role }
func (a *instructionCaptureAgent) GetConfig() types.AgentConfig {
	return types.AgentConfig{Role: a.role}
}
func (a *instructionCaptureAgent) UpdateConfig(types.AgentConfig) {}

func TestApprovalEventsRoundTripThroughArtifactFlow(t *testing.T) {
	board, err := afstate.NewBlackboard(filepath.Join(t.TempDir(), "artifactflow.db"))
	if err != nil {
		t.Fatalf("NewBlackboard failed: %v", err)
	}
	defer board.Close()

	coord := NewCoordinator(memory.NewArtifactFlowStore(board), tool.NewRegistry(), nil, nil)
	coord.SetArtifactFlow(board)
	if err := board.StartWorkflowRun("run-approval-1", "codingagents-agentic-runtime"); err != nil {
		t.Fatalf("StartWorkflowRun failed: %v", err)
	}
	if err := board.SaveEvent(afstate.Event{
		ID:    "approval-evt-1",
		RunID: "run-approval-1",
		Type:  string(types.EventApprovalRequested),
		Payload: map[string]any{
			"approval_id": "approval-1",
			"role":        string(types.RoleCoder),
			"tool":        "writer",
			"args":        `{"action":"write","path":"README.md"}`,
			"reason":      "writer path is outside scratch or shadow workspace",
			"status":      string(types.ApprovalPending),
		},
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("SaveEvent failed: %v", err)
	}

	approvals, err := coord.GetRunApprovals("run-approval-1")
	if err != nil {
		t.Fatalf("GetRunApprovals failed: %v", err)
	}
	if len(approvals) != 1 || approvals[0].Status != types.ApprovalPending {
		t.Fatalf("unexpected approvals: %#v", approvals)
	}

	if err := coord.DecideApproval("run-approval-1", "approval-1", "approved", "looks scoped"); err != nil {
		t.Fatalf("DecideApproval failed: %v", err)
	}
	approvals, err = coord.GetRunApprovals("run-approval-1")
	if err != nil {
		t.Fatalf("GetRunApprovals after decision failed: %v", err)
	}
	if len(approvals) != 1 || approvals[0].Status != types.ApprovalApproved || approvals[0].DecisionReason != "looks scoped" {
		t.Fatalf("unexpected decided approvals: %#v", approvals)
	}

	timeline, err := coord.GetRunTimeline("run-approval-1")
	if err != nil {
		t.Fatalf("GetRunTimeline failed: %v", err)
	}
	foundDecision := false
	for _, entry := range timeline {
		if entry.Kind == "event" && entry.Type == string(types.EventApprovalDecided) {
			foundDecision = true
			break
		}
	}
	if !foundDecision {
		t.Fatalf("approval decision event missing from timeline: %#v", timeline)
	}
}
