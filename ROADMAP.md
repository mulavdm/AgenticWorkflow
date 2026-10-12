---
schema: 1
id: TRACKER-AGENTIC-ROADMAP-001
type: tracker
title: "ArtifactFlow Roadmap"
status: active
authority: informative
owner: agentic-workflow
domain: AGENTIC
---
# ArtifactFlow Roadmap

This document outlines the strategic next steps for evolving the pure-code orchestration engine.

## 1. Parallel DAG Execution (Concurrency)
- **Goal**: Maximize performance by executing non-dependent graph nodes simultaneously.
- **Implementation**: Upgrade the Go `ArtifactFlow` Engine from a flat, sequential topological sort to a concurrent dispatcher. Nodes like `gen.allocate_latent` and `gen.encode_prompt` should run in parallel goroutines, dispatching their IPC commands to sidecars concurrently.

## 2. Video Generation Workflows
- **Goal**: Expand the platform's native multimodal capabilities.
- **Implementation**: Port complex video generation DAGs (e.g., Wan2.1, HunyuanVideo). This requires introducing 3D Latent Allocators, Temporal VAEs, and integrating the `VideoScriptor` application layer for handling video-specific operations and caching.

## 3. Rust Sidecar Integration [COMPLETED]
- **Goal**: Offload heavy, non-ML CPU tasks to an ultra-fast compute environment.
- **Implementation**: Fully activated and integrated the internal `rust-sidecar` compute engine. The sidecar spawns natively alongside the parent process, establishes a fast IPC named-pipe connection (`\\.\pipe\artifactflow_ipc`), and executes proxy compute tasks statelessly.

## 4. Strict DAG Validation & Linting
- **Goal**: Prevent wasted GPU compute time by failing fast on invalid workflows.
- **Implementation**: Introduce a strict DAG validation phase before execution begins. It will statically analyze the YAML to detect cyclic dependencies, broken `${node_id}` string interpolations, and potential type-mismatches across `core.switch` and upstream/downstream `Artifact` expectations.

## 5. Deep Control-Plane Integration [COMPLETED]
- **Goal**: Transition CodingAgents' internal tool-calling, validation, and approval loops entirely into ArtifactFlow's state machine.
- **Implementation**: Successfully deconstructed the monolithic Go runtime loop into five unrolled, first-class ArtifactFlow DAG nodes (`think`, `ground`, `policy`, `execute`, `route`). Every agent loop runs as a stateless node, rehydrating its context from the SQLite blackboard, supporting restart-tolerant approvals, and executing sequential step transitions.

## 6. Autonomous Self-Evolution (The `@dogfood` Loop)
- **Goal**: Enable CodingAgents to autonomously maintain, test, and implement features in both the CodingAgents and ArtifactFlow codebases.
- **Implementation**: Create a specialized `@dogfood` agent role and execution preset. Define ArtifactFlow workflows that orchestrate CodingAgents to read code, run tests, diagnose compiler/runtime errors, and apply changes in shadow workspaces before submitting verified git diffs for human promotion.

## 7. Event-Sourced Replay & Time-Travel Debugger
- **Goal**: Support absolute reproducibility and interactive debugging of multi-agent runs via full event-sourcing.
- **Implementation**: Redesign the CodingAgents runtime to reconstruct 100% of its state views from ArtifactFlow blackboard events. Build a time-travel debugger in the web dashboard, allowing developers to step through historical execution states, modify prompts/code mid-run, and branch execution timelines.

