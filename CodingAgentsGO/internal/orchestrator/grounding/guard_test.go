package grounding

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/user/cognitive-orchestrator/internal/tool"
	"github.com/user/cognitive-orchestrator/internal/types"
)

func TestGuardBlocksPrivilegedToolsByRole(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&tool.FileWriter{})
	registry.Register(&tool.DockerRunner{})

	guard := NewGuard(registry, nil)
	state := types.NewCouncilState("session-1", "trace-1")

	_, err := guard.ValidateResponse(context.Background(), state, types.RolePlanner, `[TOOL: writer, ARGS: {"action":"write","path":"README.md","content":"x"}]`, nil)
	if err == nil || !strings.Contains(err.Error(), "writer is restricted") {
		t.Fatalf("expected planner writer call to be blocked, got %v", err)
	}

	_, err = guard.ValidateResponse(context.Background(), state, types.RoleCritic, `[TOOL: dockerrunner, ARGS: {"command":"go test ./...","language":"go"}]`, nil)
	if err == nil || !strings.Contains(err.Error(), "dockerrunner is restricted") {
		t.Fatalf("expected critic dockerrunner call to be blocked, got %v", err)
	}
}

func TestGuardBlocksAutonomousMerge(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&tool.FileWriter{})

	guard := NewGuard(registry, nil)
	state := types.NewCouncilState("session-1", "trace-1")
	state.AddMessage(types.RoleSystem, "[OBSERVATION from explorer]: README.md", nil, nil)

	_, err := guard.ValidateResponse(context.Background(), state, types.RoleCoder, `[TOOL: writer, ARGS: {"action":"merge","path":"README.md"}]`, nil)
	if !errors.Is(err, types.ErrApprovalRequired) {
		t.Fatalf("expected autonomous merge to require approval, got %v", err)
	}
	foundApproval := false
	for _, evt := range state.Events {
		if evt.Type == types.EventApprovalRequested {
			foundApproval = true
			break
		}
	}
	if !foundApproval {
		t.Fatalf("expected approval request event, got %#v", state.Events)
	}
}

func TestGuardRequiresApprovalForPrivilegedTools(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&tool.FileWriter{})
	registry.Register(&tool.DockerRunner{})

	guard := NewGuard(registry, nil)
	state := types.NewCouncilState("session-1", "trace-1")
	state.AddMessage(types.RoleSystem, "[OBSERVATION from explorer]: README.md", nil, nil)

	_, err := guard.ValidateResponse(context.Background(), state, types.RoleCoder, `[TOOL: writer, ARGS: {"action":"write","path":"README.md","content":"x"}]`, nil)
	if !errors.Is(err, types.ErrApprovalRequired) {
		t.Fatalf("expected writer outside scratch to require approval, got %v", err)
	}
	if !hasPolicyDecision(state, ToolDecisionRequiresApproval) {
		t.Fatalf("expected persisted requires_approval policy decision, got %#v", state.Events)
	}

	_, err = guard.ValidateResponse(context.Background(), state, types.RoleSecurity, `[TOOL: dockerrunner, ARGS: {"command":"go test ./...","language":"go"}]`, nil)
	if !errors.Is(err, types.ErrApprovalRequired) {
		t.Fatalf("expected dockerrunner to require approval, got %v", err)
	}
}

func TestGuardRecordsAllowPolicyDecision(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&tool.FileExplorer{})

	guard := NewGuard(registry, nil)
	state := types.NewCouncilState("session-1", "trace-1")

	_, err := guard.ValidateResponse(context.Background(), state, types.RoleContext, `[TOOL: explorer, ARGS: {"action":"list","path":"."}]`, nil)
	if err != types.ErrToolExecuted {
		t.Fatalf("expected tool execution signal, got %v", err)
	}
	if !hasPolicyDecision(state, ToolDecisionAllow) {
		t.Fatalf("expected persisted allow policy decision, got %#v", state.Events)
	}
}

func hasPolicyDecision(state *types.CouncilState, decision ToolDecision) bool {
	for _, evt := range state.Events {
		if evt.Type != types.EventToolPolicyDecided {
			continue
		}
		payload, ok := evt.Payload.(map[string]string)
		if ok && payload["decision"] == string(decision) {
			return true
		}
	}
	return false
}

func TestGuardDeniesMalformedToolJSONBeforeApproval(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&tool.FileWriter{})

	guard := NewGuard(registry, nil)
	state := types.NewCouncilState("session-1", "trace-1")

	_, err := guard.ValidateResponse(context.Background(), state, types.RoleCoder, `[TOOL: writer, ARGS: {"action":"write","path":"README.md","content":"x"`, nil)
	if err == nil || !strings.Contains(err.Error(), "malformed tool JSON") {
		t.Fatalf("expected malformed JSON denial, got %v", err)
	}
	for _, evt := range state.Events {
		if evt.Type == types.EventApprovalRequested {
			t.Fatalf("malformed tool JSON should not create approval event: %#v", state.Events)
		}
	}
}

func TestGuardDeniesWriterOutsideDeclaredTaskScope(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&tool.FileWriter{})

	guard := NewGuard(registry, nil)
	state := types.NewCouncilState("session-1", "trace-1")
	state.AddMessage(types.RoleUser, "Only write logs/dogfood_writer_smoke.md.", nil, nil)
	state.AddMessage(types.RoleSystem, "[OBSERVATION from explorer]: internal/types/types.go\nlogs/dogfood_writer_smoke.md", nil, nil)

	_, err := guard.ValidateResponse(context.Background(), state, types.RoleCoder, `[TOOL: writer, ARGS: {"action":"write","path":"internal/types/types.go","content":"x"}]`, nil)
	if err == nil || !strings.Contains(err.Error(), "outside the declared task scope") {
		t.Fatalf("expected scope denial, got %v", err)
	}
	for _, evt := range state.Events {
		if evt.Type == types.EventApprovalRequested {
			t.Fatalf("out-of-scope writer call should not create approval event: %#v", state.Events)
		}
	}
}

func TestGuardAllowsWriterNamedInTechnicalPlanToReachApproval(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&tool.FileWriter{})

	guard := NewGuard(registry, nil)
	state := types.NewCouncilState("session-1", "trace-1")
	state.AddMessage(types.RoleUser, "Implement the approved plan.", nil, nil)
	state.Artifacts.TechnicalPlan = "Update internal/types/types.go."
	state.AddMessage(types.RoleSystem, "[OBSERVATION from explorer]: internal/types/types.go", nil, nil)

	_, err := guard.ValidateResponse(context.Background(), state, types.RoleCoder, `[TOOL: writer, ARGS: {"action":"write","path":"internal/types/types.go","content":"x"}]`, nil)
	if !errors.Is(err, types.ErrApprovalRequired) {
		t.Fatalf("expected in-scope privileged writer call to require approval, got %v", err)
	}
}

func TestGuardDetectsDuplicateToolCall(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&tool.FileExplorer{})

	guard := NewGuard(registry, nil)
	state := types.NewCouncilState("session-1", "trace-1")

	_, err := guard.ValidateResponse(context.Background(), state, types.RoleContext, `[TOOL: explorer, ARGS: {"action":"list","path":"."}]`, nil)
	if err != types.ErrToolExecuted {
		t.Fatalf("expected first tool execution signal, got %v", err)
	}

	_, err = guard.ValidateResponse(context.Background(), state, types.RoleContext, `[TOOL: explorer, ARGS: {"path":"./","action":"list"}]`, nil)
	if err == nil || !strings.Contains(err.Error(), "loop detected") {
		t.Fatalf("expected duplicate semantic call to be detected, got %v", err)
	}
}

func TestGuardExtractFirstToolCallFalsePositive(t *testing.T) {
	guard := NewGuard(tool.NewRegistry(), nil)
	name, args, fullMatch := guard.ExtractFirstToolCall(`Reasoning: The enforceWriterScope function perfectly describes the memory reconciliation logic for the writer tool:
1`)
	if fullMatch != "" {
		t.Fatalf("expected no tool call to be extracted from natural language text, but got tool %q with args %q (full match: %q)", name, args, fullMatch)
	}
}

func TestGuardExtractFirstToolCallWithBraces(t *testing.T) {
	guard := NewGuard(tool.NewRegistry(), nil)
	content := `Reasoning: Let's write a Fibonacci function.
[TOOL: writer, ARGS: {"action":"write","path":"math.go","content":"func Fibonacci(n int) int {\n\tif n <= 0 {\n\t\treturn 0\n\t}\n\treturn 1\n}"}]`
	name, args, fullMatch := guard.ExtractFirstToolCall(content)

	expectedArgs := `{"action":"write","path":"math.go","content":"func Fibonacci(n int) int {\n\tif n <= 0 {\n\t\treturn 0\n\t}\n\treturn 1\n}"}`
	if name != "writer" {
		t.Fatalf("expected tool name 'writer', got %q", name)
	}
	if args != expectedArgs {
		t.Fatalf("expected args:\n%s\nbut got:\n%s", expectedArgs, args)
	}
	if !strings.Contains(fullMatch, "writer") || !strings.HasSuffix(fullMatch, "]") {
		t.Fatalf("invalid fullMatch: %q", fullMatch)
	}
}
