# Workflow Package

The `workflow` package (`pkg/workflow`) defines the data structures and execution logic for workflow pipelines. It validates workflow definitions, handles dependency sorting, and coordinates node execution.

## Files and Structures

*   **`dag.go`**: Implements topological sorting for sequential pipeline graphs.
*   **`engine.go`**: Implements the execution coordinator (`Engine`), handling both sequential and interactive modes.
*   **`loader.go`**: Loads workflows from YAML descriptions.

```go
type Workflow struct {
    Name  string `yaml:"name"`
    Nodes []Node `yaml:"nodes"`
}

type Node struct {
    ID         string         `yaml:"id"`
    Processor  string         `yaml:"processor"`
    Config     map[string]any `yaml:"config,omitempty"`
    Inputs     []string       `yaml:"inputs,omitempty"`
    DependsOn  []string       `yaml:"depends_on,omitempty"`
}
```

## Key Engine Implementation Details

### Dependency Sorting
The topological sorting in `dag.go` detects circular dependencies within the DAG. If a cycle is detected, execution aborts immediately to prevent infinite execution loops.

### Sequential Run (`Engine.Run`)
1.  Performs topological sorting.
2.  Iterates over sorted nodes.
3.  Checks if the node's status in the Blackboard database is `completed`. If so, skips execution and loads its cached outputs.
4.  Interpolates configuration values (e.g. replacing `${some_node}` expressions with artifact references).
5.  Retrieves processor capabilities and triggers lazy sidecar startup.
6.  Executes the processor, saves produced artifacts to the Blackboard, and records completion state.

### Interactive Run (`Engine.RunInteractive` / `runInteractive`)
1.  Initiates execution and registers a cursor checkpoint (`SaveWorkflowCursor`).
2.  Executes the active node (`RunInteractiveNode`).
3.  Reads outputs to find a `metadata.next_node` setting.
4.  Ensures that the returned next node is a direct downstream node declared in the workflow DAG.
5.  Advances the cursor to the next node and loops until the process reaches the `"completed"` endpoint.

### Run Resumption (`ResumeInteractive`)
If an execution was suspended due to a `Paused` error:
1.  The engine checks if a workflow model is passed in memory.
2.  If none is passed, it calls the Blackboard to deserialize the `WorkflowCursor` config.
3.  It updates the run state to `running` and resumes the loop starting from the paused node.

See also: [DAG Engine Concept](../dag-engine/TDD.md), [Blackboard Persistence](../blackboard/TDD.md)
