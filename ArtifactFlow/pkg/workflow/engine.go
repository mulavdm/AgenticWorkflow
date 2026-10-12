package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ArtifactFlow/ArtifactFlow/pkg/artifact"
	"github.com/user/CentralLogger"
	"github.com/ArtifactFlow/ArtifactFlow/pkg/processor"
	"github.com/ArtifactFlow/ArtifactFlow/pkg/state"
)

type pauseError interface {
	error
	Paused() bool
}

type Engine struct {
	registry   *processor.Registry
	blackboard *state.Blackboard
}

func NewEngine(r *processor.Registry, b *state.Blackboard) *Engine {
	return &Engine{
		registry:   r,
		blackboard: b,
	}
}

// Run executes a parsed workflow DAG sequentially.
func (e *Engine) Run(ctx context.Context, wf *Workflow, runID string) error {
	logger.Info("Engine", fmt.Sprintf("Starting workflow: %s (RunID: %s)", wf.Name, runID))

	if err := e.blackboard.StartWorkflowRun(runID, wf.Name); err != nil {
		return fmt.Errorf("failed to start workflow run: %w", err)
	}

	orderedNodes, err := TopologicalSort(*wf)
	if err != nil {
		e.blackboard.UpdateWorkflowRun(runID, "failed")
		return fmt.Errorf("DAG resolution failed: %w", err)
	}

	// STUB for future concurrency:
	// Currently we execute nodes strictly sequentially according to Topological Sort.
	// In the future, we can use a worker pool and goroutines. We would check if all
	// dependencies for a node are complete, and if so, dispatch it to a worker channel.

	// Map to keep track of artifacts produced by each node to feed downstream dependencies
	// Key: nodeID, Value: list of generated artifacts
	producedArtifacts := make(map[string][]artifact.Artifact)

	for _, node := range orderedNodes {
		// 1. Check Node Status for Resumability
		status, err := e.blackboard.GetNodeStatus(runID, node.ID)
		if err != nil {
			logger.Error("Engine", fmt.Sprintf("Failed to check node status for %s: %v", node.ID, err))
			// Continue execution anyway
		}

		if status == "completed" {
			logger.Info("Engine", fmt.Sprintf("Skipping already completed Node: %s", node.ID))
			outputs, err := e.blackboard.GetNodeArtifacts(runID, node.ID)
			if err != nil {
				logger.Error("Engine", fmt.Sprintf("Failed to load artifacts for skipped node %s: %v", node.ID, err))
			} else {
				producedArtifacts[node.ID] = outputs
			}
			continue
		}

		logger.Info("Engine", fmt.Sprintf("Executing Node: %s (Processor: %s)", node.ID, node.Processor))
		e.blackboard.RecordNodeStart(runID, node.ID)

		proc, err := e.registry.Get(node.Processor)
		if err != nil {
			e.recordNodeFail(runID, node.ID, err)
			return err
		}
		if err := e.registry.EnsureCapabilities(ctx, proc); err != nil {
			e.recordNodeFail(runID, node.ID, err)
			return err
		}

		// 2. Gather inputs for this node from upstream dependencies
		var inputs []artifact.Artifact
		for _, dep := range append(node.DependsOn, node.Inputs...) {
			if arts, ok := producedArtifacts[dep]; ok {
				inputs = append(inputs, arts...)
			} else {
				// We might be expecting external input from the DB or files here.
				// For the MVP, we assume it's a previous node.
				logger.Debug("Engine", fmt.Sprintf("No produced artifacts found for upstream dependency: %s", dep))
			}
		}

		// 2.5 Dynamic Config Interpolation
		// If a config value is a string like "${node_id}", we replace it with the first artifact from that node
		// OR the raw primitive data if it's a "Value" type artifact.
		interpolatedConfig := make(map[string]any)
		for k, v := range node.Config {
			if strVal, ok := v.(string); ok && len(strVal) > 3 && strVal[0:2] == "${" && strVal[len(strVal)-1] == '}' {
				depNodeID := strVal[2 : len(strVal)-1]
				if arts, ok := producedArtifacts[depNodeID]; ok && len(arts) > 0 {
					art := arts[0]
					if art.Kind == "Value" {
						interpolatedConfig[k] = art.Metadata["data"]
					} else {
						// Pass the Artifact object itself
						interpolatedConfig[k] = art
					}
					continue
				}
			}
			interpolatedConfig[k] = v
		}

		start := time.Now()
		outputs, err := proc.Process(ctx, interpolatedConfig, inputs)
		if err != nil {
			if ctx.Err() != nil {
				e.recordNodeCancel(runID, node.ID, ctx.Err())
				return ctx.Err()
			}
			if isPaused(err) {
				e.recordNodePause(runID, node.ID, err)
				return err
			}
			e.recordNodeFail(runID, node.ID, err)
			return fmt.Errorf("node %s failed: %w", node.ID, err)
		}
		duration := time.Since(start)

		producedArtifacts[node.ID] = outputs

		// Save resulting artifacts to Blackboard
		for _, art := range outputs {
			if err := e.blackboard.SaveArtifact(runID, node.ID, art); err != nil {
				logger.Error("Engine", fmt.Sprintf("Failed to save artifact %s: %v", art.ID, err))
			}
		}

		e.blackboard.RecordNodeCompletion(runID, node.ID, "completed", "")
		logger.Info("Engine", fmt.Sprintf("Node %s completed in %v. Produced %d artifacts.", node.ID, duration, len(outputs)))
	}

	e.blackboard.UpdateWorkflowRun(runID, "completed")
	logger.Info("Engine", fmt.Sprintf("Workflow %s completed successfully.", wf.Name))
	return nil
}

func (e *Engine) recordNodeFail(runID, nodeID string, err error) {
	e.blackboard.RecordNodeCompletion(runID, nodeID, "failed", err.Error())
	e.blackboard.UpdateWorkflowRun(runID, "failed")
	e.updateCursorTerminal(runID, nodeID, "failed")
}

func (e *Engine) recordNodeCancel(runID, nodeID string, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	e.blackboard.RecordNodeCompletion(runID, nodeID, "cancelled", msg)
	e.blackboard.CancelWorkflowRun(runID)
	e.updateCursorTerminal(runID, nodeID, "cancelled")
}

func (e *Engine) recordNodePause(runID, nodeID string, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	e.blackboard.RecordNodeCompletion(runID, nodeID, "paused", msg)
	e.blackboard.UpdateWorkflowRun(runID, "paused")
	e.updateCursorCurrent(runID, nodeID, "paused")
}

type InteractiveResult struct {
	CompletedNode string
	NextNode      string
	Outputs       []artifact.Artifact
}

// RunInteractive executes one node at a time and lets processors choose the
// next node by returning an artifact with metadata.next_node. The workflow DAG
// remains authoritative: next_node must be a direct downstream node or
// "completed".
func (e *Engine) RunInteractive(ctx context.Context, wf *Workflow, runID, startNode string) error {
	if err := e.blackboard.StartWorkflowRun(runID, wf.Name); err != nil {
		return fmt.Errorf("failed to start workflow run: %w", err)
	}
	if err := e.saveCursor(runID, wf, startNode); err != nil {
		return err
	}
	return e.runInteractive(ctx, wf, runID, startNode)
}

// ResumeInteractive resumes a paused interactive workflow from startNode. If
// startNode is empty, ArtifactFlow uses the single paused node as the cursor.
func (e *Engine) ResumeInteractive(ctx context.Context, wf *Workflow, runID, startNode string) error {
	run, err := e.blackboard.GetWorkflowRun(runID)
	if err != nil {
		return err
	}
	if run.Status != "paused" {
		return fmt.Errorf("workflow run %s is %s, not paused", runID, run.Status)
	}
	if wf == nil {
		cursor, err := e.blackboard.LoadWorkflowCursor(runID)
		if err != nil {
			return err
		}
		wf = &Workflow{}
		if err := json.Unmarshal(cursor.WorkflowJSON, wf); err != nil {
			return fmt.Errorf("failed to decode workflow cursor for %s: %w", runID, err)
		}
		if startNode == "" {
			startNode = cursor.CurrentNode
		}
	}
	if startNode == "" {
		startNode, err = e.pausedNode(runID)
		if err != nil {
			return err
		}
	}
	if err := e.blackboard.UpdateWorkflowRun(runID, "running"); err != nil {
		return err
	}
	e.updateCursorCurrent(runID, startNode, "")
	return e.runInteractive(ctx, wf, runID, startNode)
}

func (e *Engine) runInteractive(ctx context.Context, wf *Workflow, runID, startNode string) error {
	if _, err := TopologicalSort(*wf); err != nil {
		e.blackboard.UpdateWorkflowRun(runID, "failed")
		return fmt.Errorf("DAG resolution failed: %w", err)
	}

	nodes := make(map[string]Node, len(wf.Nodes))
	allowed := make(map[string]map[string]bool, len(wf.Nodes))
	for _, node := range wf.Nodes {
		nodes[node.ID] = node
		if _, ok := allowed[node.ID]; !ok {
			allowed[node.ID] = make(map[string]bool)
		}
		for _, dep := range node.DependsOn {
			if _, ok := allowed[dep]; !ok {
				allowed[dep] = make(map[string]bool)
			}
			allowed[dep][node.ID] = true
		}
	}

	current := startNode
	if current == "" && len(wf.Nodes) > 0 {
		current = wf.Nodes[0].ID
	}
	lastNode := current
	for current != "" && current != "completed" {
		node, ok := nodes[current]
		if !ok {
			e.blackboard.UpdateWorkflowRun(runID, "failed")
			return fmt.Errorf("interactive node not found: %s", current)
		}
		lastNode = current
		e.updateCursorCurrent(runID, current, "")
		result, err := e.RunInteractiveNode(ctx, runID, node)
		if err != nil {
			if ctx.Err() != nil {
				e.blackboard.CancelWorkflowRun(runID)
			}
			return err
		}
		next := result.NextNode
		if next == "" {
			e.blackboard.UpdateWorkflowRun(runID, "failed")
			return fmt.Errorf("node %s did not return metadata.next_node", current)
		}
		if next != "completed" && !allowed[current][next] {
			e.blackboard.UpdateWorkflowRun(runID, "failed")
			return fmt.Errorf("invalid next_node %s from %s", next, current)
		}
		current = next
	}
	if err := e.blackboard.UpdateWorkflowRun(runID, "completed"); err != nil {
		return err
	}
	e.updateCursorTerminal(runID, lastNode, "completed")
	return nil
}

func (e *Engine) saveCursor(runID string, wf *Workflow, startNode string) error {
	if wf == nil {
		return fmt.Errorf("workflow is required")
	}
	if startNode == "" && len(wf.Nodes) > 0 {
		startNode = wf.Nodes[0].ID
	}
	data, err := json.Marshal(wf)
	if err != nil {
		return fmt.Errorf("failed to encode workflow cursor: %w", err)
	}
	return e.blackboard.SaveWorkflowCursor(state.WorkflowCursor{
		RunID:        runID,
		WorkflowName: wf.Name,
		StartNode:    startNode,
		CurrentNode:  startNode,
		WorkflowJSON: data,
	})
}

func (e *Engine) updateCursorCurrent(runID, currentNode, terminalState string) {
	cursor, err := e.blackboard.LoadWorkflowCursor(runID)
	if err != nil {
		return
	}
	cursor.CurrentNode = currentNode
	cursor.TerminalState = terminalState
	_ = e.blackboard.SaveWorkflowCursor(*cursor)
}

func (e *Engine) updateCursorTerminal(runID, currentNode, terminalState string) {
	cursor, err := e.blackboard.LoadWorkflowCursor(runID)
	if err != nil {
		return
	}
	if currentNode != "" {
		cursor.CurrentNode = currentNode
	}
	cursor.TerminalState = terminalState
	_ = e.blackboard.SaveWorkflowCursor(*cursor)
}

func (e *Engine) pausedNode(runID string) (string, error) {
	nodes, err := e.blackboard.GetNodeRuns(runID)
	if err != nil {
		return "", err
	}
	paused := ""
	for _, node := range nodes {
		if node.Status != "paused" {
			continue
		}
		if paused != "" {
			return "", fmt.Errorf("workflow run %s has multiple paused nodes", runID)
		}
		paused = node.NodeID
	}
	if paused == "" {
		return "", fmt.Errorf("workflow run %s has no paused node", runID)
	}
	return paused, nil
}

func (e *Engine) RunInteractiveNode(ctx context.Context, runID string, node Node) (InteractiveResult, error) {
	if err := e.blackboard.RecordNodeStart(runID, node.ID); err != nil {
		return InteractiveResult{}, err
	}
	proc, err := e.registry.Get(node.Processor)
	if err != nil {
		e.recordNodeFail(runID, node.ID, err)
		return InteractiveResult{}, err
	}
	if err := e.registry.EnsureCapabilities(ctx, proc); err != nil {
		e.recordNodeFail(runID, node.ID, err)
		return InteractiveResult{}, err
	}

	inputs, err := e.loadInputs(runID, node)
	if err != nil {
		e.recordNodeFail(runID, node.ID, err)
		return InteractiveResult{}, err
	}
	outputs, err := proc.Process(ctx, node.Config, inputs)
	if err != nil {
		if ctx.Err() != nil {
			e.recordNodeCancel(runID, node.ID, ctx.Err())
			return InteractiveResult{}, ctx.Err()
		}
		if isPaused(err) {
			e.recordNodePause(runID, node.ID, err)
			return InteractiveResult{}, err
		}
		e.recordNodeFail(runID, node.ID, err)
		return InteractiveResult{}, fmt.Errorf("node %s failed: %w", node.ID, err)
	}
	for _, art := range outputs {
		if err := e.blackboard.SaveArtifact(runID, node.ID, art); err != nil {
			e.recordNodeFail(runID, node.ID, err)
			return InteractiveResult{}, err
		}
	}
	if err := e.blackboard.RecordNodeCompletion(runID, node.ID, "completed", ""); err != nil {
		return InteractiveResult{}, err
	}

	return InteractiveResult{
		CompletedNode: node.ID,
		NextNode:      nextNodeFromArtifacts(outputs),
		Outputs:       outputs,
	}, nil
}

func isPaused(err error) bool {
	if err == nil {
		return false
	}
	if pause, ok := err.(pauseError); ok {
		return pause.Paused()
	}
	return false
}

func (e *Engine) loadInputs(runID string, node Node) ([]artifact.Artifact, error) {
	var inputs []artifact.Artifact
	for _, dep := range append(node.DependsOn, node.Inputs...) {
		arts, err := e.blackboard.GetNodeArtifacts(runID, dep)
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, arts...)
	}
	return inputs, nil
}

func nextNodeFromArtifacts(outputs []artifact.Artifact) string {
	for _, art := range outputs {
		if art.Metadata == nil {
			continue
		}
		if next, ok := art.Metadata["next_node"].(string); ok {
			return next
		}
	}
	return ""
}
