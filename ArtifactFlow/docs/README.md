# ArtifactFlow Documentation

Documentation for the core workflow and state platform, organised per the
workspace documentation standard
(`EOM repository-documentation-architecture-tdd`): classify by purpose,
namespace by subject, name by document type.

## Design subjects — `design/<subject>/TDD.md`

| Subject | What it specifies |
| --- | --- |
| [blackboard](design/blackboard/TDD.md) | The persistence schema for runs, artifacts, messages, events, lessons and vectors. |
| [dag-engine](design/dag-engine/TDD.md) | Sequential and interactive DAG execution, pause and resume. |
| [ipc](design/ipc/TDD.md) | FlatBuffers IPC between Go and the sidecars. |
| [capability-manager](design/capability-manager/TDD.md) | Capability discovery and lazy sidecar lifecycle. |
| [framework](design/framework/TDD.md) | The framework boundary downstream applications build on. |
| [state](design/state/TDD.md) | SQLite state persistence. |
| [workflow](design/workflow/TDD.md) | Workflow definition and cursor semantics. |
| [python-sidecar](design/python-sidecar/TDD.md) | The Python ML sidecar boundary. |
| [rust-sidecar](design/rust-sidecar/TDD.md) | The Rust IPC sidecar boundary. |

## Reference

- [`reference/runtimes/`](reference/runtimes/) — llama.cpp, Ollama, transformers.
- [`reference/integrations/`](reference/integrations/) — VideoScriptor, CodingAgentsGO.

## Other

- [`BUILDING_ROCM_GPU.md`](BUILDING_ROCM_GPU.md) — building against ROCm.
- [`archive/okf-bundle-log.md`](archive/okf-bundle-log.md) — the retired bundle's ledger.

This tree replaced an `okf/` bundle when project OKF bundles were retired
under the workspace ADR-0003. Nothing in this repository's code referenced
the bundle, so the move was documentation-only.
