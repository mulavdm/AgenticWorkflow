# Framework Package

The `framework` package (`pkg/framework`) represents the main entry point to the orchestrator runtime, coordinating logging, database setup, registry setup, IPC channels, and sidecar processes.

## Main Struct: `ArtifactFlowApp`

Defined in [framework.go](../../../pkg/framework/framework.go), `ArtifactFlowApp` holds references to all core run components:

```go
type ArtifactFlowApp struct {
    Engine         *workflow.Engine
    Registry       *processor.Registry
    Models         *models.Registry
    sidecarCmd     *exec.Cmd
    customSidecars []*exec.Cmd
    ipcClient      *ipc.Client
    blackboard     *state.Blackboard
}
```

## Key APIs

### `Init(dbPath string) (*ArtifactFlowApp, error)`
Initializes the execution engine by executing the following steps:
1.  **Logger Initialization**: Directs output to `artifactflow.log`.
2.  **Rust Sidecar Startup**: Spawns `rust-sidecar.exe` (searching from executable path or `bin/`).
3.  **IPC Connection**: Sets up the client and dials the named pipe `\\.\pipe\artifactflow_ipc`.
4.  **Blackboard initialization**: Loads the SQLite file path (e.g. `blackboard.db`) and initializes database tables.
5.  **Processor Registration**: Registers standard Go processors (`ShellProcessor`, `ExportMarkdownProcessor`) and core routing processors (`ConstantProcessor`, `SwitchProcessor`), alongside Rust proxy channels (e.g., `video.extract_frames`).
6.  **Models Registry**: Hooks up local runner backends (`Ollama`, `LlamaCpp`, `Transformers`).
7.  **Engine Creation**: Initiates the topological execution runner.

### `RegisterSidecar(name string, dir string, command []string, healthUrl string) error`
Launches an external program (e.g. downstream Python app) inside a specific directory. The framework locks the flow and polls the `healthUrl` for up to 300 seconds until the service returns `200 OK`.

### `Close()`
Ensures clean shutdown by closing active IPC named pipe connections, killing spawned sidecar processes, locking/closing the SQLite blackboard database handle, and flushing logs.

See also: [State package](../state/TDD.md), [Workflow package](../workflow/TDD.md), [Blackboard Persistence](../blackboard/TDD.md)
