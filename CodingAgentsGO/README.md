# CodingAgentsGO

CodingAgentsGO is a Go-based agentic layer for coordinating specialist LLM agents around software engineering tasks. ArtifactFlow owns workflow execution, run lifecycle, blackboard state, artifacts, messages, events, lessons, vectors, and config persistence; CodingAgentsGO contributes agents, prompts, provider adapters, tool policy, and dashboard behavior.

The project is currently in a stabilization phase. Core orchestration, provider switching, filesystem tools, session logs, SQLite-backed lessons, a local vector index, and a dashboard exist today. Some architecture notes describe planned capabilities that are not yet fully enforced.

### Implemented Today

- **ArtifactFlow-backed interactive workflow execution** for specialist roles: analyst/context, planner, critic, red team, coder, and synthesizer.
- **Topological Unrolling**: Agent phases are compiled into granular DAG nodes (`think -> ground -> policy -> execute -> route`) unrolled statelessly over the ArtifactFlow event-driven blackboard.
- **Provider selection** for Ollama, LM Studio, and OpenAI-compatible APIs through environment defaults or prompt tags.
- **Tool protocol** using `[TOOL: name, ARGS: {...}]` and explicit `[NEXT: role]` handoffs.
- **Grounding guard** for tool syntax, basic role restrictions, path discovery, repeated tool calls, and simulated observation detection.
- **Shared workspace artifacts** for project maps, technical plans, execution logs, critique, and security reports.
- **ArtifactFlow-backed lesson and vector storage** for semantic message lookup.
- **Durable run lifecycle** over HTTP, including run history, timeline, snapshot, and cancellation by run id.
- **Approval-gated tool policy** for privileged tool requests, with durable ArtifactFlow policy, approval request, and approval decision events.
- **Restart-tolerant approval resume** for persisted paused runs.
- **Direct writer calls** checked against declared task scope.
- **Persisted conservative tool policy** in `agents_config.json`, including coderunner approval threshold and safe writer prefixes.
- **Provider probes** for Ollama, LM Studio, and OpenAI-compatible endpoints, including capability metadata.
- **Local health endpoints** for logger diagnostics, blackboard schema version, ArtifactFlow capability snapshots, and indexer status.
- **Web dashboard** with Server-Sent Events, live transcript, run workbench, approval queue, provider probe panel, memory view, model discovery, agent configuration, and visual graph input.
- **Shadow workspace mode** through the `@shadow` tag.
- **Automated Sidecar Orchestration**: Automatically registers, spawns, and supervises local `rust-sidecar.exe` (compute engine) and ROCm-accelerated `llama-server.exe` (GGUF model runner) sidecars dynamically on demand, utilizing Windows Job Objects for guaranteed clean subprocess termination.

## Global Installation & Windows Deployment

The project includes a robust PowerShell installer at the workspace root to deploy the application globally, bypassing kernel-level Windows Defender Application Control (WDAC) or AppLocker binary execution blocks.

### Installation Script
Run the installer from the workspace root:
```powershell
powershell -ExecutionPolicy Bypass -File .\install.ps1
```

The installer performs the following steps:
1. **Compilation**: Compiles the Go production binary with optimizations (`-s -w`).
2. **Sidecar Deployment**: Copies `rust-sidecar.exe` from the workspace `bin/` directory to the global program folder, ensuring the compute engine is resolved and loaded out-of-the-box.
3. **Application Icon**: Deploys a custom premium application icon (`app_icon.ico`) to the program directory.
4. **Target Deployment**: Deploys the compiled executable, prompts, web assets, internal constitutions, and migrates your development `.env` configuration to the global folder:
   `%LOCALAPPDATA%\Programs\CodingAgentsGO`
5. **Command Aliasing**: Creates batch scripts (`ca.bat` and `codingagents.bat`) in the installation directory to register short, convenient terminal commands.
6. **Shell Integration**: Appends the installation path to the User's `Path` environment variable, enabling global access from any fresh terminal.
7. **Shortcuts**: Registers Start Menu and Desktop shortcuts, fully decorated with the custom application icon.
8. **Optional Code-Signing**: If run from an Elevated (Admin) shell, the installer generates a self-signed code-signing certificate, imports it into the local machine's **Trusted Root** and **Trusted Publishers** stores, and signs the compiled binary, allowing execution from any custom development path.

### Single Source of Truth
When executed globally, the application uses a single source of truth:
*   **Database**: Stores all run histories, blackboards, messages, and memories in a unified global SQLite database:
    `%APPDATA%\CodingAgentsGO\artifactflow_agents.db`
*   **Concurrence**: Both the interactive CLI loop and the Web Dashboard server (`http://localhost:8080`) run inside the same Go process, sharing the exact same `Coordinator` memory, event hub, and SQLite database.
*   **Local Override**: Set the environment variable `CODINGAGENTS_LOCAL_DB=1` to fall back to a local `artifactflow_agents.db` in the current working directory for project-specific isolation.

## Configuration

The runtime reads `.env` or process environment variables:

```text
ORCHESTRATOR_MODE=llamacpp|local|lmstudio|cloud
LOCAL_OLLAMA_MODEL=...
LOCAL_OLLAMA_URL=http://localhost:11434
LMSTUDIO_MODEL=...
LMSTUDIO_URL=http://127.0.0.1:1234
OPENAI_MODEL=...
OPENAI_URL=https://api.openai.com/v1/chat/completions
OPENAI_API_KEY=...
CODINGAGENTS_DISABLE_PROVIDER_FALLBACKS=1 # optional, force the configured provider only
CODINGAGENTS_DEV_WORKSPACE_FALLBACK=1 # optional development-only absolute workspace fallback
```

Prompt tags override defaults:

- `@llamacpp`, `@local`, `@lmstudio`, `@cloud`
- `@provider:model-id`, for example `@llamacpp:Gemma-4-E4B-Q8_K_P.gguf` or `@lmstudio:local-model`
- `@analyst` / `@context`, `@planner`, `@critic`, `@security` / `@redteam`, `@coder`, and `@synthesizer` for targeted role execution
- `@shadow` for isolated workspace execution
- `@council` for consensus mode
- `@legacy` or `@linear` to disable shared workspace artifact mode

## Local llama.cpp Sidecar Management

The framework automatically manages the local `llama.cpp` sidecar. When a local GGUF model (e.g., `Gemma-4-E4B-Q8_K_P.gguf`) is requested:
1. The framework registers the model in its local Capability Manager.
2. It automatically locates `llama-server.exe` and the model weights by searching candidate directories or using `LLAMACPP_BIN_PATH` and `LLAMACPP_MODEL_PATH` environment overrides.
3. It spawns the server on a free port, configures the context size (defaulting to `24576` for reasoning models), enables GPU layers, and connects the orchestrator to the dynamic endpoint.
4. Upon exiting the main application, the server process is automatically terminated via a supervised Job Object.

## Codebase Indexing & Semantic Search (RAG)

The framework features built-in semantic search capabilities to help agents locate codebase symbols, methods, and functions without manual filesystem traversal. This path is implemented and stabilizing; large-repository performance should be validated with project-specific benchmarks before treating it as a fully tuned production index.

1. **Incremental Indexing**: On startup, a background indexer scans the codebase. It calculates a SHA-256 hash of each source file. If a file hasn't changed, the indexer skips walking, reading, parsing, and embedding generation entirely, completing the startup check in milliseconds. If a file is new or modified, the old vector chunks are deleted (`DeletePrefix`) and re-indexed.
2. **AST-based Chunking**: For Go code, the indexer uses Go's native AST parser to logically chunk source files at struct, interface, function, and method declaration boundaries. This ensures code blocks remain semantically whole (so functions are not cut in half), making similarity matches highly accurate. Non-Go files fall back to standard line-count chunking.
3. **Dedicated Retrieval Tools**:
   - `codebase_search`: Queries the indexed codebase semantically for relevant code snippets (structs, functions, interfaces, methods) matching the query. Filters out historical conversations.
   - `memory_retriever`: Queries semantic memory of past agent runs, user requests, and compaction summaries. Filters out codebase files.
4. **Filtered vector queries**: Code and memory retrieval are separated at the ArtifactFlow vector query layer when using the ArtifactFlow-backed store.
5. **Customizable Threshold**: Cosine similarity defaults to a `0.70` matching threshold. This can be configured in your `.env` configuration file via `VECTOR_MEM_THRESHOLD=0.65`.
6. **Indexer status**: `GET /api/indexer/status` reports whether indexing is running, files scanned/indexed/skipped, chunks indexed, and the last error.

## Running

Launch the interactive CLI loop and the background dashboard server:
```bash
ca
```

To run in API/UI-only mode as a background service without the interactive terminal loop:
```powershell
$env:CODINGAGENTS_SERVER_ONLY = '1'
ca
```

The dashboard is accessible at `http://localhost:8080`.

Run a side-effect-light local preflight without starting ArtifactFlow sidecars:
```powershell
ca doctor
ca doctor --json
```

The doctor checks deployed assets, config/database writability, optional runtime binaries, llama.cpp model path assumptions, local GGUF endpoint sanity, and dashboard port availability. Workspace-absolute runtime paths are reported as warnings unless `CODINGAGENTS_DEV_WORKSPACE_FALLBACK=1` is set.

## HTTP API

- `POST /api/execute` starts a run and returns `{ "run_id": "..." }` with HTTP 202.
- `POST /api/execute/cancel` accepts `{ "run_id": "..." }` and cancels that active run.
- `GET /api/runs?limit=50` lists recent ArtifactFlow runs.
- `GET /api/runs/{run_id}` returns run metadata.
- `GET /api/runs/{run_id}/messages`, `/events`, `/artifacts`, `/timeline`, and `/snapshot` expose persisted run state.
- `GET /api/runs/{run_id}/approvals` lists reconstructed approval requests and decisions.
- `POST /api/runs/{run_id}/approvals/{approval_id}` accepts `{ "decision": "approved|denied", "reason": "..." }` to resolve and resume paused runs.
- `GET /api/health` returns logger diagnostics, blackboard schema version where available, memory presence, ArtifactFlow capability snapshots, and indexer status. The response sets `ok=false` when any reported capability is failed, cancelled, or missing.
- `GET /api/indexer/status` returns the current codebase indexer status.
- `GET/PUT /api/tool-policy` reads or updates the conservative tool policy config stored in `agents_config.json`.
- `POST /api/providers/probe` accepts `{ "url": "..." }` and returns provider Reachability and capability metadata.

## Validation

From the workspace root or this project directory, run:
```bash
go test ./...
```

Run the local dashboard/API smoke test from this project directory:
```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\smoke-server.ps1
```

The smoke script builds a temporary binary under `scratch\server-smoke`, runs `doctor --json`, starts server-only mode with `CODINGAGENTS_LOCAL_DB=1`, verifies `/`, `/api/health`, `/api/indexer/status`, and `/api/tool-policy`, then shuts down the process it started. A full installed-mode acceptance pass should additionally run against the globally installed `ca` command and real configured sidecars/provider endpoints.

After running the workspace installer, verify the deployed app without rebuilding:
```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\smoke-installed.ps1
```

The installed smoke checks `%LOCALAPPDATA%\Programs\CodingAgentsGO`, verifies aliases and deployed assets, runs the installed `doctor --json`, starts the installed binary in server-only mode using the global AppData database/config paths, probes the same dashboard/API endpoints, and stops the process it started.

Run the installed workflow smoke to exercise the API with a deterministic local OpenAI-compatible mock provider:
```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\smoke-installed-workflow.ps1
```

This starts the installed binary from an isolated `workflow-smoke` working directory, uses `CODINGAGENTS_LOCAL_DB=1` so global run state is not mutated, probes the mock provider, completes a planner run, triggers a coder writer approval, denies it through the approval API, and verifies the final denied run state.

Run the live installed llama.cpp smoke when the local GGUF runtime is available:
```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\smoke-installed-llamacpp.ps1
```

This starts the installed binary from an isolated `llamacpp-smoke` working directory, requires a local OpenAI-compatible llama.cpp URL and GGUF model from `OPENAI_URL`/`OPENAI_MODEL` or explicit parameters, disables provider fallbacks, and runs a planner-only request with `@llamacpp:<model>`. The configured endpoint probe is advisory by default because ArtifactFlow may allocate the llama.cpp port dynamically on the first GGUF run; pass `-RequireConfiguredEndpointReady` when validating a pre-warmed fixed endpoint.

## Runtime Artifacts

The application creates local runtime files that should remain untracked:

- `artifactflow_agents.db`
- `agents_config.json`
- `logs/`
- `.gocache/`
- `.shadow_workspaces/`
- compiled binaries such as `*.exe`

## Development Notes

- Keep `types.AgentConfig` as the canonical agent configuration type.
- Keep docs close to implemented behavior. Planned behavior belongs in roadmap sections.
- Treat `writer`, `dockerrunner`, long-running `coderunner`, and shadow merge/promotion as privileged surfaces.
- Full provider request payload logging is disabled by default. Set `CODINGAGENTS_LOG_PROMPTS=full` only for deliberate local debugging.
- Keep local smoke logs inside project `logs/` directories; do not write project logs to system temp directories.
- Production installs do not silently fall back to the development workspace. Use `CODINGAGENTS_DEV_WORKSPACE_FALLBACK=1` only for local development.
- Use `CODINGAGENTS_DISABLE_PROVIDER_FALLBACKS=1` for production smoke tests that must prove exactly one provider endpoint, especially local llama.cpp.
- Run metadata records build version, structured-output mode, provider URLs, and per-role prompt/config hashes for reproducibility.
- Preserve user edits in the existing dirty worktree; avoid broad formatting or unrelated UI churn.
- Treat structured artifacts as role-owned; system tool observations belong in execution logs and must not overwrite plans or reports.
