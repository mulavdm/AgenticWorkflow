package state

import (
	"path/filepath"
	"testing"

	"github.com/ArtifactFlow/ArtifactFlow/pkg/artifact"
)

func TestBlackboardSaveArtifactIsImmutableOnIDConflict(t *testing.T) {
	b, err := NewBlackboard(filepath.Join(t.TempDir(), "blackboard.db"))
	if err != nil {
		t.Fatalf("NewBlackboard failed: %v", err)
	}
	defer b.Close()

	first := artifact.Artifact{
		ID:       "artifact-1",
		Kind:     artifact.KindText,
		URI:      "first.txt",
		Metadata: map[string]any{"version": "first"},
	}
	second := artifact.Artifact{
		ID:       "artifact-1",
		Kind:     artifact.KindText,
		URI:      "second.txt",
		Metadata: map[string]any{"version": "second"},
	}

	if err := b.SaveArtifact("run-1", "node-1", first); err != nil {
		t.Fatalf("SaveArtifact first failed: %v", err)
	}
	if err := b.SaveArtifact("run-1", "node-1", second); err != nil {
		t.Fatalf("SaveArtifact second failed: %v", err)
	}

	got, err := b.GetArtifact("artifact-1")
	if err != nil {
		t.Fatalf("GetArtifact failed: %v", err)
	}
	if got.URI != "first.txt" {
		t.Fatalf("artifact was mutated on conflict: got URI %q", got.URI)
	}
	if got.Metadata["version"] != "first" {
		t.Fatalf("artifact metadata was mutated: got %#v", got.Metadata)
	}

	artifacts, err := b.GetRunArtifacts("run-1")
	if err != nil {
		t.Fatalf("GetRunArtifacts failed: %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].ID != "artifact-1" {
		t.Fatalf("unexpected run artifacts: %#v", artifacts)
	}
}

func TestBlackboardPersistsAgentState(t *testing.T) {
	b, err := NewBlackboard(filepath.Join(t.TempDir(), "blackboard.db"))
	if err != nil {
		t.Fatalf("NewBlackboard failed: %v", err)
	}
	defer b.Close()

	if err := b.StartWorkflowRun("run-1", "agentic"); err != nil {
		t.Fatalf("StartWorkflowRun failed: %v", err)
	}
	if err := b.SaveMessage(Message{ID: "msg-1", RunID: "run-1", NodeID: "planner", Role: "planner", Content: "plan", Visible: true}); err != nil {
		t.Fatalf("SaveMessage failed: %v", err)
	}
	if err := b.SaveEvent(Event{ID: "evt-1", RunID: "run-1", NodeID: "planner", Type: "phase_started", Payload: map[string]any{"role": "planner"}}); err != nil {
		t.Fatalf("SaveEvent failed: %v", err)
	}
	if err := b.SaveLesson(Lesson{Task: "task", Critique: "critique", Resolution: "resolution", Tags: "go"}); err != nil {
		t.Fatalf("SaveLesson failed: %v", err)
	}
	if err := b.SaveVector(VectorEntry{ID: "vec-1", RunID: "run-1", TargetID: "msg-1", Vector: []float32{1, 2}, Metadata: map[string]string{"role": "planner"}}); err != nil {
		t.Fatalf("SaveVector failed: %v", err)
	}
	if err := b.SaveConfig("codingagents", "global", map[string]any{"mode": "local"}); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	messages, err := b.GetMessages("run-1")
	if err != nil {
		t.Fatalf("GetMessages failed: %v", err)
	}
	if len(messages) != 1 || messages[0].Content != "plan" {
		t.Fatalf("unexpected messages: %#v", messages)
	}

	events, err := b.GetEvents("run-1")
	if err != nil {
		t.Fatalf("GetEvents failed: %v", err)
	}
	if len(events) != 1 || events[0].Type != "phase_started" {
		t.Fatalf("unexpected events: %#v", events)
	}

	lessons, err := b.SearchLessons("task", 5)
	if err != nil {
		t.Fatalf("SearchLessons failed: %v", err)
	}
	if len(lessons) != 1 || lessons[0].Resolution != "resolution" {
		t.Fatalf("unexpected lessons: %#v", lessons)
	}

	vectors, err := b.GetVectors()
	if err != nil {
		t.Fatalf("GetVectors failed: %v", err)
	}
	if len(vectors) != 1 || vectors[0].TargetID != "msg-1" {
		t.Fatalf("unexpected vectors: %#v", vectors)
	}

	var cfg map[string]any
	if err := b.LoadConfig("codingagents", "global", &cfg); err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg["mode"] != "local" {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}

func TestBlackboardSchemaVersionAndMigrate(t *testing.T) {
	b, err := NewBlackboard(filepath.Join(t.TempDir(), "blackboard.db"))
	if err != nil {
		t.Fatalf("NewBlackboard failed: %v", err)
	}
	defer b.Close()

	version, err := b.SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion failed: %v", err)
	}
	if version != currentSchemaVersion {
		t.Fatalf("expected schema version %d, got %d", currentSchemaVersion, version)
	}
	if err := b.Migrate(); err != nil {
		t.Fatalf("Migrate should be idempotent: %v", err)
	}
}

func TestBlackboardQueryVectorsFiltersMetadata(t *testing.T) {
	b, err := NewBlackboard(filepath.Join(t.TempDir(), "blackboard.db"))
	if err != nil {
		t.Fatalf("NewBlackboard failed: %v", err)
	}
	defer b.Close()

	entries := []VectorEntry{
		{
			ID:       "vec-memory",
			RunID:    "run-1",
			TargetID: "msg-1",
			Vector:   []float32{1, 0, 0},
			Metadata: map[string]string{"kind": "memory", "embedding_model_id": "embed-a"},
		},
		{
			ID:       "vec-code-a",
			RunID:    "run-1",
			TargetID: "code-a",
			Vector:   []float32{0, 1, 0},
			Metadata: map[string]string{"kind": "code", "source_path": "internal/a.go", "embedding_model_id": "embed-a"},
		},
		{
			ID:       "vec-code-b",
			RunID:    "run-2",
			TargetID: "code-b",
			Vector:   []float32{0, 0, 1},
			Metadata: map[string]string{"role": "code", "phase": "pkg/b.go", "embedding_model_id": "embed-b"},
		},
	}
	for _, entry := range entries {
		if err := b.SaveVector(entry); err != nil {
			t.Fatalf("SaveVector(%s) failed: %v", entry.ID, err)
		}
	}

	code, err := b.QueryVectors(VectorQuery{Kind: "code"})
	if err != nil {
		t.Fatalf("QueryVectors code failed: %v", err)
	}
	if len(code) != 2 {
		t.Fatalf("expected two code vectors, got %#v", code)
	}

	internal, err := b.QueryVectors(VectorQuery{Kind: "code", SourcePathPrefix: "internal/"})
	if err != nil {
		t.Fatalf("QueryVectors source failed: %v", err)
	}
	if len(internal) != 1 || internal[0].ID != "vec-code-a" {
		t.Fatalf("unexpected source-filtered vectors: %#v", internal)
	}

	model, err := b.QueryVectors(VectorQuery{EmbeddingModelID: "embed-b", Dimensions: 3})
	if err != nil {
		t.Fatalf("QueryVectors model failed: %v", err)
	}
	if len(model) != 1 || model[0].ID != "vec-code-b" {
		t.Fatalf("unexpected model-filtered vectors: %#v", model)
	}

	limited, err := b.QueryVectors(VectorQuery{Kind: "code", Limit: 1})
	if err != nil {
		t.Fatalf("QueryVectors limit failed: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("expected one limited vector, got %#v", limited)
	}
}

func TestBlackboardRunLifecycleTimelineAndReconstruction(t *testing.T) {
	b, err := NewBlackboard(filepath.Join(t.TempDir(), "blackboard.db"))
	if err != nil {
		t.Fatalf("NewBlackboard failed: %v", err)
	}
	defer b.Close()

	if err := b.StartWorkflowRun("run-1", "agentic"); err != nil {
		t.Fatalf("StartWorkflowRun failed: %v", err)
	}
	if err := b.RecordNodeStart("run-1", "planner"); err != nil {
		t.Fatalf("RecordNodeStart failed: %v", err)
	}
	if err := b.SaveMessage(Message{ID: "msg-1", RunID: "run-1", NodeID: "planner", Role: "planner", Content: "plan", Visible: true}); err != nil {
		t.Fatalf("SaveMessage failed: %v", err)
	}
	if err := b.SaveEvent(Event{ID: "evt-1", RunID: "run-1", NodeID: "planner", Type: "tool_requested", Payload: map[string]any{"tool": "explorer"}}); err != nil {
		t.Fatalf("SaveEvent failed: %v", err)
	}
	if err := b.SaveArtifact("run-1", "planner", artifact.Artifact{ID: "art-1", Kind: artifact.KindMarkdown, URI: "plan.md", Metadata: map[string]any{"name": "technical_plan"}}); err != nil {
		t.Fatalf("SaveArtifact failed: %v", err)
	}
	if err := b.RecordNodeCompletion("run-1", "planner", "completed", ""); err != nil {
		t.Fatalf("RecordNodeCompletion failed: %v", err)
	}
	if err := b.CancelWorkflowRun("run-1"); err != nil {
		t.Fatalf("CancelWorkflowRun failed: %v", err)
	}

	run, err := b.GetWorkflowRun("run-1")
	if err != nil {
		t.Fatalf("GetWorkflowRun failed: %v", err)
	}
	if run.Status != "cancelled" || run.CompletedAt == nil {
		t.Fatalf("unexpected run status: %#v", run)
	}

	runs, err := b.ListWorkflowRuns(10)
	if err != nil {
		t.Fatalf("ListWorkflowRuns failed: %v", err)
	}
	if len(runs) != 1 || runs[0].RunID != "run-1" {
		t.Fatalf("unexpected runs: %#v", runs)
	}

	nodes, err := b.GetNodeRuns("run-1")
	if err != nil {
		t.Fatalf("GetNodeRuns failed: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Status != "completed" {
		t.Fatalf("unexpected nodes: %#v", nodes)
	}

	timeline, err := b.GetTimeline("run-1")
	if err != nil {
		t.Fatalf("GetTimeline failed: %v", err)
	}
	if len(timeline) != 3 {
		t.Fatalf("expected message, event, artifact timeline entries, got %#v", timeline)
	}

	snapshot, err := b.ReconstructRun("run-1")
	if err != nil {
		t.Fatalf("ReconstructRun failed: %v", err)
	}
	if snapshot.Run.Status != "cancelled" || len(snapshot.Messages) != 1 || len(snapshot.Events) != 1 || len(snapshot.Artifacts) != 1 || len(snapshot.Nodes) != 1 {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
}
