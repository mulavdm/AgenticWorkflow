# ArtifactFlow

> **Archived. This project is not actively developed.**
>
> ArtifactFlow was an experiment, built between June and October 2026, to see
> whether one small Go platform could own everything a local agentic or
> multimodal application needs in order to run a workflow:
>
> - executing a workflow as a DAG of processors, in order or one step at a
>   time with the processors choosing the next step;
> - keeping every run on a SQLite "blackboard", so a run can be inspected,
>   paused for a person, resumed and replayed;
> - starting and supervising the model runtimes the processors need (a Rust
>   sidecar over FlatBuffers IPC, a Python sidecar, llama.cpp and
>   whisper.cpp), so that an application never manages a process itself.
>
> It is published as it stood, for reading. Issues and pull requests are not
> watched. Three things a reader should know:
>
> - **It builds from the repository root, not on its own.** The Go code
>   imports the small logging module in [`../CentralLogger`](../CentralLogger),
>   which the root's `go.work` resolves.
> - **It was developed on one Windows machine with an AMD GPU.** The ROCm
>   build notes name its `gfx1101` card. The fallbacks that named that
>   machine's own paths were removed for publication, and the whisper.cpp
>   call now reads its two paths from the environment; nothing else in the
>   code differs from what ran.
> - **Its two consumers sit beside it.** [`../VideoScriptor`](../VideoScriptor)
>   and [`../CodingAgentsGO`](../CodingAgentsGO) were built on it, and
>   `docs/reference/integrations/` describes how they used it.

ArtifactFlow is the workflow and state platform for agentic and multimodal applications in the workspace it was built in. Downstream projects register domain processors and request capabilities through ArtifactFlow; they do not own workflow execution, blackboard state, or sidecar lifecycle.

## Core Model

- Workflows are DAGs of nodes.
- Nodes execute processors.
- Processors consume and produce artifacts.
- The blackboard owns runs, node status, interactive workflow cursors, artifacts, messages, events, lessons, vectors, and config.
- Run APIs cover start, update, cancel, list, timeline query, node status query, and deterministic snapshot reconstruction.
- Capabilities describe runtime requirements such as Rust, Python, or C++ sidecar-backed processors and expose status snapshots for downstream health panels.

## Execution Modes

- `Engine.Run` executes a workflow sequentially in topological order. This path remains compatible with existing media/workflow consumers.
- `Engine.RunInteractive` executes one node at a time and lets processors return `metadata.next_node`. ArtifactFlow validates that dynamic handoff against the workflow DAG.
- Processors can return a paused error contract when downstream policy requires human intervention. ArtifactFlow records the node and workflow as `paused` without importing downstream domain types.
- `ResumeInteractive` resumes a paused interactive workflow from its paused node, preserving DAG handoff validation. Callers may pass the workflow in memory as before, or pass `nil` so ArtifactFlow reloads the persisted workflow cursor and definition from the blackboard.

## Capability Ownership

ArtifactFlow owns capability and sidecar lifecycle. Downstream applications request processors or capabilities through the public Go API. ArtifactFlow lazily starts or connects required sidecars when a processor declares a capability requirement.

`LocalCapabilityManager` exposes status snapshots through `CapabilityStatus` and `CapabilityStatuses`, and `Registry` forwards those read-only snapshots when its capability manager supports them. Status values are `registered`, `missing`, `starting`, `ready`, `failed`, and `cancelled`. These APIs are inspection-only; downstream applications should not start or stop sidecars directly.

CodingAgentsGO uses this model for agent role workflows: it registers agent processors, while ArtifactFlow owns the run, messages, events, artifacts, and routing.

Capability status snapshots may include runtime metadata. The local llama.cpp capability reports endpoint URL, model path/name, context size, flash-attention mode, and GPU offload setting so downstream health panels can display effective runtime settings without taking over lifecycle management.

## Run Inspection

The blackboard exposes canonical run inspection helpers:

- `ListWorkflowRuns`
- `GetWorkflowRun`
- `GetNodeRuns`
- `GetTimeline`
- `ReconstructRun`
- `CancelWorkflowRun`
- `ResumeInteractive`
- `SaveWorkflowCursor` / `LoadWorkflowCursor`

Downstream applications should use these APIs for run history and replay instead of maintaining parallel runtime databases.

## Local llama.cpp Runtime

Local GGUF model execution is treated as an ArtifactFlow-owned runtime dependency. The fresh upstream checkout lives under ignored local state:

- source: `bin/llama.cpp/`
- ROCm build: `bin/llama.cpp/build-gfx1101-vs2022/`
- models: `models/`
- smoke/server logs: `logs/`

Do not commit llama.cpp sources, build products, models, or logs. The ignored `bin/go.mod` boundary is local housekeeping so `go test ./...` for ArtifactFlow does not descend into external tool checkouts.

The default local dogfood llama.cpp config uses an explicit `24576` token context window, `--flash-attn auto`, and `-ngl 999`. Keep context size explicit because small reasoning models can spend several thousand tokens on thinking before producing useful tool or handoff output.

Sidecars, models and llama.cpp binaries are found beside the executable, through configured environment paths, or in AppData model directories; no absolute development path is compiled in. Audio transcription runs whisper.cpp's `whisper-cli`, named by `WHISPER_CPP_CLI`, with the model named by `WHISPER_CPP_MODEL`. Production installs should resolve assets from the installation directory, configured environment paths, or AppData model directories. When `LLAMACPP_MODEL_PATH` points at one model family directory, ArtifactFlow also searches that directory's parent so sibling GGUF models, such as dedicated embedding models, remain resolvable.

See `docs/BUILDING_ROCM_GPU.md` for the ROCm 7.1 / `gfx1101` build and smoke-test workflow.

## Runtime Artifacts

ArtifactFlow creates local state during tests and development. These should stay untracked:

- `*.db`
- `*.log`
- `models/`
- `output/`
- `bin/`
- sidecar build caches and virtual environments

## Documentation

The design documents are under [`docs/`](docs/README.md), one folder per
subject:

- [`docs/design/`](docs/design/) has a `TDD.md` for each of the blackboard,
  the DAG engine, IPC, the capability manager, the framework boundary, state,
  workflow, and the Python and Rust sidecars.
- [`docs/reference/runtimes/`](docs/reference/runtimes/) covers llama.cpp,
  Ollama and transformers.
- [`docs/reference/integrations/`](docs/reference/integrations/) describes the
  two applications that consumed the platform.
- [`docs/BUILDING_ROCM_GPU.md`](docs/BUILDING_ROCM_GPU.md) is the ROCm build
  of llama.cpp that the local runtime used.

`AGENTS.md`, `CLAUDE.md` and `GEMINI.md` are the instructions the coding
agents working on this repository were given. They refer to documents of the
private workspace it was developed in, which are not published.

## Validation

```bash
go test ./...
go build ./...
```

## License

Licensed under either of

- the Apache License, Version 2.0 ([LICENSE-APACHE](../LICENSE-APACHE) or
  <http://www.apache.org/licenses/LICENSE-2.0>), or
- the MIT license ([LICENSE-MIT](../LICENSE-MIT) or
  <http://opensource.org/licenses/MIT>),

at your option.

The generated FlatBuffers code under `pkg/ipc/artifactflow/` and in
`rust-sidecar/src/ipc/workflow_generated.rs` was produced by the FlatBuffers
compiler from [`schema/workflow.fbs`](schema/workflow.fbs).
