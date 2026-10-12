# Transformers Runtime

The **Transformers Runtime** integrates Hugging Face model pipelines into the [Python Sidecar](../python-sidecar/TDD.md) to perform computer vision, transcription, and embedding tasks.

## Security & Safetensors Enforcement

To conform with security guidelines, the orchestrator enforces **Safetensors** loading policies:
*   **Vulnerability Mitigation**: The runner must explicitly specify `use_safetensors=True` when loading Hugging Face checkpoint weights.
*   **Rationale**: This prevents arbitrary code execution vulnerabilities associated with legacy PyTorch pickle serialization formats (`.bin` or `.pt` weight files) and bypasses security mitigations on Windows.

## Windows / AMD GPU Acceleration

When running ML pipelines on AMD graphics hardware under Windows:
*   **DirectML**: The environment utilizes `torch-directml` as the backend acceleration layer.
*   **Version Pinning**: The PyTorch version is pinned (typically to `2.2.2`) to maintain DirectML library compatibility.

## Usage Example

Downstream applications can request a transformers-backed capability by registering models (such as image captioning models) that direct requests to the python-sidecar's endpoints, loading the weights from local caches.

See also: [Python Sidecar](../python-sidecar/TDD.md), [VideoScriptor integration](../reference/integrations/videoscriptor.md)
