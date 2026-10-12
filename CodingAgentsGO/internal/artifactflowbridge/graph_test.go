package artifactflowbridge

import "testing"

func TestValidateRoleGraphAcceptsAcyclicGraph(t *testing.T) {
	graph := map[string][]string{
		"context": {"planner"},
		"planner": {"coder", "critic"},
		"coder":   {"synthesizer"},
		"critic":  {"synthesizer"},
	}

	if err := ValidateRoleGraph(graph); err != nil {
		t.Fatalf("expected graph to be valid: %v", err)
	}
}

func TestValidateRoleGraphRejectsCycle(t *testing.T) {
	graph := map[string][]string{
		"planner": {"critic"},
		"critic":  {"planner"},
	}

	if err := ValidateRoleGraph(graph); err == nil {
		t.Fatal("expected cycle to be rejected")
	}
}
