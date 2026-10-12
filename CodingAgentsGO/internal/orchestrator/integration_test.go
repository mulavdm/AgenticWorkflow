package orchestrator

import (
	"context"
	"fmt"
	"github.com/user/cognitive-orchestrator/internal/memory"
	"github.com/user/cognitive-orchestrator/internal/tool"
	"github.com/user/cognitive-orchestrator/internal/types"
	"strings"
	"testing"
	"time"
	"path/filepath"

	afstate "github.com/ArtifactFlow/ArtifactFlow/pkg/state"
)

type mockAgent struct {
	role types.Role
	resp string
}

func (m *mockAgent) Respond(ctx context.Context, state *types.CouncilState, instructions string) (types.AgentResponse, error) {
	return types.AgentResponse{Content: m.resp}, nil
}

func (m *mockAgent) GetRole() types.Role            { return m.role }
func (m *mockAgent) GetConfig() types.AgentConfig   { return types.AgentConfig{Role: m.role} }
func (m *mockAgent) UpdateConfig(types.AgentConfig) {}

type functionalMockAgent struct {
	role    types.Role
	respond func(ctx context.Context, state *types.CouncilState) string
}

func (m *functionalMockAgent) Respond(ctx context.Context, state *types.CouncilState, instructions string) (types.AgentResponse, error) {
	return types.AgentResponse{Content: m.respond(ctx, state)}, nil
}

func (m *functionalMockAgent) GetRole() types.Role            { return m.role }
func (m *functionalMockAgent) GetConfig() types.AgentConfig   { return types.AgentConfig{Role: m.role} }
func (m *functionalMockAgent) UpdateConfig(types.AgentConfig) {}

type mockEmbeddingProvider struct{}

func (m *mockEmbeddingProvider) GenerateEmbedding(ctx context.Context, text string) ([]float32, error) {
	return []float32{0.1, 0.2, 0.3}, nil
}
func (m *mockEmbeddingProvider) GetDimensions() int { return 3 }

type mockVectorStore struct{}

func (m *mockVectorStore) IndexMessage(ctx context.Context, msgID string, vector []float32, metadata map[string]string) error {
	return nil
}
func (m *mockVectorStore) SearchMessages(ctx context.Context, queryVector []float32, limit int) ([]string, error) {
	return []string{}, nil
}
func (m *mockVectorStore) GetMessage(ctx context.Context, id string) (types.Message, error) {
	return types.Message{}, fmt.Errorf("not implemented")
}
func (m *mockVectorStore) DeletePrefix(ctx context.Context, prefix string) error {
	return nil
}
func (m *mockVectorStore) Close() error { return nil }

type mockStore struct {
	vector *mockVectorStore
}

func (m *mockStore) SaveLesson(ctx context.Context, lesson memory.Lesson) error { return nil }
func (m *mockStore) SaveLessonsFromSession(ctx context.Context, sessionID string, messages []types.Message) error {
	return nil
}
func (m *mockStore) GetRelevantLessons(ctx context.Context, query string) ([]memory.Lesson, error) {
	return []memory.Lesson{}, nil
}
func (m *mockStore) GetVectorStore() types.VectorStore { return m.vector }
func (m *mockStore) Close() error                      { return nil }

func TestSharedWorkspaceIntegration(t *testing.T) {
	tools := tool.NewRegistry()
	tools.Register(&tool.FileExplorer{})

	mem := &mockStore{vector: &mockVectorStore{}}
	emb := &mockEmbeddingProvider{}

	coord := NewCoordinator(mem, tools, nil, nil)
	coord.Embedding = emb

	// Setup mock agents
	contextTurn := 0
	coord.AddAgent(&mockAgent{
		role: types.RoleContext,
		resp: "[TOOL: explorer, ARGS: {\"action\": \"list\", \"path\": \".\"}]",
	})
	// Wait, if I do this, it will call explorer, get an observation, and then call Analyst AGAIN.
	// So I need the Analyst to respond differently on the second turn.

	// Let's use a more flexible mock agent
	coord.Agents[types.RoleContext] = &functionalMockAgent{
		role: types.RoleContext,
		respond: func(ctx context.Context, state *types.CouncilState) string {
			if contextTurn == 0 {
				contextTurn++
				return "[TOOL: explorer, ARGS: {\"action\": \"list\", \"path\": \".\"}]"
			}
			return "Found files.\n[PROJECT MAP]\n- internal/types\n[/PROJECT MAP]\n[NEXT: planner]"
		},
	}

	coord.AddAgent(&mockAgent{
		role: types.RolePlanner,
		resp: "Planning...\n[TECHNICAL PLAN]\n1. Edit state.go\n[/TECHNICAL PLAN]\n[NEXT: completed]",
	})

	ctx := context.Background()
	prompt := "Test shared workspace"

	// Pre-seed state with an explorer observation to satisfy the Guard's grounding gate
	state := types.NewCouncilState("integration-1", "trace-1")
	state.AddMessage(types.RoleSystem, "[OBSERVATION from explorer]: Found files.", nil, nil)
	state.AddMessage(types.RoleUser, prompt, nil, nil)

	// We need a way to run Execute with a pre-seeded state or just bypass the gate in the mock.
	// Actually, NewCoordinator creates a real Guard.

	// Let's just have the mock analyst return a tool call first?
	// Or even simpler: the Guard is doing its job. I'll just skip the gate by using a different role
	// or mocking the guard.

	// But wait, the point is to test the integration.
	// I'll update the mock analyst to perform a tool call first.

	// We'll pass an empty targetRole to allow the loop to follow handoffs
	messages, err := coord.Execute(ctx, prompt, "", nil)

	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	fmt.Println("Execution Messages:")
	for _, m := range messages {
		fmt.Printf("[%s] %s\n", m.Role, m.Content)
	}

	// Find the final state (it's not directly returned, but we can check the messages)
	// Actually, coord.Execute returns messages.
	// But we want to check the Artifacts in the state used by coord.Execute.
	// Since Execute creates a new state, we need a way to inspect it.

	// Let's check the messages for the tags
	foundMap := false
	foundPlan := false
	for _, m := range messages {
		if strings.Contains(m.Content, "[PROJECT MAP]") {
			foundMap = true
		}
		if strings.Contains(m.Content, "[TECHNICAL PLAN]") {
			foundPlan = true
		}
	}

	if !foundMap {
		t.Error("Project map tag missing from messages")
	}
	if !foundPlan {
		t.Error("Technical plan tag missing from messages")
	}

	// To verify that the internal state was updated, we would need to capture the state.
	// We can do this by passing a Broadcaster that captures the "session_complete" event.
}

type captureBroadcaster struct {
	state *types.CouncilState
}

func (b *captureBroadcaster) Broadcast(evt types.UIEvent) {
	if evt.Type == "session_complete" {
		b.state = evt.Payload.(*types.CouncilState)
	}
}

func TestArtifactMaterialization(t *testing.T) {
	board, err := afstate.NewBlackboard(filepath.Join(t.TempDir(), "artifactflow.db"))
	if err != nil {
		t.Fatalf("NewBlackboard failed: %v", err)
	}
	defer board.Close()

	tools := tool.NewRegistry()
	tools.Register(&tool.FileExplorer{})
	bc := &captureBroadcaster{}

	mem := memory.NewArtifactFlowStore(board)
	emb := &mockEmbeddingProvider{}

	coord := NewCoordinator(mem, tools, bc, nil)
	coord.Embedding = emb
	coord.SetArtifactFlow(board)

	coord.AddAgent(&mockAgent{
		role: types.RoleContext,
		resp: "[PROJECT MAP]\n- root\n[/PROJECT MAP]\n[NEXT: completed]",
	})

	ctx := context.Background()
	const runID = "run-materialization-1"

	// Pre-seed blackboard with an explorer observation to satisfy the Analyst grounding gate
	err = board.SaveMessage(afstate.Message{
		ID:        "msg-seed-explorer",
		RunID:     runID,
		NodeID:    "context_execute_1",
		Role:      "system",
		Content:   "[OBSERVATION from explorer]: Directory listing.",
		Visible:   true,
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("SaveMessage failed: %v", err)
	}

	_, _ = coord.ExecuteRun(ctx, runID, "test", types.RoleContext, nil)

	if bc.state == nil {
		t.Fatal("Session complete event not received")
	}

	if bc.state.Artifacts.ProjectMap != "- root" {
		t.Errorf("Artifact ProjectMap not materialized, got: %q", bc.state.Artifacts.ProjectMap)
	}
}
