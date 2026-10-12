# IPC Communication & Named Pipes

To handle performance-critical operations (like media processing or model execution), ArtifactFlow offloads compute tasks to external sidecar programs. On Windows, communication between the Go orchestrator and the sidecars occurs over **Named Pipes**.

## Named Pipe Configuration

*   **Endpoint**: The framework communicates with the primary sidecar using the Windows named pipe:
    `\\.\pipe\artifactflow_ipc`
*   **Initialization**: The Go framework lazily detects or launches `rust-sidecar.exe` during startup and attempts to dial the pipe with a retry/timeout configuration (up to 10 seconds).

## FlatBuffers Message Serialization

To ensure zero-allocation performance and cross-language compatibility, the named pipe payload uses **FlatBuffers**. The schemas defined under `schema/ipc.fbs` generate the serialization Go structs in `pkg/ipc/artifactflow/`:

*   **`IPCMessage`**: The wrapper frame specifying payload type and version.
*   **`NodeExecutionRequest`**: Transports node configurations and upstream input artifacts.
*   **`NodeExecutionResponse`**: Carries execution results, exit codes, and output artifacts back to Go.
*   **`FrameExtractionRequest` / `FrameExtractionResponse`**: Specialized payloads for media workflows (e.g., frame extraction).

## Proxy Processors

From the engine's perspective, sidecar actions are registered as standard processors (e.g. `RustProxyProcessor`). 
1.  When executed, the proxy translates Go inputs into FlatBuffers buffers.
2.  It writes the buffer to the active named pipe.
3.  It blocks until the sidecar returns a response, parses the resulting FlatBuffers output, and returns it as native Go structures.

## Orphaned Process Safeguards

Aborted tests or daemon crashes on Windows can leave named pipes or SQLite DB locks hanging. The engineering guidelines dictate that developers must force-terminate sidecar instances and delete local databases when encountering named pipe conflicts or DB access exceptions.
```powershell
Stop-Process -Name rust-sidecar -Force -ErrorAction SilentlyContinue
```

See also: [IPC Package Component](../workflow/TDD.md), [Rust Sidecar Component](../rust-sidecar/TDD.md)
