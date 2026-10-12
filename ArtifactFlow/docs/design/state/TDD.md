# State Package

The `state` package (`pkg/state`) implements SQL query boundaries over the local SQLite database. It wraps database operations inside the `Blackboard` struct to provide thread-safe reads and writes using a synchronization mutex.

## Files and Structs

Defined in [blackboard.go](../../../pkg/state/blackboard.go), the package maps database rows into Go structures:

*   **`Blackboard`**: Controls the `*sql.DB` connection and provides mutual exclusion locks (`mu sync.Mutex`) for writes.
*   **`WorkflowRun`**: Represens rows in `workflow_runs`.
*   **`NodeRun`**: Represents rows in `node_runs`.
*   **`WorkflowCursor`**: Stores interactive run checkpoints (`RunID`, `WorkflowName`, `CurrentNode`, `WorkflowJSON`).
*   **`Message` / `Event`**: Models conversational prompts and system state updates.
*   **`TimelineEntry`**: A unified structure representing an event, message, or artifact creation at a specific point in time.

## Key Functions

### 1. Run & Node Lifecycle APIs
*   `StartWorkflowRun(runID, name string) error` / `UpdateWorkflowRun(runID, status string) error`
*   `RecordNodeStart(runID, nodeID string) error` / `RecordNodeCompletion(runID, nodeID, status, errorMsg string) error`
*   `GetNodeStatus(runID, nodeID string) (string, error)` (checks if a node in a run is `completed`, `running`, or `failed`).

### 2. Interaction Cursor APIs
*   `SaveWorkflowCursor(cursor WorkflowCursor) error` / `LoadWorkflowCursor(runID string) (*WorkflowCursor, error)`
    These APIs serialize and save/restore the execution cursor to let the engine resume interactive workflows.

### 3. Data Entry APIs
*   `SaveArtifact(runID, nodeID string, art artifact.Artifact) error`
*   `SaveMessage(msg Message) error` / `SaveEvent(evt Event) error`
*   `SaveVector(entry VectorEntry) error` (inserts embeddings to perform semantic lookups).
*   `SaveLesson(lesson Lesson) error` (persists engineering critique insights for learning reinforcement).

### 4. Replay & Timeline Construction
*   `GetTimeline(runID string) ([]TimelineEntry, error)`: Merges messages, events, and artifacts produced in a run, sorting them chronologically.
*   `ReconstructRun(runID string) (*RunSnapshot, error)`: Aggregates the timeline, nodes history, configuration context, and messages list into a single snapshot object to replay executions.

See also: [Blackboard Architecture Concept](../blackboard/TDD.md), [DAG Engine](../dag-engine/TDD.md)
