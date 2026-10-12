# Rust Sidecar

The **Rust Sidecar** (`rust-sidecar`) is a high-performance computation daemon that handles CPU and GPU intensive pipeline tasks (such as image operations, file manipulation, and media frame extraction).

## Compilation and Lifecycle

*   **Source Code**: Located under the root `/rust-sidecar` directory.
*   **Compilation**: Compiled to `/bin/rust-sidecar.exe`. The source and target binaries are ignored in version control to keep dependencies local.
*   **Process Owner**: Spawning and termination are managed by the Go [Framework Package](../framework/TDD.md). The orchestrator terminates the process handle when the application exits.

## IPC Integration

*   **Named Pipe Interface**: On Windows, the sidecar listens on the `\\.\pipe\artifactflow_ipc` pipe.
*   **FlatBuffers Parser**: It receives binary packages matching the `schema/ipc.fbs` specifications. The sidecar deserializes these frames, executes the corresponding compute actions, and returns a FlatBuffers-encoded response frame.
*   **Core Tasks**: Includes media processing, frame decoding, and metadata extraction.

## Go Proxy Mapping

Downstream applications execute sidecar actions through proxy processors registered in the Go orchestrator. For example, `RustProxyProcessor` takes Go request payloads, forwards them across the named pipe, and parses the returned FlatBuffers stream into Go output structures.

See also: [IPC Communication Concept](../ipc/TDD.md), [Framework Package](../framework/TDD.md)
