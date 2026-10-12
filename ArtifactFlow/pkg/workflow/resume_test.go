package workflow

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArtifactFlow/ArtifactFlow/pkg/artifact"
	"github.com/ArtifactFlow/ArtifactFlow/pkg/processor"
	"github.com/ArtifactFlow/ArtifactFlow/pkg/state"
)

type testPauseError struct{}

func (e testPauseError) Error() string { return "paused for test" }
func (e testPauseError) Paused() bool  { return true }

type resumableProcessor struct {
	calls int
}

func (p *resumableProcessor) Name() string { return "test.resumable" }

func (p *resumableProcessor) Process(ctx context.Context, config map[string]any, inputs []artifact.Artifact) ([]artifact.Artifact, error) {
	p.calls++
	if p.calls == 1 {
		return nil, testPauseError{}
	}
	return []artifact.Artifact{{
		ID:   "control",
		Kind: artifact.KindAgentEvent,
		Metadata: map[string]any{
			"next_node": "completed",
		},
	}}, nil
}

func TestResumeInteractiveContinuesPausedRun(t *testing.T) {
	board, err := state.NewBlackboard(filepath.Join(t.TempDir(), "artifactflow.db"))
	if err != nil {
		t.Fatalf("NewBlackboard failed: %v", err)
	}
	defer board.Close()

	registry := processor.NewRegistry()
	proc := &resumableProcessor{}
	registry.Register(proc)
	engine := NewEngine(registry, board)
	wf := &Workflow{Name: "resume-test", Nodes: []Node{{ID: "planner", Processor: proc.Name()}}}

	if err := engine.RunInteractive(context.Background(), wf, "run-1", "planner"); !isPaused(err) {
		t.Fatalf("expected pause error, got %v", err)
	}
	run, err := board.GetWorkflowRun("run-1")
	if err != nil {
		t.Fatalf("GetWorkflowRun failed: %v", err)
	}
	if run.Status != "paused" {
		t.Fatalf("expected paused run, got %#v", run)
	}

	if err := engine.ResumeInteractive(context.Background(), wf, "run-1", ""); err != nil {
		t.Fatalf("ResumeInteractive failed: %v", err)
	}
	run, err = board.GetWorkflowRun("run-1")
	if err != nil {
		t.Fatalf("GetWorkflowRun after resume failed: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("expected completed run, got %#v", run)
	}
	if proc.calls != 2 {
		t.Fatalf("expected processor to run twice, got %d", proc.calls)
	}
}

func TestResumeInteractiveUsesPersistedCursorWithFreshEngine(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "artifactflow.db")
	board, err := state.NewBlackboard(dbPath)
	if err != nil {
		t.Fatalf("NewBlackboard failed: %v", err)
	}

	registry := processor.NewRegistry()
	proc := &resumableProcessor{}
	registry.Register(proc)
	engine := NewEngine(registry, board)
	wf := &Workflow{Name: "resume-test", Nodes: []Node{{ID: "planner", Processor: proc.Name()}}}

	if err := engine.RunInteractive(context.Background(), wf, "run-1", "planner"); !isPaused(err) {
		t.Fatalf("expected pause error, got %v", err)
	}
	cursor, err := board.LoadWorkflowCursor("run-1")
	if err != nil {
		t.Fatalf("LoadWorkflowCursor failed: %v", err)
	}
	if cursor.CurrentNode != "planner" || cursor.WorkflowName != "resume-test" || len(cursor.WorkflowJSON) == 0 {
		t.Fatalf("unexpected cursor: %#v", cursor)
	}
	board.Close()

	reopened, err := state.NewBlackboard(dbPath)
	if err != nil {
		t.Fatalf("reopen NewBlackboard failed: %v", err)
	}
	defer reopened.Close()
	freshRegistry := processor.NewRegistry()
	freshProc := &resumableProcessor{calls: 1}
	freshRegistry.Register(freshProc)
	freshEngine := NewEngine(freshRegistry, reopened)

	if err := freshEngine.ResumeInteractive(context.Background(), nil, "run-1", ""); err != nil {
		t.Fatalf("ResumeInteractive from persisted cursor failed: %v", err)
	}
	run, err := reopened.GetWorkflowRun("run-1")
	if err != nil {
		t.Fatalf("GetWorkflowRun failed: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("expected completed run, got %#v", run)
	}
	cursor, err = reopened.LoadWorkflowCursor("run-1")
	if err != nil {
		t.Fatalf("LoadWorkflowCursor after resume failed: %v", err)
	}
	if cursor.TerminalState != "completed" {
		t.Fatalf("expected completed cursor, got %#v", cursor)
	}
}

func TestResumeInteractiveRejectsNonPausedRun(t *testing.T) {
	board, err := state.NewBlackboard(filepath.Join(t.TempDir(), "artifactflow.db"))
	if err != nil {
		t.Fatalf("NewBlackboard failed: %v", err)
	}
	defer board.Close()

	registry := processor.NewRegistry()
	proc := &resumableProcessor{calls: 1}
	registry.Register(proc)
	engine := NewEngine(registry, board)
	wf := &Workflow{Name: "resume-test", Nodes: []Node{{ID: "planner", Processor: proc.Name()}}}

	if err := engine.RunInteractive(context.Background(), wf, "run-1", "planner"); err != nil {
		t.Fatalf("RunInteractive failed: %v", err)
	}
	if err := engine.ResumeInteractive(context.Background(), wf, "run-1", "planner"); err == nil || !strings.Contains(err.Error(), "not paused") {
		t.Fatalf("expected non-paused resume to fail, got %v", err)
	}
}

func TestResumeInteractiveRejectsMissingCursor(t *testing.T) {
	board, err := state.NewBlackboard(filepath.Join(t.TempDir(), "artifactflow.db"))
	if err != nil {
		t.Fatalf("NewBlackboard failed: %v", err)
	}
	defer board.Close()

	registry := processor.NewRegistry()
	proc := &resumableProcessor{calls: 1}
	registry.Register(proc)
	engine := NewEngine(registry, board)

	if err := board.StartWorkflowRun("run-1", "resume-test"); err != nil {
		t.Fatalf("StartWorkflowRun failed: %v", err)
	}
	if err := board.RecordNodeStart("run-1", "planner"); err != nil {
		t.Fatalf("RecordNodeStart failed: %v", err)
	}
	if err := board.RecordNodeCompletion("run-1", "planner", "paused", "waiting"); err != nil {
		t.Fatalf("RecordNodeCompletion failed: %v", err)
	}
	if err := board.UpdateWorkflowRun("run-1", "paused"); err != nil {
		t.Fatalf("UpdateWorkflowRun failed: %v", err)
	}

	if err := engine.ResumeInteractive(context.Background(), nil, "run-1", ""); err == nil || !strings.Contains(err.Error(), "workflow cursor not found") {
		t.Fatalf("expected missing cursor error, got %v", err)
	}
}
