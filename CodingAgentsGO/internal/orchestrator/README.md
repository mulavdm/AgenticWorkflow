# Orchestrator

The `orchestrator` package is the central coordination hub of the **CodingAgentsGO** framework. It adapts CodingAgents role agents, tool policy, and prompts onto ArtifactFlow-owned workflow execution and persisted run state.

## Architecture

The orchestrator follows a modular design to separate execution, safety, and consensus:

- **[Coordinator](coordinator.go)**: The top-level facade that composes the specialized engines.
- **[ArtifactFlow Runtime Bridge](artifactflow_runtime.go)**: Registers CodingAgents role processors, compiles role graphs into ArtifactFlow workflows, and persists run snapshots through the ArtifactFlow blackboard.
- **[Engine](engine/)**: The state machine and phase execution runtime.
- **[Grounding](grounding/)**: Safety, RBAC, and path grounding (The "Iron Hand").
- **[Consensus](consensus/)**: Parallel reasoning and swarm resolution.
- **[Infrastructure](infrastructure.go)**: Workspace isolation (Shadow Mode) and resource management.

## Philosophy: Determinism vs. Probability

Unlike traditional LLM frameworks where the agent owns the loop, here **the runtime owns the architecture**. ArtifactFlow owns workflow execution and run persistence; CodingAgents role agents are bounded processors that operate within the constraints enforced by the `Grounding Guard`.

### Observability & State Integrity

To maintain deterministic control, the orchestrator uses:

1. **TraceID**: Every request lifecycle is assigned a unique `TraceID`. This ID anchors messages, tool calls, and logs to a single execution thread.
2. **ArtifactFlow Blackboard**: Messages, events, artifacts, lessons, vectors, run status, and node status are persisted through ArtifactFlow.
3. **Materialized CouncilState**: `CouncilState` remains the in-process view used by existing agent and tool code. Full reconstruction of every runtime view from persisted ArtifactFlow events is still partial.

## Modularization

This package is modularized according to idiomatic Go principles:
1. **Shared Types**: Domain objects (`CouncilState`, `Role`, `AgentResponse`) live in `internal/types`.
2. **Encapsulation**: Each sub-package owns a specific responsibility and is decoupled from the `Coordinator` struct.
3. **Interface-Driven**: Components interact via small, focused interfaces, enabling easier testing and modification.
