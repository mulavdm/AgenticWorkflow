# Swarm Consensus

The `consensus` package handles multi-agent parallel reasoning and contradiction resolution. It allows the framework to spawn a "Council" of agents to tackle complex tasks with higher reliability.

## Responsibilities

- **Parallel Execution**: Spawns multiple concurrent agents (Goroutines) to process the same prompt independently.
- **State Isolation**: Provides each swarm agent with a thread-safe `Clone()` of the session state to prevent cross-contamination during deliberation.
- **Consensus Resolution**: Uses a specialized `Synthesizer` role to review all parallel outputs, resolve contradictions, and produce a single, high-confidence unified response.

## Key Types

- **`Engine`**: Orchestrates the swarm lifecycle.

## Why Use Consensus?

Agentic systems often suffer from stochastic failures or "lucky" successes. By running multiple agents in parallel and synthesizing their results, the framework moves from **probabilistic success** to **deterministic verification**. This is particularly useful for complex architectural planning or critical security reviews.
