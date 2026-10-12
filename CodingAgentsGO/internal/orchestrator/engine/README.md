# Orchestrator Engine

The `engine` package provides the CodingAgents role-phase runtime. ArtifactFlow owns workflow-level execution and validates dynamic DAG handoffs; this package owns the behavior inside one CodingAgents role invocation.

## Responsibilities

- **Phase Runtime**: Executes a single CodingAgents role against the current materialized `CouncilState`.
- **Phase Execution**: Manages the lifecycle of a single phase, including retries and communication with agents.
- **Handoff Parsing**: Extracts `[NEXT: role]` tags from agent responses. ArtifactFlow validates the resulting dynamic handoff against the workflow DAG.
- **Validation Integration**: Interacts with the `grounding` package to verify tool calls and grounding safety before advancing the state.

## Key Types

- **`Runtime`**: The main execution engine. It coordinates with a `Grounder` to ensure each step is valid and grounded in reality.
- **`Agent`**: A consumer-defined interface that allows the engine to interact with any reasoning entity (local or cloud-based).

## Logic Flow

1. **Invoke Agent**: Send the current state and instructions to the active agent.
2. **Validate**: Pass the response to the `Grounder`.
3. **Execute Tools**: If a tool call is detected and valid, execute it and return an `ErrToolExecuted` signal to re-prompt the agent.
4. **Handoff**: Once a valid final response is received, parse the next role and transition the state machine.
