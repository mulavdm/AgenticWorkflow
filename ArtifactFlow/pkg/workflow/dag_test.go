package workflow

import "testing"

func TestTopologicalSortOrdersDependenciesBeforeDependents(t *testing.T) {
	wf := Workflow{
		Name: "test",
		Nodes: []Node{
			{ID: "final", DependsOn: []string{"middle"}},
			{ID: "middle", DependsOn: []string{"start"}},
			{ID: "start"},
		},
	}

	ordered, err := TopologicalSort(wf)
	if err != nil {
		t.Fatalf("TopologicalSort returned error: %v", err)
	}

	got := []string{ordered[0].ID, ordered[1].ID, ordered[2].ID}
	want := []string{"start", "middle", "final"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected order: got %v, want %v", got, want)
		}
	}
}

func TestTopologicalSortRejectsCycle(t *testing.T) {
	wf := Workflow{
		Name: "cycle",
		Nodes: []Node{
			{ID: "a", DependsOn: []string{"b"}},
			{ID: "b", DependsOn: []string{"a"}},
		},
	}

	if _, err := TopologicalSort(wf); err == nil {
		t.Fatal("expected cycle to be rejected")
	}
}

func TestTopologicalSortRejectsMissingDependsOnNode(t *testing.T) {
	wf := Workflow{
		Name: "missing",
		Nodes: []Node{
			{ID: "a", DependsOn: []string{"missing"}},
		},
	}

	if _, err := TopologicalSort(wf); err == nil {
		t.Fatal("expected missing depends_on node to be rejected")
	}
}

func TestTopologicalSortAllowsExternalInputs(t *testing.T) {
	wf := Workflow{
		Name: "external-input",
		Nodes: []Node{
			{ID: "a", Inputs: []string{"external-artifact"}},
		},
	}

	if _, err := TopologicalSort(wf); err != nil {
		t.Fatalf("expected external inputs to be allowed, got %v", err)
	}
}
