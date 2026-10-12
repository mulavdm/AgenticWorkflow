package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	afstate "github.com/ArtifactFlow/ArtifactFlow/pkg/state"
	"github.com/user/cognitive-orchestrator/internal/memory"
	"github.com/user/cognitive-orchestrator/internal/types"
)

func TestUpdateArtifacts(t *testing.T) {
	c := &Coordinator{}
	state := types.NewCouncilState("session-1", "trace-1")
	state.WorkspaceMode = true

	// Test Project Map
	content := "Mapping the project...\n[PROJECT MAP]\n- root: main.go\n- internal: logic\n[/PROJECT MAP]\n[NEXT: planner]"
	c.updateArtifacts(state, types.RoleAnalyst, content)
	if state.Artifacts.ProjectMap != "- root: main.go\n- internal: logic" {
		t.Errorf("ProjectMap not extracted correctly, got: %s", state.Artifacts.ProjectMap)
	}

	// Test Technical Plan
	content = "Here is the plan:\n[TECHNICAL PLAN]\n1. Step one\n2. Step two\n[/TECHNICAL PLAN]\n[NEXT: coder]"
	c.updateArtifacts(state, types.RolePlanner, content)
	if state.Artifacts.TechnicalPlan != "1. Step one\n2. Step two" {
		t.Errorf("TechnicalPlan not extracted correctly, got: %s", state.Artifacts.TechnicalPlan)
	}

	// Test Critique
	content = "[CRITIQUE]\nThe plan is too complex.\n[/CRITIQUE]\n[NEXT: planner]"
	c.updateArtifacts(state, types.RoleCritic, content)
	if state.Artifacts.Critique != "The plan is too complex." {
		t.Errorf("Critique not extracted correctly, got: %s", state.Artifacts.Critique)
	}

	// Test Observation append to Execution Logs
	content = "[OBSERVATION from explorer]: File Content:\npackage main..."
	c.updateArtifacts(state, types.RoleSystem, content)
	if !strings.Contains(state.Artifacts.ExecutionLogs, "package main") {
		t.Error("ExecutionLogs should contain observation content")
	}
}

func TestHookUpdate(t *testing.T) {
	c := &Coordinator{}
	state := types.NewCouncilState("session-2", "trace-2")
	state.WorkspaceMode = true
	state.OnMessageAdded = func(role types.Role, content string) {
		c.updateArtifacts(state, role, content)
	}

	// Adding a message should trigger the hook and update the artifact
	planContent := "[TECHNICAL PLAN]\n1. Fix bug\n2. Run test\n[/TECHNICAL PLAN]\n[NEXT: coder]"
	state.AddMessage(types.RolePlanner, planContent, nil, nil)

	if state.Artifacts.TechnicalPlan != "1. Fix bug\n2. Run test" {
		t.Errorf("Hook failed to update TechnicalPlan, got: %s", state.Artifacts.TechnicalPlan)
	}
}

func TestUpdateArtifactsIgnoresStructuredTagsInSystemObservations(t *testing.T) {
	c := &Coordinator{}
	state := types.NewCouncilState("session-3", "trace-3")
	state.WorkspaceMode = true
	state.WorkspaceDir = t.TempDir()
	state.Artifacts.TechnicalPlan = "keep this plan"

	observation := "[OBSERVATION from explorer]: file contents:\n[TECHNICAL PLAN]\nold stale plan\n[/TECHNICAL PLAN]"
	c.updateArtifacts(state, types.RoleSystem, observation)

	if state.Artifacts.TechnicalPlan != "keep this plan" {
		t.Fatalf("system observation overwrote technical plan: %q", state.Artifacts.TechnicalPlan)
	}
	if !strings.Contains(state.Artifacts.ExecutionLogs, observation) {
		t.Fatalf("system observation was not preserved in execution logs: %q", state.Artifacts.ExecutionLogs)
	}
}

func TestMergeGlobalConfigKeepsBaseWhenOverrideFieldsAreEmpty(t *testing.T) {
	base := types.GlobalConfig{
		DefaultMode: "cloud",
		OpenAIModel: "env-model",
		OpenAIURL:   "http://127.0.0.1:8081/v1/chat/completions",
		OpenAIKey:   "local",
	}
	override := types.GlobalConfig{
		DefaultMode: "local",
		OpenAIModel: "saved-model",
		OpenAIURL:   "",
		OpenAIKey:   "",
	}

	got := mergeGlobalConfig(base, override)
	if got.DefaultMode != "local" {
		t.Fatalf("DefaultMode = %q, want local", got.DefaultMode)
	}
	if got.OpenAIModel != "saved-model" {
		t.Fatalf("OpenAIModel = %q, want saved-model", got.OpenAIModel)
	}
	if got.OpenAIURL != base.OpenAIURL {
		t.Fatalf("OpenAIURL = %q, want %q", got.OpenAIURL, base.OpenAIURL)
	}
	if got.OpenAIKey != base.OpenAIKey {
		t.Fatalf("OpenAIKey = %q, want %q", got.OpenAIKey, base.OpenAIKey)
	}
}

func TestASTChunkGoFile(t *testing.T) {
	content := `package testpkg

import (
	"fmt"
	"os"
)

type Config struct {
	Host string
	Port int
}

func GetConfig() *Config {
	return &Config{Host: "localhost", Port: 8080}
}

func (c *Config) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}
`
	chunks := chunkGoFile("test.go", content)
	if len(chunks) != 4 {
		t.Fatalf("expected 4 chunks (package_imports, Config, GetConfig, Config.Addr), got %d: %+v", len(chunks), chunks)
	}

	// 1st chunk: package + imports
	if chunks[0].Label != "package_imports" {
		t.Errorf("chunk[0] label = %q, want package_imports", chunks[0].Label)
	}
	if !strings.Contains(chunks[0].Content, "import (") {
		t.Errorf("chunk[0] missing imports: %q", chunks[0].Content)
	}

	// 2nd chunk: Config struct
	if chunks[1].Label != "Config" {
		t.Errorf("chunk[1] label = %q, want Config", chunks[1].Label)
	}

	// 3rd chunk: GetConfig function
	if chunks[2].Label != "GetConfig" {
		t.Errorf("chunk[2] label = %q, want GetConfig", chunks[2].Label)
	}

	// 4th chunk: Config.Addr method
	if chunks[3].Label != "Config.Addr" {
		t.Errorf("chunk[3] label = %q, want Config.Addr", chunks[3].Label)
	}
}

func TestIndexCodebaseIncremental(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "blackboard.db")
	board, err := afstate.NewBlackboard(dbPath)
	if err != nil {
		t.Fatalf("failed to create blackboard: %v", err)
	}
	defer board.Close()

	// Set up mock directory structures
	codebaseDir := filepath.Join(tempDir, "mycode")
	if err := os.MkdirAll(codebaseDir, 0755); err != nil {
		t.Fatalf("failed to create code dir: %v", err)
	}

	testFile := filepath.Join(codebaseDir, "logic.go")
	initialContent := `package main
func Hello() string {
	return "world"
}
`
	if err := os.WriteFile(testFile, []byte(initialContent), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// Change working directory to codebaseDir for the indexer walk (since walk root is ".")
	oldWd, _ := os.Getwd()
	if err := os.Chdir(codebaseDir); err != nil {
		t.Fatalf("failed to change wd: %v", err)
	}
	defer os.Chdir(oldWd)

	store := memory.NewArtifactFlowStore(board)
	defer store.Close()

	coord := &Coordinator{
		Memory:    store,
		Embedding: &mockEmbeddingProvider{},
		afBoard:   board,
	}

	ctx := context.Background()

	// 1. First run: should index the new file
	coord.IndexCodebase(ctx)

	// Verify that the chunk is indexed
	chunks, err := board.GetVectors()
	if err != nil {
		t.Fatalf("failed to get vectors: %v", err)
	}
	if len(chunks) != 2 { // imports + func Hello
		t.Fatalf("expected 2 indexed chunks, got %d: %+v", len(chunks), chunks)
	}

	// Check hash cache value in config
	var cachedHash string
	if err := board.LoadConfig("file_hashes", "logic.go", &cachedHash); err != nil {
		t.Fatalf("expected hash cached, got error: %v", err)
	}
	expectedHash := fileSHA256Hash([]byte(initialContent))
	if cachedHash != expectedHash {
		t.Errorf("cached hash = %q, want %q", cachedHash, expectedHash)
	}

	// 2. Second run (incremental): content did not change, should skip indexing
	coord.IndexCodebase(ctx)

	// Count vectors again, should remain identical
	chunks2, _ := board.GetVectors()
	if len(chunks2) != 2 {
		t.Fatalf("expected 2 vectors after cache hit run, got %d", len(chunks2))
	}

	// 3. Third run (changed content): should detect hash mismatch, delete old vectors, and index new
	newContent := `package main
func Hello() string {
	return "updated world"
}
func Extra() int {
	return 42
}
`
	if err := os.WriteFile(testFile, []byte(newContent), 0644); err != nil {
		t.Fatalf("failed to rewrite file: %v", err)
	}

	coord.IndexCodebase(ctx)

	// Verify old vectors were deleted and new ones stored
	chunks3, _ := board.GetVectors()
	// Should have 3 chunks now: package_imports, func Hello, func Extra
	if len(chunks3) != 3 {
		t.Fatalf("expected 3 vectors after file rewrite, got %d: %+v", len(chunks3), chunks3)
	}

	// Verify Hello function has updated content in vector metadata
	foundHello := false
	for _, chunk := range chunks3 {
		if strings.Contains(chunk.ID, "func-Hello") {
			foundHello = true
			if !strings.Contains(chunk.Metadata["content"], "updated world") {
				t.Errorf("Hello chunk content not updated: %q", chunk.Metadata["content"])
			}
		}
	}
	if !foundHello {
		t.Error("func Hello chunk missing after re-indexing")
	}
}
