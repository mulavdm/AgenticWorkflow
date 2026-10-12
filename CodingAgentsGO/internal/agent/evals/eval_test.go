package evals

import (
	"strings"
	"testing"

	"github.com/user/cognitive-orchestrator/internal/types"
)

func TestGoldenRoleFixtures(t *testing.T) {
	fixtures := []Fixture{
		{
			Name:           "analyst project map",
			Role:           types.RoleContext,
			Response:       "[PROJECT MAP]\n- Observed packages.\n[/PROJECT MAP]\n[NEXT: planner]",
			AllowedTools:   []string{"explorer", "memory_retriever", "codebase_search"},
			RequiredTags:   []string{"[PROJECT MAP]", "[NEXT: planner]"},
			ForbiddenTexts: []string{"[OBSERVATION from"},
		},
		{
			Name:           "planner technical plan",
			Role:           types.RolePlanner,
			Response:       "[TECHNICAL PLAN]\n1. Update state tests.\n[/TECHNICAL PLAN]\n[NEXT: coder]",
			AllowedTools:   []string{"explorer", "memory_retriever"},
			RequiredTags:   []string{"[TECHNICAL PLAN]", "[NEXT: coder]"},
			ForbiddenTexts: []string{"[OBSERVATION from"},
		},
		{
			Name:           "coder execution logs",
			Role:           types.RoleCoder,
			Response:       "[EXECUTION LOGS]\n- Changed files.\n- Verification result: go test ./...\n[/EXECUTION LOGS]\n[NEXT: synthesizer]",
			AllowedTools:   []string{"explorer", "writer", "coderunner", "dockerrunner", "memory_retriever"},
			RequiredTags:   []string{"[EXECUTION LOGS]", "Verification result", "[NEXT: synthesizer]"},
			ForbiddenTexts: []string{"verification passed without running"},
		},
		{
			Name:           "critic critique",
			Role:           types.RoleCritic,
			Response:       "[CRITIQUE]\nNo blocking findings.\n[/CRITIQUE]\n[NEXT: completed]",
			AllowedTools:   []string{"explorer", "memory_retriever"},
			RequiredTags:   []string{"[CRITIQUE]", "[NEXT: completed]"},
			ForbiddenTexts: []string{"[TOOL: writer"},
		},
		{
			Name:           "security report",
			Role:           types.RoleRedTeamer,
			Response:       "[SECURITY REPORT]\nResidual risk: prompt logging.\n[/SECURITY REPORT]\n[NEXT: coder]",
			AllowedTools:   []string{"explorer", "memory_retriever", "coderunner"},
			RequiredTags:   []string{"[SECURITY REPORT]", "[NEXT: coder]"},
			ForbiddenTexts: []string{"[TOOL: writer"},
		},
		{
			Name:           "synthesizer final",
			Role:           types.RoleSynthesizer,
			Response:       "Implemented and verified the scoped changes.\n[NEXT: completed]",
			AllowedTools:   nil,
			RequiredTags:   []string{"[NEXT: completed]"},
			ForbiddenTexts: []string{"[TOOL:"},
		},
	}

	for _, result := range Run(fixtures) {
		if !result.Passed {
			t.Fatalf("%s failed: %s", result.Name, strings.Join(result.Errors, "; "))
		}
	}
}

func TestEvalRejectsForbiddenToolAndMissingTerminalAction(t *testing.T) {
	result := Evaluate(Fixture{
		Name:           "bad critic",
		Role:           types.RoleCritic,
		Response:       "[CRITIQUE]\nLooks good.\n[TOOL: writer, ARGS: {\"action\":\"write\"}] trailing text",
		AllowedTools:   []string{"explorer", "memory_retriever"},
		RequiredTags:   []string{"[CRITIQUE]"},
		ForbiddenTexts: []string{"[OBSERVATION from"},
	})
	if result.Passed {
		t.Fatal("expected fixture to fail")
	}
	if len(result.Errors) < 2 {
		t.Fatalf("expected multiple errors, got %#v", result.Errors)
	}
}
