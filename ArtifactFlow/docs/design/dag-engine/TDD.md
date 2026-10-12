# DAG Execution Engine

The core execution coordinator in ArtifactFlow represents workflow processes as Directed Acyclic Graphs (DAGs) of nodes. The engine loads these configurations, resolves dependencies, and manages the execution flow across processors.

## Execution Modes

The engine supports two primary modes of pipeline execution:

### 1. Sequential Execution (`Engine.Run`)
*   The engine parses the workflow and performs a **topological sort** to find a valid linear execution order.
*   Nodes are executed one by one. Before running a node, the engine checks the [Blackboard](../blackboard/TDD.md) database. If a node is already marked `completed` in that run ID, it is skipped and its cached artifacts are reloaded to satisfy downstream dependencies.
*   This mode is fully deterministic and backward-compatible with batch pipelines.

### 2. Interactive Execution (`Engine.RunInteractive`)
*   Executes nodes one step at a time, allowing dynamic handoffs.
*   Rather than hardcoding transitions, a node's processor can decide the next node to transition to by returning a special artifact containing `metadata.next_node` (e.g. `next_node: "another_node_id"` or `"completed"`).
*   **DAG Validation**: To prevent arbitrary jumps or infinite loops, the engine validates the handoff target against the static DAG; the target must be a declared direct downstream dependent of the current node.

## Pausing and Resuming

A processor can pause execution by returning a custom error that implements the pause contract (`Paused() bool`).
*   **State change**: The engine marks the node and workflow status as `paused` in the Blackboard, preserving the current state.
*   **Cursor persistence**: The current node pointer, starting node, and original workflow JSON definition are packed into a `WorkflowCursor` and saved in the Blackboard's `configs` table.
*   **Resumption (`ResumeInteractive`)**: Callers can resume execution by invoking `ResumeInteractive` with the run ID. If the caller passed `nil` for the workflow definition, the engine automatically reloads the serialized cursor configuration from the Blackboard to rebuild state and restart execution from the paused node.

See also: [Workflow Package Component](../workflow/TDD.md), [Blackboard Persistence](../blackboard/TDD.md)
