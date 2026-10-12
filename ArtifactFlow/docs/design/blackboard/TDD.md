# Blackboard State Persistence

ArtifactFlow utilizes a **Blackboard** pattern to centralize all execution state, runs history, node status, and intermediate artifacts. Rather than having nodes pass complex local memory references or requiring downstream applications to maintain parallel execution tracking databases, everything is written to a shared SQLite store (`blackboard.db`).

## Database Schema

The SQLite schema initializes the following tables:

*   **`workflow_runs`**: Tracks overall pipeline status (`running`, `paused`, `completed`, `cancelled`, `failed`) and lifecycle timestamps.
*   **`node_runs`**: Records individual node executions within a run, storing their status, execution duration, and error logs if failed.
*   **`artifacts`**: Stores the output data from completed nodes. Each artifact has a unique ID, kind, URI, MIME type, and JSON metadata.
*   **`messages`**: Captures LLM inputs/outputs (visible messages, roles, and token usages) to support auditing and history generation.
*   **`run_events`**: Logs internal pipeline event payloads to reconstruct executing timelines.
*   **`configs`**: A namespace/key/value table storing execution configs, including interactive workflow cursors.
*   **`lessons`**: Stores task critique and resolution steps, which allows learning from past execution failures.
*   **`vectors`**: Contains semantic embeddings (`dims` and `vector_json`) linked to targets to facilitate vector-based semantic search.
*   **`schema_versions`**: Records the active Blackboard schema version so future migrations can be applied idempotently.

The Blackboard records schema version `1` after initialization and exposes `SchemaVersion` and `Migrate` APIs. The initial migration set creates indexes for common run, node, artifact, event, message, and vector queries.

## Vector Queries

Vector records remain stored in SQLite as JSON-backed local state. Production callers should prefer `QueryVectors` over unfiltered `GetVectors` when they can constrain the search by run id, vector dimensions, source path prefix, embedding model id, or content kind such as `code` versus `memory`. This keeps downstream search behavior bounded while preserving the older compatibility API.

## Replay and Determinism

By querying the Blackboard through Go APIs like `ReconstructRun` and `GetTimeline`, the orchestrator can construct a unified time-sorted log of all messages, events, and artifacts produced during execution. This allows:
1.  **Run inspection**: Rendering downstream dashboards with real-time progress.
2.  **Run replay**: Deterministically reconstructing past runs from their persisted snapshots.
3.  **Resume executions**: Safely skipping previously completed nodes during a retry.

See also: [State package component](../state/TDD.md), [DAG Engine](../dag-engine/TDD.md)
