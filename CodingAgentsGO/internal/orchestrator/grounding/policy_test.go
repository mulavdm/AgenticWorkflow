package grounding

import (
	"testing"

	"github.com/user/cognitive-orchestrator/internal/types"
)

func TestToolPolicyDecisions(t *testing.T) {
	policy := DefaultToolPolicy()
	tests := []struct {
		name     string
		role     types.Role
		tool     string
		args     string
		decision ToolDecision
	}{
		{
			name:     "explorer is allowed",
			role:     types.RoleContext,
			tool:     "explorer",
			args:     `{"action":"list","path":"."}`,
			decision: ToolDecisionAllow,
		},
		{
			name:     "planner writer is denied",
			role:     types.RolePlanner,
			tool:     "writer",
			args:     `{"action":"write","path":"scratch/note.txt","content":"x"}`,
			decision: ToolDecisionDeny,
		},
		{
			name:     "coder writer in scratch is allowed",
			role:     types.RoleCoder,
			tool:     "writer",
			args:     `{"action":"write","path":"scratch/note.txt","content":"x"}`,
			decision: ToolDecisionAllow,
		},
		{
			name:     "coder writer outside scratch requires approval",
			role:     types.RoleCoder,
			tool:     "writer",
			args:     `{"action":"write","path":"README.md","content":"x"}`,
			decision: ToolDecisionRequiresApproval,
		},
		{
			name:     "merge requires approval",
			role:     types.RoleCoder,
			tool:     "writer",
			args:     `{"action":"merge","path":"README.md"}`,
			decision: ToolDecisionRequiresApproval,
		},
		{
			name:     "dockerrunner requires approval",
			role:     types.RoleSecurity,
			tool:     "dockerrunner",
			args:     `{"command":"go test ./..."}`,
			decision: ToolDecisionRequiresApproval,
		},
		{
			name:     "long coderunner requires approval",
			role:     types.RoleCoder,
			tool:     "coderunner",
			args:     `{"language":"go","code":"package main","timeout_seconds":60}`,
			decision: ToolDecisionRequiresApproval,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := policy.Decide(tt.role, tt.tool, tt.args)
			if got.Decision != tt.decision {
				t.Fatalf("decision = %s, want %s (%s)", got.Decision, tt.decision, got.Reason)
			}
		})
	}
}

func TestToolPolicyConfigKeepsConservativeDefaults(t *testing.T) {
	policy := ToolPolicyFromConfig(ToolPolicyConfig{})
	cfg := policy.Config()
	if cfg.CodeRunnerApprovalThresholdMS <= 0 {
		t.Fatalf("expected positive coderunner threshold, got %#v", cfg)
	}
	if len(cfg.SafeWritePrefixes) == 0 {
		t.Fatalf("expected default safe write prefixes, got %#v", cfg)
	}
	if got := policy.Decide(types.RoleCoder, "writer", `{"action":"write","path":"README.md","content":"x"}`); got.Decision != ToolDecisionRequiresApproval {
		t.Fatalf("writer outside safe prefixes should require approval, got %#v", got)
	}
}

func TestToolPolicyConfigCanExtendSafeWritePrefixes(t *testing.T) {
	policy := ToolPolicyFromConfig(ToolPolicyConfig{
		CodeRunnerApprovalThresholdMS: 1000,
		SafeWritePrefixes:             []string{"scratch", ".shadow_workspaces", "docs/generated"},
	})
	if got := policy.Decide(types.RoleCoder, "writer", `{"action":"write","path":"docs/generated/report.md","content":"x"}`); got.Decision != ToolDecisionAllow {
		t.Fatalf("configured safe prefix should allow writer, got %#v", got)
	}
	if got := policy.Decide(types.RoleCoder, "coderunner", `{"timeout_seconds":2}`); got.Decision != ToolDecisionRequiresApproval {
		t.Fatalf("configured coderunner threshold should require approval, got %#v", got)
	}
}
