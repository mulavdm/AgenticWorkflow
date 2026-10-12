package workflow

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/ArtifactFlow/ArtifactFlow/pkg/artifact"
	"github.com/ArtifactFlow/ArtifactFlow/pkg/processor"
	"github.com/ArtifactFlow/ArtifactFlow/pkg/state"
)

type nextProcessor struct {
	name string
	next string
}

func (p nextProcessor) Name() string { return p.name }

func (p nextProcessor) Process(ctx context.Context, config map[string]any, inputs []artifact.Artifact) ([]artifact.Artifact, error) {
	next := p.next
	if configured, ok := config["next_node"].(string); ok {
		next = configured
	}
	return []artifact.Artifact{{
		ID:   fmt.Sprintf("%s-output", p.name),
		Kind: artifact.KindAgentEvent,
		Metadata: map[string]any{
			"next_node": next,
		},
	}}, nil
}

type cancelProcessor struct{}

func (p cancelProcessor) Name() string { return "role.cancel" }

func (p cancelProcessor) Process(ctx context.Context, config map[string]any, inputs []artifact.Artifact) ([]artifact.Artifact, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestRunInteractiveFollowsValidDynamicHandoff(t *testing.T) {
	reg := processor.NewRegistry()
	reg.Register(nextProcessor{name: "role.context", next: "planner"})
	reg.Register(nextProcessor{name: "role.planner", next: "completed"})

	board, err := state.NewBlackboard(filepath.Join(t.TempDir(), "blackboard.db"))
	if err != nil {
		t.Fatalf("NewBlackboard failed: %v", err)
	}
	defer board.Close()

	engine := NewEngine(reg, board)
	wf := &Workflow{
		Name: "agentic",
		Nodes: []Node{
			{ID: "context", Processor: "role.context"},
			{ID: "planner", Processor: "role.planner", DependsOn: []string{"context"}},
		},
	}

	if err := engine.RunInteractive(context.Background(), wf, "run-1", "context"); err != nil {
		t.Fatalf("RunInteractive failed: %v", err)
	}
	run, err := board.GetWorkflowRun("run-1")
	if err != nil {
		t.Fatalf("GetWorkflowRun failed: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("unexpected run status: got %q", run.Status)
	}
}

func TestRunInteractiveRejectsInvalidDynamicHandoff(t *testing.T) {
	reg := processor.NewRegistry()
	reg.Register(nextProcessor{name: "role.context", next: "synthesizer"})

	board, err := state.NewBlackboard(filepath.Join(t.TempDir(), "blackboard.db"))
	if err != nil {
		t.Fatalf("NewBlackboard failed: %v", err)
	}
	defer board.Close()

	engine := NewEngine(reg, board)
	wf := &Workflow{
		Name: "agentic",
		Nodes: []Node{
			{ID: "context", Processor: "role.context"},
			{ID: "planner", Processor: "role.context", DependsOn: []string{"context"}},
		},
	}

	if err := engine.RunInteractive(context.Background(), wf, "run-1", "context"); err == nil {
		t.Fatal("expected invalid handoff to fail")
	}
}

func TestRunInteractiveRecordsCancellation(t *testing.T) {
	reg := processor.NewRegistry()
	reg.Register(cancelProcessor{})

	board, err := state.NewBlackboard(filepath.Join(t.TempDir(), "blackboard.db"))
	if err != nil {
		t.Fatalf("NewBlackboard failed: %v", err)
	}
	defer board.Close()

	engine := NewEngine(reg, board)
	wf := &Workflow{
		Name: "agentic",
		Nodes: []Node{
			{ID: "context", Processor: "role.cancel"},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = engine.RunInteractive(ctx, wf, "run-1", "context")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	run, err := board.GetWorkflowRun("run-1")
	if err != nil {
		t.Fatalf("GetWorkflowRun failed: %v", err)
	}
	if run.Status != "cancelled" {
		t.Fatalf("expected cancelled run, got %q", run.Status)
	}
	nodes, err := board.GetNodeRuns("run-1")
	if err != nil {
		t.Fatalf("GetNodeRuns failed: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Status != "cancelled" {
		t.Fatalf("expected cancelled node, got %#v", nodes)
	}
}
