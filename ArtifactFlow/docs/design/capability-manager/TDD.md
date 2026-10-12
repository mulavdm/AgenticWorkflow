# Capability Manager

To minimize startup overhead and prevent idle resource consumption, ArtifactFlow employs a **lazy initialization** pattern for all external model runtimes and secondary sidecar dependencies. Rather than starting all services immediately at boot, sidecars are spawned only when an active workflow requests a processor requiring that specific capability.

## Lifecycle States

Capabilities are registered in the system and monitored using the `LocalCapabilityManager`. A capability transition goes through the following states:

*   `registered`: The capability is known to the registry but not yet active.
*   `missing`: The required binaries or model weights are missing from the system.
*   `starting`: The sidecar process is spawning and waiting for health readiness.
*   `ready`: The IPC port or HTTP endpoint has responded successfully, and the runner is open for inference.
*   `failed`: The sidecar crashed or failed to signal readiness within the timeout boundary.
*   `cancelled`: The capability request was aborted.

## Lazy Startup Orchestration

1.  **Registration**: Downstream applications register custom model capabilities (e.g., specifying GGUF files for `llama.cpp` or custom Python endpoints).
2.  **Trigger**: The engine prepares a node for execution.
3.  **Check**: The registry resolves the target processor's capabilities. If the runner (e.g., `llama.cpp` or `transformers`) is not yet initialized, the framework launches it.
4.  **Health Check**: The orchestrator polls the sidecar's health URL (up to 300 seconds) or verifies socket connectivity.
5.  **Execution**: Once the status transitions to `ready`, the engine forwards execution to the processor.

## Downstream Inspection

While ArtifactFlow retains full lifecycle ownership of the processes, it exposes read-only status snapshots via `CapabilityStatus` and `CapabilityStatuses` APIs on the local manager and forwards them through `Registry` for downstream inspection. This allows downstream dashboards to report execution health panels without directly starting, stopping, or managing sidecar daemons.

Capability snapshots include the lifecycle status, last error, update timestamp, optional process id, endpoint URL, health URL, log path, startup duration, effective runtime args, and backend-specific metadata. Downstream applications may display these values, but lifecycle control remains in ArtifactFlow.

See also: [Framework Component](../framework/TDD.md), [VideoScriptor integration](../reference/integrations/videoscriptor.md)
