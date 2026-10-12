---
schema: 1
id: AGENTS-AGENTIC-ROOT-001
type: agent-instructions
title: "Agent Instructions"
status: active
authority: governing
owner: agentic-workflow
domain: AGENTIC
accepted:
  by: owner
  date: 2026-07-16
---
# Agent Instructions

This repository contains multiple software projects. Agents working here should first inspect the relevant project directory, then follow the local conventions they find there.

## Read First

- Read `SOFTWARE_ENGINEERING_PRINCIPLES.md` for the general engineering principles expected in this workspace.
- Check the target project for its own README, docs, tests, build scripts, style configuration, and package or module files.
- If a nested `AGENTS.md` exists in a project directory, follow the more specific instructions there for files under that directory.

## Universal Engineering Principles

- **Strict Scope Control**: Keep changes strictly scoped to the user's request. Do not execute gratuitous refactoring or rewrite unrelated code while fixing a specific issue.
- **Contextual Blending**: Before writing any code, read all relevant files. Your changes must blend in with the surrounding codebase's existing patterns.
- **Safe Operations**: Preserve user changes and avoid destructive git or filesystem operations unless explicitly requested.
- **Anti-Indirection**: Prefer straightforward modules, simple functions, plain data structures, and native types over heavy indirection or framework-heavy abstractions.
- **Minimal Dependencies**: Start with the standard library of the language in use. Avoid tiny helper dependencies for trivial code (no micro-packages). Reject dependencies that force the repo toward an unwanted framework shape.
- **Clear Data Ownership**: Keep data flow, ownership, lifecycle, and side effects explicit and easy to follow.
- **Immutability**: Treat shared state and blackboards as immutable once published. Produce new data rather than mutating in place.
- **Measure Everything**: Always implement profiling, benchmarking, and maintain zero/low-allocation routing in hot paths. Measure before merging.
- **Security by Design**: Incorporate defensive engineering, red-team testing, or attack vector simulations for any newly added feature.
- **Robust Lifecycles**: Do not block routing threads. Actively manage child sidecars—poll for health, use timeouts, and gracefully clean up orphaned background processes on crashes.
- **Strict Locking Discipline**: Use language-appropriate read-write locks correctly—prefer read locks for reads, and isolate write locks strictly to mutation operations.
- **Atomic Updates**: When APIs, configuration schemas, or behaviors change, update the relevant documentation (`README.md`, OKF concepts in `okf/` folders, or architecture docs) in the exact same commit. Documentation is part of the code.
- **Truthful & Living Documentation**: Treat architecture documents and OKF concepts as living specifications. Remove stale references to deprecated features or deleted projects. Do not turn brainstorms or speculative plans into present-tense guarantees.
- **Proximity of Information**: Keep implementation details near the code or project it describes. Do not stuff specific technical workflows into global workspace documents.
- **Root Versus Project Scope**: Keep workspace-level documents strictly generic. Project-specific architecture, workflow, and implementation guidance belongs exclusively in the relevant project directory.
- **Targeted Validation**: Always run the most relevant formatter, linter, and targeted tests for the specific module you are changing. Clearly report any verification that could not be run.
- **Human Accountability**: A pull request is a long-term commitment. AI must act purely as an assistant. Do not generate invasive subsystems the human cannot explain or maintain.
- **No Autonomous Overreach**: Agents must not unilaterally commit, push, or open Pull Requests on the user's behalf without explicit direction.
- **Professional Interfaces**: Maintain a professional, accessible, and vanilla visual theme. Avoid over-the-top styling (like "cyberpunk" or heavy neon aesthetics). Prioritize clean typography and functionality.


## Repository Notes

- The root may contain several independent projects rather than one single application.
- Do not assume one project's tooling applies to another.
- Prefer project-local commands from the target directory.
- Keep shared workspace-level documents generic unless the user asks for project-specific policy.

## OS & Infrastructure Nuances

- **PowerShell Execution**: When running PowerShell scripts (`.ps1`) for build automation on Windows, you will often encounter Execution Policy restrictions. Use `-ExecutionPolicy Bypass` when invoking scripts (e.g., `powershell -ExecutionPolicy Bypass -File .\build.ps1`).
- **Windows Named Pipes**: When building cross-language IPC on Windows, be mindful of path formatting. Go's standard `net` library requires external packages (like `npipe` or `go-winio`) to dial `\\.\pipe\...` correctly. In Rust, libraries like `interprocess` may automatically namespace pipe names, so passing the fully qualified path can cause double-escaping bugs.
- **Orphaned Background Processes**: When testing applications that spawn background daemons (like the `rust-sidecar`), aborted test runs can leave orphaned processes holding locks on IPC named pipes or SQLite databases. If you encounter "Access is denied. (os error 5)" or "UNIQUE constraint failed", run `Stop-Process -Name rust-sidecar -Force -ErrorAction SilentlyContinue` and delete the SQLite `.db` files before trying again.
- **Go Workspaces**: The repository uses `go.work` to manage local module dependencies. When a downstream application (like `VideoScriptor`) imports `github.com/ArtifactFlow`, it relies on the local source. You can co-develop both the framework and the downstream app simultaneously without needing to mock version bumps or push to a remote repository.
- **Mocking System Binaries**: If testing requires system dependencies like `ffmpeg`, you can easily write a mocked Go program (e.g. `package main; func main() { println("Mock") }`) and compile it to the workspace's `bin\ffmpeg.exe`. Ensure the `bin\` folder is added to `$env:Path` during tests.
- **PyTorch & DirectML on Windows**: When running local ML sidecars on AMD/Windows using `torch-directml`, the PyTorch version is heavily pinned (often to legacy versions like `2.2.2`). This triggers security restrictions in newer `transformers` versions (like blocking `.bin` files due to CVE-2025-32434). Always explicitly enforce `use_safetensors=True` when loading HuggingFace checkpoints to bypass legacy `torch.load` restrictions.
- **External C++ CLI Orchestration**: When launching tools like `llama.cpp` or `whisper.cpp` via `os/exec` in Go, always set `cmd.Dir` to the executable's folder and pass absolute paths to `.bin` or `.gguf` weight files. These binaries often rely on relative paths to resolve their contexts and will crash cryptically if invoked from a different working directory.
- **DAG State & Blackboard Caching**: The `ArtifactFlow` orchestrator aggressively caches node artifacts in SQLite (`videoscriptor.db`). If you modify an ML processor or upstream logic and need to test it, you *must* delete the local database file first; otherwise, the engine will instantly skip execution and serve the cached state.
