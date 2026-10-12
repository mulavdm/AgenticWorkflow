# Implementation Plan: Shared Workspace Architecture

Transition the framework from a linear chat history model to an **Atomic Decomposition** model where agents operate in isolation on shared artifacts.

## User Review Required

> [!IMPORTANT]
> This is a structural refactor of the `internal/orchestrator` and `internal/types` packages. It will change how history is passed to LLM providers.

> [!WARNING]
> Existing long-running sessions might be incompatible with the new state format during the transition.

## Proposed Changes

### [Component] Core Types & State
#### [MODIFY] [types.go](internal/types/types.go)
- Add an `Artifacts` struct to `CouncilState`:
  ```go
  type Artifacts struct {
      ProjectMap   string // Structural overview
      TechnicalPlan string // Current implementation plan
      ExecutionLogs string // Summary of tool outputs
  }
  ```
- Add a `WorkspaceMode` flag to enable/disable this behavior during transition.

### [Component] Orchestrator (The "Materializer")
#### [MODIFY] [coordinator.go](internal/orchestrator/coordinator.go)
- Implement `MaterializeArtifacts()`: A function that scans the `Events` log and populates the `Artifacts` struct.
- Update the `Transition` logic: When an agent finishes, the orchestrator parses its final response to update the relevant artifact (e.g., extracting the plan from the Planner).

### [Component] Agent Provider Interface
#### [MODIFY] [provider.go](internal/agent/provider.go)
- Modify the history builder to be role-aware.
- **Analyst Context**: User Request + Root Directory Listing.
- **Planner Context**: User Request + Project Map Artifact.
- **Coder Context**: Technical Plan Artifact + Target File Content.
- **Critic Context**: Technical Plan + Execution Logs.
- **Security Context**: Technical Plan + Surgical Patch + Execution Logs.
- **Synthesizer Context**: User Request + Final Execution Logs.

### [Component] Agent Constitutions
#### [MODIFY] [analyst/constitution.md](internal/agent/analyst/constitution.md)
#### [MODIFY] [planner/constitution.md](internal/agent/planner/constitution.md)
#### [MODIFY] [critic/constitution.md](internal/agent/critic/constitution.md)
#### [MODIFY] [security/constitution.md](internal/agent/security/constitution.md)
- Update directives to explicitly mention that their output will be used to update the "Shared Workspace."
- Mandate structured tags (e.g., `[PROJECT MAP]`, `[TECHNICAL PLAN]`, `[CRITIQUE]`, `[SECURITY REPORT]`) for easier parsing by the Go runtime.

## Verification Plan

### Automated Tests
- `go test ./internal/orchestrator/...`: Verify that `MaterializeArtifacts` correctly updates state from a mock event log.
- `go test ./internal/agent/...`: Verify that the history builder produces the correct "isolated" context for each role.

### Manual Verification
- Run a full cycle (`Analyst` -> `Planner` -> `Coder`) and inspect the logs to confirm that the Coder *only* received the Plan and not the raw Analyst reasoning.
