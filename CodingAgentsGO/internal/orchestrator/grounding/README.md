# Grounding & Safety

The `grounding` package implements the "Iron Hand" of the framework. It ensures that all agent actions are secure, grounded in the actual filesystem state, and compliant with protocol standards.

## Responsibilities

- **Grounding Gate**: Prevents agents from "hallucinating" file paths. Every path used in a tool call must have been previously observed in a system `[OBSERVATION]`.
- **Tool Validation**: Parses and verifies the syntax of `[TOOL: ...]` blocks.
- **RBAC (Role-Based Access Control)**: Restricts tool usage based on the agent's role (e.g., the `analyst` is read-only).
- **Anti-Hallucination**: Detects and blocks attempts by agents to simulate system responses or tool outputs.
- **Protocol Enforcement**: Strips history markers and ensures agents follow the reasoning-before-action protocol.

## Key Types

- **`Guard`**: The primary validator. It inspects every agent response before it is committed to the shared state.

## Grounding Logic

The `Guard` maintains a semantic history of all directory listings and file reads. If an agent attempts to access a path that hasn't been "discovered" yet, the `Guard` rejects the call and provides a diagnostic hint (e.g., "Use explorer:list on the parent directory first").
