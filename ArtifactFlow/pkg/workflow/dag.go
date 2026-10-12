package workflow

import (
	"fmt"
)

// Workflow represents a parsed YAML workflow graph.
type Workflow struct {
	Name  string `yaml:"name"`
	Nodes []Node `yaml:"nodes"`
}

// Node represents a single step in the workflow graph.
type Node struct {
	ID        string         `yaml:"id"`
	Processor string         `yaml:"processor"`
	DependsOn []string       `yaml:"depends_on,omitempty"`
	Config    map[string]any `yaml:"config,omitempty"`
	Inputs    []string       `yaml:"inputs,omitempty"`
}

// TopologicalSort returns a flat, ordered list of Nodes such that every node
// appears after all the nodes listed in its DependsOn or Inputs slices.
// Returns an error if there is a cycle or a missing dependency.
func TopologicalSort(wf Workflow) ([]Node, error) {
	nodeMap := make(map[string]Node)
	for _, n := range wf.Nodes {
		nodeMap[n.ID] = n
	}

	var ordered []Node
	visited := make(map[string]bool)
	tempMark := make(map[string]bool)

	var visit func(nodeID string) error
	visit = func(nodeID string) error {
		if tempMark[nodeID] {
			return fmt.Errorf("cyclic dependency detected involving node: %s", nodeID)
		}
		if visited[nodeID] {
			return nil
		}

		node, exists := nodeMap[nodeID]
		if !exists {
			// Check if it's an external input (which won't be in the nodes list)
			// For simplicity in MVP, we just assume any unknown dependency might be an external input.
			// But for strict DAG execution of nodes, if a node depends on another node, it must exist.
			// Let's assume external inputs are handled separately and we only sort nodes.
			return nil // It's an external artifact input, not a node to be executed
		}

		tempMark[nodeID] = true

		for _, depID := range node.DependsOn {
			if _, ok := nodeMap[depID]; !ok {
				return fmt.Errorf("node %s depends on missing node: %s", nodeID, depID)
			}
			if err := visit(depID); err != nil {
				return err
			}
		}

		for _, depID := range node.Inputs {
			if err := visit(depID); err != nil {
				return err
			}
		}

		tempMark[nodeID] = false
		visited[nodeID] = true
		ordered = append(ordered, node)

		return nil
	}

	for _, n := range wf.Nodes {
		if !visited[n.ID] {
			if err := visit(n.ID); err != nil {
				return nil, err
			}
		}
	}

	return ordered, nil
}
