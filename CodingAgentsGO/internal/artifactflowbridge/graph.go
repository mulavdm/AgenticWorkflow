package artifactflowbridge

import (
	"fmt"

	artifactworkflow "github.com/ArtifactFlow/ArtifactFlow/pkg/workflow"
)

// ValidateRoleGraph checks that the CodingAgents visual role graph is a valid DAG.
// CodingAgents stores edges as from -> allowed next roles. ArtifactFlow models a
// node's prerequisites, so each allowed next role depends on the current role.
func ValidateRoleGraph(graph map[string][]string) error {
	if len(graph) == 0 {
		return nil
	}

	nodes := make(map[string]*artifactworkflow.Node)
	ensureNode := func(id string) *artifactworkflow.Node {
		if nodes[id] == nil {
			nodes[id] = &artifactworkflow.Node{
				ID:        id,
				Processor: "codingagents.role",
			}
		}
		return nodes[id]
	}

	for from, transitions := range graph {
		ensureNode(from)
		for _, to := range transitions {
			node := ensureNode(to)
			node.DependsOn = append(node.DependsOn, from)
		}
	}

	wf := artifactworkflow.Workflow{
		Name:  "codingagents-role-graph",
		Nodes: make([]artifactworkflow.Node, 0, len(nodes)),
	}
	for _, node := range nodes {
		wf.Nodes = append(wf.Nodes, *node)
	}

	if _, err := artifactworkflow.TopologicalSort(wf); err != nil {
		return fmt.Errorf("invalid role graph: %w", err)
	}
	return nil
}
